package data

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MeetingRepo struct {
	db    *gorm.DB
	quota *MeetingQuotaRepo
}

var _ biz.MeetingRepo = (*MeetingRepo)(nil)

func NewMeetingRepo(db *gorm.DB) biz.MeetingRepo {
	return &MeetingRepo{db: db, quota: &MeetingQuotaRepo{db: db}}
}

func (r *MeetingRepo) FindByCreateIdempotency(ctx context.Context, userID, idempotencyKey string) (*biz.Meeting, error) {
	var row model.Meeting
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND create_idempotency_key = ? AND deleted_at IS NULL", userID, idempotencyKey).
		Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingNotFound
	}
	if err != nil {
		return nil, quotaDataError(err)
	}
	return toBizMeeting(&row)
}

func (r *MeetingRepo) CreateWithQuota(ctx context.Context, input biz.MeetingCreatePersistenceInput, quotaInput biz.MeetingQuotaReserveInput) (*biz.MeetingCreatePersistenceResult, error) {
	if quotaInput.MeetingID != input.MeetingID || quotaInput.UserID != input.UserID || quotaInput.ReservationID == "" {
		return nil, fmt.Errorf("meeting and quota reservation inputs do not match")
	}
	var output *biz.MeetingCreatePersistenceResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize a user's identical create key before checking for an existing
		// row. This preserves idempotency even when two requests arrive before
		// either transaction has inserted the meeting.
		if err := tx.WithContext(ctx).Exec(
			"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", input.UserID+":"+input.IdempotencyKey,
		).Error; err != nil {
			return err
		}
		var existing model.Meeting
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND create_idempotency_key = ? AND deleted_at IS NULL", input.UserID, input.IdempotencyKey).
			Take(&existing).Error
		if err == nil {
			if existing.CreateRequestFingerprint != input.RequestFingerprint {
				return biz.ErrMeetingIdempotencyConflict
			}
			meeting, err := toBizMeeting(&existing)
			if err != nil {
				return err
			}
			reservation, err := meetingToQuotaReservation(&existing)
			if err != nil {
				return err
			}
			output = &biz.MeetingCreatePersistenceResult{Meeting: meeting, Reservation: reservation, Existing: true}
			return nil
		}
		if !stderrors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		quotaResult, err := r.quota.reserveWithTx(ctx, tx, quotaInput)
		if err != nil {
			return err
		}
		if quotaResult == nil || quotaResult.Reservation == nil || quotaResult.Existing {
			return fmt.Errorf("new meeting quota reservation is invalid")
		}
		reservation := quotaResult.Reservation
		candidate := model.Meeting{
			ID: input.MeetingID, UserID: input.UserID, ReservationID: reservation.ID,
			CreateIdempotencyKey: input.IdempotencyKey, CreateRequestFingerprint: input.RequestFingerprint,
			Status: biz.MeetingStatusRecording.String(), TranscriptionStatus: biz.MeetingTranscriptionStatusPending.String(),
			Language: input.Language.String(), RetainAudio: input.RetainAudio,
			GrantedAudioSeconds: reservation.GrantedSeconds,
			QuotaPeriodStart:    reservation.Period.Start, QuotaPeriodEnd: reservation.Period.End,
			QuotaStatus: reservation.Status.String(), QuotaExpiresAt: reservation.ExpiresAt,
			TranscriptSegments: json.RawMessage("[]"), SummaryStatus: biz.MeetingSummaryStatusNotStarted.String(),
			SummaryContent: json.RawMessage("{}"), StartedAt: input.Now, CreatedAt: input.Now, UpdatedAt: input.Now,
		}
		if err := tx.WithContext(ctx).Create(&candidate).Error; err != nil {
			if stderrors.Is(err, gorm.ErrDuplicatedKey) {
				return biz.ErrMeetingIdempotencyConflict
			}
			return err
		}
		meeting, err := toBizMeeting(&candidate)
		if err != nil {
			return err
		}
		output = &biz.MeetingCreatePersistenceResult{Meeting: meeting, Reservation: reservation}
		return nil
	})
	if err != nil {
		if stderrors.Is(err, biz.ErrMeetingIdempotencyConflict) || stderrors.Is(err, biz.ErrMeetingQuotaExceeded) {
			return nil, err
		}
		return nil, quotaDataError(err)
	}
	return output, nil
}

