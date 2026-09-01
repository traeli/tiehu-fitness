package data

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TranscriptionRepo struct {
	db *gorm.DB
}

var _ biz.TranscriptionSessionRepo = (*TranscriptionRepo)(nil)
var _ biz.ASRAttemptRepo = (*TranscriptionRepo)(nil)

func NewTranscriptionRepo(db *gorm.DB) (*TranscriptionRepo, error) {
	if db == nil {
		return nil, fmt.Errorf("transcription database is required")
	}
	return &TranscriptionRepo{db: db}, nil
}

func (r *TranscriptionRepo) CreateOrGet(ctx context.Context, session *biz.TranscriptionSession) (*biz.TranscriptionSession, bool, error) {
	if ctx == nil || session == nil {
		return nil, false, fmt.Errorf("transcription session and context are required")
	}
	row := transcriptionModelFromBiz(session)
	var stored model.TranscriptionSession
	wasCreated := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return fmt.Errorf("create transcription session: %w", result.Error)
		}
		if result.RowsAffected == 1 {
			job := model.ASRJob{
				SessionID: row.ID, Status: string(biz.ASRJobStatusPending), Provider: row.Provider,
				AttemptCount: 0, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			}
			if err := tx.Create(&job).Error; err != nil {
				return fmt.Errorf("create ASR job: %w", err)
			}
			stored = row
			wasCreated = true
			return nil
		}
		if err := tx.Where("idempotency_key = ? OR meeting_id = ?", session.IdempotencyKey, session.MeetingID).
			Order("created_at ASC").First(&stored).Error; err != nil {
			return translateTranscriptionDBError("find idempotent transcription session", err)
		}
		if stored.IdempotencyKey != session.IdempotencyKey || stored.MeetingID != session.MeetingID ||
			stored.UserID != session.UserID || stored.ReservationID != session.ReservationID ||
			stored.Language != string(session.Language) || stored.GrantedAudioMilliseconds != session.GrantedAudioDuration.Duration().Milliseconds() {
			return biz.ErrTranscriptionConflict
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	mapped, err := transcriptionModelToBiz(&stored)
	return mapped, wasCreated, err
}