func (r *MeetingRepo) MarkTranscriptionPrepared(ctx context.Context, userID, meetingID string, session *biz.MeetingTranscriptionSession, now time.Time) (*biz.Meeting, error) {
	if session == nil {
		return nil, fmt.Errorf("transcription session is required")
	}
	var output *biz.Meeting
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockMeeting(ctx, tx, userID, meetingID)
		if err != nil {
			return err
		}
		status, err := biz.ParseMeetingStatus(row.Status)
		if err != nil {
			return err
		}
		transcriptionStatus, err := biz.ParseMeetingTranscriptionStatus(row.TranscriptionStatus)
		if err != nil {
			return err
		}
		if row.TranscriptionSessionID != nil {
			if *row.TranscriptionSessionID != session.ID {
				return biz.ErrMeetingSessionMismatch
			}
			output, err = toBizMeeting(row)
			return err
		}
		if status != biz.MeetingStatusRecording || !transcriptionStatus.CanTransitionTo(biz.MeetingTranscriptionStatusConnecting) {
			return biz.ErrMeetingStateConflict
		}
		if session.Audio.Channels < math.MinInt16 || session.Audio.Channels > math.MaxInt16 {
			return fmt.Errorf("transcription audio channel count is out of range")
		}
		chunkDurationMS := session.Audio.ChunkDuration / time.Millisecond
		if chunkDurationMS < math.MinInt32 || chunkDurationMS > math.MaxInt32 {
			return fmt.Errorf("transcription audio chunk duration is out of range")
		}
		channels := int16(session.Audio.Channels)
		updates := map[string]any{
			"transcription_session_id": session.ID,
			"transcription_status":     biz.MeetingTranscriptionStatusConnecting.String(),
			"websocket_url":            session.WebSocketURL,
			"session_expires_at":       session.ExpiresAt,
			"audio_mime_type":          session.Audio.MIMEType,
			"audio_sample_rate":        session.Audio.SampleRate,
			"audio_channels":           channels,
			"audio_chunk_duration_ms":  int32(chunkDurationMS),
			"audio_max_chunk_bytes":    session.Audio.MaxChunkBytes,
			"updated_at":               now,
		}
		if err := tx.WithContext(ctx).Model(row).Updates(updates).Error; err != nil {
			return err
		}
		row.TranscriptionSessionID = &session.ID
		row.TranscriptionStatus = biz.MeetingTranscriptionStatusConnecting.String()
		row.UpdatedAt = now
		output, err = toBizMeeting(row)
		return err
	})
	if err != nil {
		return nil, mapMeetingDataError(err)
	}
	return output, nil
}

func (r *MeetingRepo) FailPreparationAndRelease(ctx context.Context, userID, meetingID string, now time.Time) (*biz.Meeting, error) {
	var output *biz.Meeting
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		meetingRow, err := lockMeeting(ctx, tx, userID, meetingID)
		if err != nil {
			return err
		}
		status, err := biz.ParseMeetingStatus(meetingRow.Status)
		if err != nil {
			return err
		}
		if status == biz.MeetingStatusFailed {
			output, err = toBizMeeting(meetingRow)
			return err
		}
		if !status.CanTransitionTo(biz.MeetingStatusFailed) {
			return biz.ErrMeetingStateConflict
		}
		if _, err := r.quota.finalizeWithTx(ctx, tx, biz.MeetingQuotaFinalizeInput{
			MeetingUsageFinalizeCommand: biz.MeetingUsageFinalizeCommand{
				ReservationID: meetingRow.ReservationID, MeetingID: meetingRow.ID,
				Reason: biz.MeetingUsageSettlementReasonPreparationFailed, FinalizedAt: now,
			}, Kind: biz.MeetingUsageKindASRAudio,
		}, meetingRow); err != nil {
			return err
		}
		updates := map[string]any{
			"status":               biz.MeetingStatusFailed.String(),
			"transcription_status": biz.MeetingTranscriptionStatusFailed.String(),
			"stopped_at":           now, "updated_at": now,
		}
		if err := tx.WithContext(ctx).Model(meetingRow).Updates(updates).Error; err != nil {
			return err
		}
		meetingRow.Status = biz.MeetingStatusFailed.String()
		meetingRow.TranscriptionStatus = biz.MeetingTranscriptionStatusFailed.String()
		meetingRow.StoppedAt = &now
		meetingRow.UpdatedAt = now
		output, err = toBizMeeting(meetingRow)
		return err
	})
	if err != nil {
		return nil, mapMeetingDataError(err)
	}
	return output, nil
}

func (r *MeetingRepo) Stop(ctx context.Context, userID, meetingID, idempotencyKey string, now time.Time) (*biz.Meeting, error) {
	var output *biz.Meeting
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var keyOwner model.Meeting
		err := tx.WithContext(ctx).
			Where("user_id = ? AND stop_idempotency_key = ? AND id <> ?", userID, idempotencyKey, meetingID).
			Take(&keyOwner).Error
		if err == nil {
			return biz.ErrMeetingStopKeyConflict
		}
		if !stderrors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row, err := lockMeeting(ctx, tx, userID, meetingID)
		if err != nil {
			return err
		}
		status, err := biz.ParseMeetingStatus(row.Status)
		if err != nil {
			return err
		}
		if status != biz.MeetingStatusRecording {
			output, err = toBizMeeting(row)
			return err
		}
		transcriptionStatus, err := biz.ParseMeetingTranscriptionStatus(row.TranscriptionStatus)
		if err != nil {
			return err
		}
		if !status.CanTransitionTo(biz.MeetingStatusProcessing) ||
			!transcriptionStatus.CanTransitionTo(biz.MeetingTranscriptionStatusFinishing) {
			return biz.ErrMeetingStateConflict
		}
		updates := map[string]any{
			"status":               biz.MeetingStatusProcessing.String(),
			"transcription_status": biz.MeetingTranscriptionStatusFinishing.String(),
			"stop_idempotency_key": idempotencyKey, "stopped_at": now, "updated_at": now,
		}
		if err := tx.WithContext(ctx).Model(row).Updates(updates).Error; err != nil {
			if stderrors.Is(err, gorm.ErrDuplicatedKey) {
				return biz.ErrMeetingStopKeyConflict
			}
			return err
		}
		row.Status = biz.MeetingStatusProcessing.String()
		row.TranscriptionStatus = biz.MeetingTranscriptionStatusFinishing.String()
		row.StopIdempotencyKey = &idempotencyKey
		row.StoppedAt = &now
		row.UpdatedAt = now
		output, err = toBizMeeting(row)
		return err
	})
	if err != nil {
		return nil, mapMeetingDataError(err)
	}
	return output, nil
}

func (r *MeetingRepo) Get(ctx context.Context, userID, meetingID string) (*biz.Meeting, error) {
	var row model.Meeting
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ? AND deleted_at IS NULL", meetingID, userID).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingNotFound
	}
	if err != nil {
		return nil, quotaDataError(err)
	}
	return toBizMeeting(&row)
}

func (r *MeetingRepo) AppendFinalTranscriptSegments(ctx context.Context, meetingID, sessionID, batchID string, segments []*biz.MeetingTranscriptSegment) (int64, error) {
	if len(segments) == 0 {
		return 0, fmt.Errorf("transcript segment batch is required")
	}
	for _, segment := range segments {
		if segment == nil {
			return 0, fmt.Errorf("transcript segment is required")
		}
	}
	_ = batchID // Segment identity and sequence provide the durable idempotency boundary.
	lastSequence := int64(0)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var meetingRow model.Meeting
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", meetingID).Take(&meetingRow).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrMeetingNotFound
		}
		if err != nil {
			return err
		}
		if meetingRow.TranscriptionSessionID == nil || *meetingRow.TranscriptionSessionID != sessionID {
			return biz.ErrMeetingSessionMismatch
		}
		stored, err := decodeTranscriptSegments(meetingRow.ID, meetingRow.TranscriptSegments)
		if err != nil {
			return err
		}
		bySequence := make(map[int64]*biz.MeetingTranscriptSegment, len(stored))
		byID := make(map[string]*biz.MeetingTranscriptSegment, len(stored))
		for _, segment := range stored {
			bySequence[segment.SequenceNo] = segment
			byID[segment.ID] = segment
		}
		for _, segment := range segments {
			existingBySequence, sequenceExists := bySequence[segment.SequenceNo]
			existingByID, idExists := byID[segment.ID]
			if sequenceExists || idExists {
				if !sequenceExists || !idExists || existingBySequence != existingByID || !sameTranscriptSegment(existingBySequence, segment) {
					return biz.ErrTranscriptSegmentConflict
				}
				continue
			}
			stored = append(stored, segment)
			bySequence[segment.SequenceNo] = segment
			byID[segment.ID] = segment
		}
		sort.Slice(stored, func(left, right int) bool { return stored[left].SequenceNo < stored[right].SequenceNo })
		lastSequence = meetingRow.TranscriptRevision
		if candidate := stored[len(stored)-1].SequenceNo; candidate > lastSequence {
			lastSequence = candidate
		}
		encoded, err := encodeTranscriptSegments(stored)
		if err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&meetingRow).
			Updates(map[string]any{"transcript_segments": encoded, "transcript_revision": lastSequence, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, mapMeetingDataError(err)
	}
	return lastSequence, nil
}