func (r *TranscriptionRepo) ListStalePending(ctx context.Context, before time.Time, limit int) ([]*biz.TranscriptionSession, error) {
	if ctx == nil || before.IsZero() || limit <= 0 || limit > 1_000 {
		return nil, fmt.Errorf("stale pending transcription query is invalid")
	}
	var rows []model.TranscriptionSession
	if err := r.db.WithContext(ctx).
		Where("status = ? AND updated_at <= ?", biz.TranscriptionSessionStatusPending, before.UTC()).
		Order("updated_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list stale pending transcription sessions: %w", err)
	}
	sessions := make([]*biz.TranscriptionSession, 0, len(rows))
	for index := range rows {
		session, err := transcriptionModelToBiz(&rows[index])
		if err != nil {
			return nil, fmt.Errorf("map stale pending transcription session: %w", err)
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (r *TranscriptionRepo) StartAttempt(ctx context.Context, sessionID string, provider biz.ASRProviderName) (*biz.ASRAttempt, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if _, err := biz.ParseASRProviderName(string(provider)); err != nil {
		return nil, err
	}
	var attempt *biz.ASRAttempt
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job model.ASRJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id = ?", sessionID).First(&job).Error; err != nil {
			return translateTranscriptionDBError("lock ASR job", err)
		}
		status, err := biz.ParseASRJobStatus(job.Status)
		if err != nil {
			return err
		}
		if status != biz.ASRJobStatusPending && status != biz.ASRJobStatusFailed {
			return biz.ErrTranscriptionStateConflict
		}
		if job.AttemptCount == math.MaxInt32 {
			return fmt.Errorf("ASR attempt count is out of range")
		}
		now := time.Now().UTC()
		number := job.AttemptCount + 1
		row := model.AIJobAttempt{
			ID: uuid.NewString(), JobID: job.ID, AttemptNumber: number, Provider: string(provider),
			Status: string(biz.ASRAttemptStatusProcessing), ErrorCode: "", StartedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("create ASR attempt: %w", err)
		}
		if err := tx.Model(&job).Updates(map[string]any{
			"status": string(biz.ASRJobStatusProcessing), "attempt_count": number, "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("start ASR job attempt: %w", err)
		}
		attempt = &biz.ASRAttempt{
			ID: row.ID, SessionID: sessionID, Provider: provider, Status: biz.ASRAttemptStatusProcessing,
			AttemptNumber: number, StartedAt: now,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attempt, nil
}

func (r *TranscriptionRepo) FinishAttempt(ctx context.Context, attemptID string, next biz.ASRAttemptStatus, errorCode string) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	if next != biz.ASRAttemptStatusSucceeded && next != biz.ASRAttemptStatusFailed && next != biz.ASRAttemptStatusCancelled {
		return fmt.Errorf("ASR attempt terminal status is invalid")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var attempt model.AIJobAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", attemptID).First(&attempt).Error; err != nil {
			return translateTranscriptionDBError("lock ASR attempt", err)
		}
		current, err := biz.ParseASRAttemptStatus(attempt.Status)
		if err != nil {
			return err
		}
		if current == next {
			return nil
		}
		if current != biz.ASRAttemptStatusProcessing {
			return biz.ErrTranscriptionStateConflict
		}
		now := time.Now().UTC()
		if err := tx.Model(&attempt).Updates(map[string]any{
			"status": string(next), "error_code": errorCode, "finished_at": now,
		}).Error; err != nil {
			return fmt.Errorf("finish ASR attempt: %w", err)
		}
		jobStatus := biz.ASRJobStatusFailed
		switch next {
		case biz.ASRAttemptStatusSucceeded:
			jobStatus = biz.ASRJobStatusSucceeded
		case biz.ASRAttemptStatusCancelled:
			jobStatus = biz.ASRJobStatusCancelled
		case biz.ASRAttemptStatusFailed:
			jobStatus = biz.ASRJobStatusFailed
		}
		if err := tx.Model(&model.ASRJob{}).Where("id = ?", attempt.JobID).Updates(map[string]any{
			"status": string(jobStatus), "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("finish ASR job: %w", err)
		}
		return nil
	})
}

func (r *TranscriptionRepo) Get(ctx context.Context, sessionID, meetingID string) (*biz.TranscriptionSession, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	var row model.TranscriptionSession
	query := r.db.WithContext(ctx).Where("id = ?", sessionID)
	if meetingID != "" {
		query = query.Where("meeting_id = ?", meetingID)
	}
	if err := query.First(&row).Error; err != nil {
		return nil, translateTranscriptionDBError("get transcription session", err)
	}
	return transcriptionModelToBiz(&row)
}

func (r *TranscriptionRepo) Transition(ctx context.Context, sessionID string, allowed []biz.TranscriptionSessionStatus, next biz.TranscriptionSessionStatus, failureCode string) (*biz.TranscriptionSession, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	var result *biz.TranscriptionSession
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.TranscriptionSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sessionID).First(&row).Error; err != nil {
			return translateTranscriptionDBError("lock transcription session", err)
		}
		current, err := biz.ParseTranscriptionSessionStatus(row.Status)
		if err != nil {
			return fmt.Errorf("parse stored transcription status: %w", err)
		}
		if !containsTranscriptionStatus(allowed, current) || !current.CanTransitionTo(next) {
			return biz.ErrTranscriptionStateConflict
		}
		now := time.Now().UTC()
		updates := map[string]any{"status": string(next), "updated_at": now, "failure_code": failureCode}
		if next == biz.TranscriptionSessionStatusStreaming && row.StartedAt == nil {
			updates["started_at"] = now
		}
		if next.IsTerminal() {
			updates["finished_at"] = now
		}
		if err := tx.Model(&model.TranscriptionSession{}).Where("id = ?", sessionID).Updates(updates).Error; err != nil {
			return fmt.Errorf("update transcription state: %w", err)
		}
		if next.IsTerminal() {
			payload, err := transcriptionOutboxPayload(row.ID, row.MeetingID)
			if err != nil {
				return err
			}
			outbox := model.TranscriptionOutbox{
				SessionID: row.ID, EventType: string(biz.TranscriptionOutboxEventTypeUsageReady), Payload: payload,
				Status: string(biz.TranscriptionOutboxStatusPending), AvailableAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&outbox).Error; err != nil {
				return fmt.Errorf("persist terminal transcription outbox: %w", err)
			}
		}
		if err := tx.Where("id = ?", sessionID).First(&row).Error; err != nil {
			return fmt.Errorf("reload transcription session: %w", err)
		}
		result, err = transcriptionModelToBiz(&row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *TranscriptionRepo) AcceptAudio(ctx context.Context, sessionID string, sequence, sizeBytes, grantedBytes int64) (*biz.AcceptAudioResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if sequence <= 0 || sizeBytes <= 0 || sizeBytes > math.MaxInt32 || grantedBytes <= 0 {
		return nil, biz.ErrTranscriptionSequence
	}
	var result *biz.AcceptAudioResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.TranscriptionSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sessionID).First(&row).Error; err != nil {
			return translateTranscriptionDBError("lock transcription session for audio", err)
		}
		var duplicateCount int64
		if err := tx.Model(&model.TranscriptionAudioChunk{}).
			Where("session_id = ? AND sequence_no = ?", sessionID, sequence).Count(&duplicateCount).Error; err != nil {
			return fmt.Errorf("check duplicate audio chunk: %w", err)
		}
		if duplicateCount > 0 {
			snapshot, err := transcriptionModelToBiz(&row)
			if err != nil {
				return err
			}
			result = &biz.AcceptAudioResult{Session: snapshot, Duplicate: true, LimitReached: row.Status == string(biz.TranscriptionSessionStatusFinishing)}
			return nil
		}
		status, err := biz.ParseTranscriptionSessionStatus(row.Status)
		if err != nil {
			return fmt.Errorf("parse stored transcription status: %w", err)
		}
		if status != biz.TranscriptionSessionStatusStreaming {
			return biz.ErrTranscriptionStateConflict
		}
		if sequence != row.LastAudioSequence+1 {
			return biz.ErrTranscriptionSequence
		}
		if row.AcceptedAudioBytes > grantedBytes-sizeBytes {
			now := time.Now().UTC()
			if err := tx.Model(&row).Updates(map[string]any{"status": string(biz.TranscriptionSessionStatusFinishing), "updated_at": now}).Error; err != nil {
				return fmt.Errorf("finish transcription at audio limit: %w", err)
			}
			row.Status = string(biz.TranscriptionSessionStatusFinishing)
			row.UpdatedAt = now
			snapshot, err := transcriptionModelToBiz(&row)
			if err != nil {
				return err
			}
			result = &biz.AcceptAudioResult{Session: snapshot, LimitReached: true}
			return nil
		}
		now := time.Now().UTC()
		chunk := model.TranscriptionAudioChunk{SessionID: sessionID, SequenceNo: sequence, SizeBytes: int32(sizeBytes), CreatedAt: now}
		if err := tx.Create(&chunk).Error; err != nil {
			return fmt.Errorf("record accepted audio chunk: %w", err)
		}
		accepted := row.AcceptedAudioBytes + sizeBytes
		updates := map[string]any{"accepted_audio_bytes": accepted, "last_audio_sequence": sequence, "updated_at": now}
		limitReached := accepted == grantedBytes
		if limitReached {
			updates["status"] = string(biz.TranscriptionSessionStatusFinishing)
		}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return fmt.Errorf("update accepted audio usage: %w", err)
		}
		row.AcceptedAudioBytes = accepted
		row.LastAudioSequence = sequence
		row.UpdatedAt = now
		if limitReached {
			row.Status = string(biz.TranscriptionSessionStatusFinishing)
		}
		snapshot, err := transcriptionModelToBiz(&row)
		if err != nil {
			return err
		}
		result = &biz.AcceptAudioResult{Session: snapshot, LimitReached: limitReached}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *TranscriptionRepo) Complete(ctx context.Context, sessionID string, segments []biz.TranscriptSegment) (*biz.TranscriptionSession, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	for i := range segments {
		if err := segments[i].Validate(); err != nil {
			return nil, fmt.Errorf("validate final segment %d: %w", i, err)
		}
		if segments[i].SessionID != "" && segments[i].SessionID != sessionID {
			return nil, fmt.Errorf("final segment session mismatch")
		}
	}
	var result *biz.TranscriptionSession
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.TranscriptionSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sessionID).First(&row).Error; err != nil {
			return translateTranscriptionDBError("lock transcription session for completion", err)
		}
		status, err := biz.ParseTranscriptionSessionStatus(row.Status)
		if err != nil {
			return fmt.Errorf("parse stored transcription status: %w", err)
		}
		if status == biz.TranscriptionSessionStatusSucceeded {
			result, err = transcriptionModelToBiz(&row)
			return err
		}
		if status != biz.TranscriptionSessionStatusFinishing {
			return biz.ErrTranscriptionStateConflict
		}
		now := time.Now().UTC()
		for i := range segments {
			segment := segments[i]
			createdAt := segment.CreatedAt
			if createdAt.IsZero() {
				createdAt = now
			}
			persisted := model.TranscriptionFinalSegment{
				ID: segment.ID, SessionID: sessionID, SequenceNo: segment.Sequence,
				StartMilliseconds: segment.StartOffset.Milliseconds(), EndMilliseconds: segment.EndOffset.Milliseconds(),
				SpeakerLabel: segment.SpeakerLabel, Content: segment.Content, Language: string(segment.Language),
				Confidence: segment.Confidence, CreatedAt: createdAt,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&persisted).Error; err != nil {
				return fmt.Errorf("persist final transcript segment: %w", err)
			}
		}
		payload, err := transcriptionOutboxPayload(row.ID, row.MeetingID)
		if err != nil {
			return fmt.Errorf("encode transcription outbox payload: %w", err)
		}
		outboxRows := []model.TranscriptionOutbox{
			{SessionID: row.ID, EventType: string(biz.TranscriptionOutboxEventTypeFinalTranscriptReady), Payload: payload, Status: string(biz.TranscriptionOutboxStatusPending), AvailableAt: now, CreatedAt: now, UpdatedAt: now},
			{SessionID: row.ID, EventType: string(biz.TranscriptionOutboxEventTypeUsageReady), Payload: payload, Status: string(biz.TranscriptionOutboxStatusPending), AvailableAt: now, CreatedAt: now, UpdatedAt: now},
		}
		if err := tx.Create(&outboxRows).Error; err != nil {
			return fmt.Errorf("persist transcription outbox: %w", err)
		}
		if err := tx.Model(&row).Updates(map[string]any{
			"status": string(biz.TranscriptionSessionStatusSucceeded), "failure_code": "", "finished_at": now, "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("complete transcription session: %w", err)
		}
		row.Status = string(biz.TranscriptionSessionStatusSucceeded)
		row.FailureCode = ""
		row.FinishedAt = &now
		row.UpdatedAt = now
		result, err = transcriptionModelToBiz(&row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func transcriptionModelFromBiz(session *biz.TranscriptionSession) model.TranscriptionSession {
	return model.TranscriptionSession{
		ID: session.ID, ProviderConfigID: session.ProviderConfigID,
		MeetingID: session.MeetingID, UserID: session.UserID, ReservationID: session.ReservationID,
		Language: string(session.Language), Status: string(session.Status), Provider: string(session.Provider),
		IdempotencyKey: session.IdempotencyKey, GrantedAudioMilliseconds: session.GrantedAudioDuration.Duration().Milliseconds(),
		AcceptedAudioBytes: session.AcceptedAudioBytes, LastAudioSequence: session.LastAudioSequence,
		FailureCode: session.FailureCode, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}
}

func transcriptionModelToBiz(row *model.TranscriptionSession) (*biz.TranscriptionSession, error) {
	if row == nil {
		return nil, fmt.Errorf("transcription row is required")
	}
	status, err := biz.ParseTranscriptionSessionStatus(row.Status)
	if err != nil {
		return nil, err
	}
	provider, err := biz.ParseASRProviderName(row.Provider)
	if err != nil {
		return nil, err
	}
	language, err := biz.ParseMeetingLanguage(row.Language)
	if err != nil {
		return nil, err
	}
	return &biz.TranscriptionSession{
		ID: row.ID, ProviderConfigID: row.ProviderConfigID,
		MeetingID: row.MeetingID, UserID: row.UserID, ReservationID: row.ReservationID,
		Language: language, Status: status, Provider: provider, IdempotencyKey: row.IdempotencyKey,
		GrantedAudioDuration: biz.GrantedAudioDuration(time.Duration(row.GrantedAudioMilliseconds) * time.Millisecond),
		AcceptedAudioBytes:   row.AcceptedAudioBytes, LastAudioSequence: row.LastAudioSequence,
		FailureCode: row.FailureCode, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		FinishedAt: row.FinishedAt,
	}, nil
}

func transcriptionOutboxPayload(sessionID, meetingID string) (json.RawMessage, error) {
	payload, err := json.Marshal(struct {
		SessionID string `json:"session_id"`
		MeetingID string `json:"meeting_id"`
	}{SessionID: sessionID, MeetingID: meetingID})
	if err != nil {
		return nil, fmt.Errorf("encode transcription outbox payload: %w", err)
	}
	return payload, nil
}

func containsTranscriptionStatus(statuses []biz.TranscriptionSessionStatus, target biz.TranscriptionSessionStatus) bool {
	for _, status := range statuses {
		if status == target {
			return true
		}
	}
	return false
}

func translateTranscriptionDBError(operation string, err error) error {
	switch {
	case stderrors.Is(err, gorm.ErrRecordNotFound):
		return biz.ErrTranscriptionNotFound
	case stderrors.Is(err, gorm.ErrDuplicatedKey):
		return biz.ErrTranscriptionConflict
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
}