func (r *MeetingRepo) ValidateTranscriptionIdentity(ctx context.Context, meetingID, sessionID, reservationID string) error {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Meeting{}).
		Where("id = ? AND transcription_session_id = ? AND reservation_id = ? AND deleted_at IS NULL", meetingID, sessionID, reservationID).
		Count(&count).Error
	if err != nil {
		return quotaDataError(err)
	}
	if count == 0 {
		var meetingCount int64
		if err := r.db.WithContext(ctx).Model(&model.Meeting{}).Where("id = ? AND deleted_at IS NULL", meetingID).Count(&meetingCount).Error; err != nil {
			return quotaDataError(err)
		}
		if meetingCount == 0 {
			return biz.ErrMeetingNotFound
		}
		return biz.ErrMeetingSessionMismatch
	}
	return nil
}

func (r *MeetingRepo) FinalizeTranscription(ctx context.Context, input biz.FinalizeMeetingTranscriptionCommand) (*biz.FinalizeMeetingTranscriptionResult, error) {
	var output *biz.FinalizeMeetingTranscriptionResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var meetingRow model.Meeting
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", input.MeetingID).Take(&meetingRow).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrMeetingNotFound
		}
		if err != nil {
			return err
		}
		if meetingRow.TranscriptionSessionID == nil || *meetingRow.TranscriptionSessionID != input.SessionID || meetingRow.ReservationID != input.ReservationID {
			return biz.ErrMeetingSessionMismatch
		}
		currentMeetingStatus, err := biz.ParseMeetingStatus(meetingRow.Status)
		if err != nil {
			return err
		}
		currentTranscriptionStatus, err := biz.ParseMeetingTranscriptionStatus(meetingRow.TranscriptionStatus)
		if err != nil {
			return err
		}
		alreadyFinalized := currentMeetingStatus == input.MeetingStatus && currentTranscriptionStatus == input.TranscriptionStatus
		if input.MeetingStatus == biz.MeetingStatusProcessing && input.TranscriptionStatus == biz.MeetingTranscriptionStatusSucceeded &&
			currentTranscriptionStatus == biz.MeetingTranscriptionStatusSucceeded &&
			(currentMeetingStatus == biz.MeetingStatusCompleted || currentMeetingStatus == biz.MeetingStatusPartiallyCompleted) {
			// A summary callback may reach core before vision receives the response
			// for this idempotent transcription completion retry. Never move a
			// summary terminal meeting back to processing.
			alreadyFinalized = true
		}
		if !alreadyFinalized {
			// Meeting and transcription are two dimensions of the same lifecycle.
			// A client Stop may already move only the meeting dimension to
			// processing before vision reports finishing -> succeeded. Treat an
			// unchanged dimension as valid instead of requiring a self-transition
			// from the domain state machine.
			if !meetingFinalizationTransitionAllowed(
				currentMeetingStatus,
				input.MeetingStatus,
				currentTranscriptionStatus,
				input.TranscriptionStatus,
			) {
				return biz.ErrMeetingStateConflict
			}
		}

		usage, err := r.quota.finalizeWithTx(ctx, tx, biz.MeetingQuotaFinalizeInput{
			MeetingUsageFinalizeCommand: biz.MeetingUsageFinalizeCommand{
				ReservationID: input.ReservationID, MeetingID: input.MeetingID,
				TotalAcceptedSeconds: input.TotalAcceptedSeconds, ProviderUsageSeconds: input.ProviderUsageSeconds,
				Reason: input.SettlementReason, FinalizedAt: input.FinalizedAt,
			},
			Kind: biz.MeetingUsageKindASRAudio,
		}, &meetingRow)
		if err != nil {
			return err
		}
		if !alreadyFinalized {
			updates := map[string]any{
				"status": input.MeetingStatus.String(), "transcription_status": input.TranscriptionStatus.String(),
				"updated_at": input.FinalizedAt,
			}
			if meetingRow.StoppedAt == nil {
				updates["stopped_at"] = input.FinalizedAt
				meetingRow.StoppedAt = &input.FinalizedAt
			}
			if err := tx.WithContext(ctx).Model(&meetingRow).Updates(updates).Error; err != nil {
				return err
			}
			meetingRow.Status = input.MeetingStatus.String()
			meetingRow.TranscriptionStatus = input.TranscriptionStatus.String()
			meetingRow.UpdatedAt = input.FinalizedAt
		}
		meeting, err := toBizMeeting(&meetingRow)
		if err != nil {
			return err
		}
		output = &biz.FinalizeMeetingTranscriptionResult{Meeting: meeting, Usage: usage}
		return nil
	})
	if err != nil {
		return nil, mapMeetingDataError(err)
	}
	return output, nil
}

func meetingFinalizationTransitionAllowed(
	currentMeeting, targetMeeting biz.MeetingStatus,
	currentTranscription, targetTranscription biz.MeetingTranscriptionStatus,
) bool {
	meetingAllowed := currentMeeting == targetMeeting || currentMeeting.CanTransitionTo(targetMeeting)
	transcriptionAllowed := currentTranscription == targetTranscription || currentTranscription.CanTransitionTo(targetTranscription)
	return meetingAllowed && transcriptionAllowed
}

func (r *MeetingRepo) ListTranscriptSegments(ctx context.Context, input biz.ListMeetingTranscriptInput) ([]*biz.MeetingTranscriptSegment, bool, error) {
	if input.PageSize <= 0 || input.PageSize > 200 {
		return nil, false, fmt.Errorf("transcript page size is out of range")
	}
	var row model.Meeting
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ? AND deleted_at IS NULL", input.MeetingID, input.UserID).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, biz.ErrMeetingNotFound
	}
	if err != nil {
		return nil, false, quotaDataError(err)
	}
	stored, err := decodeTranscriptSegments(row.ID, row.TranscriptSegments)
	if err != nil {
		return nil, false, quotaDataError(err)
	}
	segments := make([]*biz.MeetingTranscriptSegment, 0, input.PageSize+1)
	for _, segment := range stored {
		if segment.SequenceNo > input.AfterSequence {
			segments = append(segments, segment)
			if len(segments) > int(input.PageSize) {
				break
			}
		}
	}
	hasMore := len(segments) > int(input.PageSize)
	if hasMore {
		segments = segments[:input.PageSize]
	}
	return segments, hasMore, nil
}

func lockMeeting(ctx context.Context, tx *gorm.DB, userID, meetingID string) (*model.Meeting, error) {
	var row model.Meeting
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND user_id = ? AND deleted_at IS NULL", meetingID, userID).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingNotFound
	}
	return &row, err
}

func toBizMeeting(row *model.Meeting) (*biz.Meeting, error) {
	if row == nil {
		return nil, fmt.Errorf("meeting row is nil")
	}
	status, err := biz.ParseMeetingStatus(row.Status)
	if err != nil {
		return nil, err
	}
	transcriptionStatus, err := biz.ParseMeetingTranscriptionStatus(row.TranscriptionStatus)
	if err != nil {
		return nil, err
	}
	language, err := biz.ParseMeetingLanguage(row.Language)
	if err != nil {
		return nil, err
	}
	summaryStatus, err := biz.ParseMeetingSummaryStatus(row.SummaryStatus)
	if err != nil {
		return nil, err
	}
	meeting := &biz.Meeting{
		ID: row.ID, UserID: row.UserID, ReservationID: row.ReservationID,
		CreateIdempotencyKey: row.CreateIdempotencyKey, CreateRequestFingerprint: row.CreateRequestFingerprint,
		Status: status, TranscriptionStatus: transcriptionStatus, Language: language,
		RetainAudio: row.RetainAudio, GrantedAudioSeconds: row.GrantedAudioSeconds,
		TranscriptRevision: row.TranscriptRevision, SummaryStatus: summaryStatus, SummaryVersion: row.SummaryVersion,
		StartedAt: row.StartedAt, StoppedAt: row.StoppedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.StopIdempotencyKey != nil {
		meeting.StopIdempotencyKey = *row.StopIdempotencyKey
	}
	if row.TranscriptionSessionID != nil {
		meeting.TranscriptionSessionID = *row.TranscriptionSessionID
	}
	return meeting, nil
}

type persistedTranscriptSegment struct {
	ID            string    `json:"id"`
	SequenceNo    int64     `json:"sequence_no"`
	StartOffsetMS int64     `json:"start_offset_ms"`
	EndOffsetMS   int64     `json:"end_offset_ms"`
	SpeakerLabel  string    `json:"speaker_label"`
	Content       string    `json:"content"`
	Language      string    `json:"language"`
	Confidence    *float32  `json:"confidence,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func encodeTranscriptSegments(segments []*biz.MeetingTranscriptSegment) (json.RawMessage, error) {
	rows := make([]persistedTranscriptSegment, 0, len(segments))
	for _, segment := range segments {
		if segment == nil {
			return nil, fmt.Errorf("transcript segment is nil")
		}
		rows = append(rows, persistedTranscriptSegment{
			ID: segment.ID, SequenceNo: segment.SequenceNo,
			StartOffsetMS: int64(segment.StartOffset / time.Millisecond), EndOffsetMS: int64(segment.EndOffset / time.Millisecond),
			SpeakerLabel: segment.SpeakerLabel, Content: segment.Content, Language: segment.Language.String(),
			Confidence: segment.Confidence, CreatedAt: segment.CreatedAt,
		})
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("encode meeting transcript segments: %w", err)
	}
	return encoded, nil
}

func decodeTranscriptSegments(meetingID string, raw json.RawMessage) ([]*biz.MeetingTranscriptSegment, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []*biz.MeetingTranscriptSegment{}, nil
	}
	var rows []persistedTranscriptSegment
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode meeting transcript segments: %w", err)
	}
	segments := make([]*biz.MeetingTranscriptSegment, 0, len(rows))
	for index := range rows {
		segment, err := persistedTranscriptSegmentToBiz(meetingID, &rows[index])
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	sort.Slice(segments, func(left, right int) bool { return segments[left].SequenceNo < segments[right].SequenceNo })
	return segments, nil
}

func persistedTranscriptSegmentToBiz(meetingID string, row *persistedTranscriptSegment) (*biz.MeetingTranscriptSegment, error) {
	if row == nil || row.StartOffsetMS < 0 || row.EndOffsetMS < row.StartOffsetMS ||
		row.StartOffsetMS > math.MaxInt64/int64(time.Millisecond) || row.EndOffsetMS > math.MaxInt64/int64(time.Millisecond) {
		return nil, fmt.Errorf("stored transcript segment offsets are invalid")
	}
	language, err := biz.ParseMeetingLanguage(row.Language)
	if err != nil {
		return nil, err
	}
	return &biz.MeetingTranscriptSegment{
		ID: row.ID, MeetingID: meetingID, SequenceNo: row.SequenceNo,
		StartOffset:  time.Duration(row.StartOffsetMS) * time.Millisecond,
		EndOffset:    time.Duration(row.EndOffsetMS) * time.Millisecond,
		SpeakerLabel: row.SpeakerLabel, Content: row.Content, Language: language,
		Confidence: row.Confidence, CreatedAt: row.CreatedAt,
	}, nil
}

func sameTranscriptSegment(stored, incoming *biz.MeetingTranscriptSegment) bool {
	if stored == nil || incoming == nil || stored.ID != incoming.ID || stored.SequenceNo != incoming.SequenceNo ||
		stored.StartOffset != incoming.StartOffset || stored.EndOffset != incoming.EndOffset ||
		stored.SpeakerLabel != incoming.SpeakerLabel || stored.Content != incoming.Content ||
		stored.Language != incoming.Language || !stored.CreatedAt.Equal(incoming.CreatedAt) {
		return false
	}
	if stored.Confidence == nil || incoming.Confidence == nil {
		return stored.Confidence == nil && incoming.Confidence == nil
	}
	return *stored.Confidence == *incoming.Confidence
}

func mapMeetingDataError(err error) error {
	if stderrors.Is(err, biz.ErrMeetingNotFound) || stderrors.Is(err, biz.ErrMeetingIdempotencyConflict) ||
		stderrors.Is(err, biz.ErrMeetingStopKeyConflict) || stderrors.Is(err, biz.ErrMeetingStateConflict) ||
		stderrors.Is(err, biz.ErrMeetingSessionMismatch) || stderrors.Is(err, biz.ErrTranscriptSegmentConflict) ||
		stderrors.Is(err, biz.ErrMeetingQuotaReservationNotFound) {
		return err
	}
	return quotaDataError(err)
}
