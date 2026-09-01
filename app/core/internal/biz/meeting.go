package biz

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

const (
	defaultTranscriptPageSize = int32(50)
	maxTranscriptPageSize     = int32(200)
	maxTranscriptBatchSize    = 100
	maxTranscriptContentRunes = 20_000
	maxSpeakerLabelRunes      = 80
)

type MeetingStatus uint8

const (
	MeetingStatusUnspecified MeetingStatus = iota
	MeetingStatusRecording
	MeetingStatusProcessing
	MeetingStatusCompleted
	MeetingStatusPartiallyCompleted
	MeetingStatusFailed
	MeetingStatusCancelled
)

func (s MeetingStatus) String() string {
	switch s {
	case MeetingStatusRecording:
		return "recording"
	case MeetingStatusProcessing:
		return "processing"
	case MeetingStatusCompleted:
		return "completed"
	case MeetingStatusPartiallyCompleted:
		return "partially_completed"
	case MeetingStatusFailed:
		return "failed"
	case MeetingStatusCancelled:
		return "cancelled"
	default:
		return ""
	}
}

func ParseMeetingStatus(raw string) (MeetingStatus, error) {
	switch raw {
	case MeetingStatusRecording.String():
		return MeetingStatusRecording, nil
	case MeetingStatusProcessing.String():
		return MeetingStatusProcessing, nil
	case MeetingStatusCompleted.String():
		return MeetingStatusCompleted, nil
	case MeetingStatusPartiallyCompleted.String():
		return MeetingStatusPartiallyCompleted, nil
	case MeetingStatusFailed.String():
		return MeetingStatusFailed, nil
	case MeetingStatusCancelled.String():
		return MeetingStatusCancelled, nil
	default:
		return MeetingStatusUnspecified, fmt.Errorf("unknown meeting status %q", raw)
	}
}

func (s MeetingStatus) CanTransitionTo(next MeetingStatus) bool {
	switch s {
	case MeetingStatusRecording:
		// vision may durably finish before the client sends StopMeeting. Allow the
		// reliable completion command to collapse recording -> processing -> completed.
		return next == MeetingStatusProcessing || next == MeetingStatusCompleted || next == MeetingStatusFailed || next == MeetingStatusCancelled
	case MeetingStatusProcessing:
		return next == MeetingStatusCompleted || next == MeetingStatusPartiallyCompleted || next == MeetingStatusFailed || next == MeetingStatusCancelled
	case MeetingStatusPartiallyCompleted:
		return next == MeetingStatusCompleted || next == MeetingStatusFailed
	default:
		return false
	}
}

func (s MeetingStatus) IsTerminal() bool {
	return s == MeetingStatusCompleted || s == MeetingStatusFailed || s == MeetingStatusCancelled
}

type MeetingTranscriptionStatus uint8

const (
	MeetingTranscriptionStatusUnspecified MeetingTranscriptionStatus = iota
	MeetingTranscriptionStatusPending
	MeetingTranscriptionStatusConnecting
	MeetingTranscriptionStatusStreaming
	MeetingTranscriptionStatusFinishing
	MeetingTranscriptionStatusSucceeded
	MeetingTranscriptionStatusFailed
	MeetingTranscriptionStatusCancelled
	MeetingTranscriptionStatusExpired
)

func (s MeetingTranscriptionStatus) String() string {
	switch s {
	case MeetingTranscriptionStatusPending:
		return "pending"
	case MeetingTranscriptionStatusConnecting:
		return "connecting"
	case MeetingTranscriptionStatusStreaming:
		return "streaming"
	case MeetingTranscriptionStatusFinishing:
		return "finishing"
	case MeetingTranscriptionStatusSucceeded:
		return "succeeded"
	case MeetingTranscriptionStatusFailed:
		return "failed"
	case MeetingTranscriptionStatusCancelled:
		return "cancelled"
	case MeetingTranscriptionStatusExpired:
		return "expired"
	default:
		return ""
	}
}

func ParseMeetingTranscriptionStatus(raw string) (MeetingTranscriptionStatus, error) {
	for _, status := range []MeetingTranscriptionStatus{
		MeetingTranscriptionStatusPending, MeetingTranscriptionStatusConnecting,
		MeetingTranscriptionStatusStreaming, MeetingTranscriptionStatusFinishing,
		MeetingTranscriptionStatusSucceeded, MeetingTranscriptionStatusFailed,
		MeetingTranscriptionStatusCancelled, MeetingTranscriptionStatusExpired,
	} {
		if raw == status.String() {
			return status, nil
		}
	}
	return MeetingTranscriptionStatusUnspecified, fmt.Errorf("unknown meeting transcription status %q", raw)
}

func (s MeetingTranscriptionStatus) CanTransitionTo(next MeetingTranscriptionStatus) bool {
	switch s {
	case MeetingTranscriptionStatusPending:
		return next == MeetingTranscriptionStatusConnecting || next == MeetingTranscriptionStatusFailed || next == MeetingTranscriptionStatusCancelled
	case MeetingTranscriptionStatusConnecting:
		return next == MeetingTranscriptionStatusStreaming || next == MeetingTranscriptionStatusFinishing || next == MeetingTranscriptionStatusSucceeded || next == MeetingTranscriptionStatusFailed || next == MeetingTranscriptionStatusCancelled || next == MeetingTranscriptionStatusExpired
	case MeetingTranscriptionStatusStreaming:
		return next == MeetingTranscriptionStatusFinishing || next == MeetingTranscriptionStatusSucceeded || next == MeetingTranscriptionStatusFailed || next == MeetingTranscriptionStatusCancelled || next == MeetingTranscriptionStatusExpired
	case MeetingTranscriptionStatusFinishing:
		return next == MeetingTranscriptionStatusSucceeded || next == MeetingTranscriptionStatusFailed || next == MeetingTranscriptionStatusCancelled || next == MeetingTranscriptionStatusExpired
	default:
		return false
	}
}

func (s MeetingTranscriptionStatus) IsTerminal() bool {
	return s == MeetingTranscriptionStatusSucceeded || s == MeetingTranscriptionStatusFailed ||
		s == MeetingTranscriptionStatusCancelled || s == MeetingTranscriptionStatusExpired
}

type MeetingLanguage uint8

const (
	MeetingLanguageUnspecified MeetingLanguage = iota
	MeetingLanguageAuto
	MeetingLanguageZhCN
	MeetingLanguageEnUS
)

func (l MeetingLanguage) String() string {
	switch l {
	case MeetingLanguageAuto:
		return "auto"
	case MeetingLanguageZhCN:
		return "zh_cn"
	case MeetingLanguageEnUS:
		return "en_us"
	default:
		return ""
	}
}

func ParseMeetingLanguage(raw string) (MeetingLanguage, error) {
	switch raw {
	case MeetingLanguageAuto.String():
		return MeetingLanguageAuto, nil
	case MeetingLanguageZhCN.String():
		return MeetingLanguageZhCN, nil
	case MeetingLanguageEnUS.String():
		return MeetingLanguageEnUS, nil
	default:
		return MeetingLanguageUnspecified, fmt.Errorf("unknown meeting language %q", raw)
	}
}

type Meeting struct {
	ID                       string
	UserID                   string
	ReservationID            string
	CreateIdempotencyKey     string
	CreateRequestFingerprint string
	StopIdempotencyKey       string
	Status                   MeetingStatus
	TranscriptionStatus      MeetingTranscriptionStatus
	Language                 MeetingLanguage
	RetainAudio              bool
	GrantedAudioSeconds      int64
	TranscriptionSessionID   string
	TranscriptRevision       int64
	SummaryStatus            MeetingSummaryStatus
	SummaryVersion           int64
	StartedAt                time.Time
	StoppedAt                *time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type MeetingAudioSpec struct {
	MIMEType      string
	SampleRate    int32
	Channels      int32
	ChunkDuration time.Duration
	MaxChunkBytes int32
}

type MeetingTranscriptionSession struct {
	ID                  string
	WebSocketURL        string
	Ticket              string
	ExpiresAt           time.Time
	Audio               MeetingAudioSpec
	GrantedAudioSeconds int64
}

type MeetingTranscriptSegment struct {
	ID           string
	MeetingID    string
	SequenceNo   int64
	StartOffset  time.Duration
	EndOffset    time.Duration
	SpeakerLabel string
	Content      string
	Language     MeetingLanguage
	Confidence   *float32
	CreatedAt    time.Time
}

type CreateMeetingCommand struct {
	UserID               string
	IdempotencyKey       string
	Language             MeetingLanguage
	RetainAudio          bool
	TranscriptionConsent bool
	Now                  time.Time
}

type MeetingCreatePersistenceInput struct {
	MeetingID          string
	UserID             string
	IdempotencyKey     string
	RequestFingerprint string
	Language           MeetingLanguage
	RetainAudio        bool
	Now                time.Time
}

type MeetingCreatePersistenceResult struct {
	Meeting     *Meeting
	Reservation *MeetingUsageReservation
	Existing    bool
}

type PrepareMeetingTranscriptionInput struct {
	MeetingID      string
	UserID         string
	ReservationID  string
	Language       MeetingLanguage
	GrantedSeconds int64
	IdempotencyKey string
}

type CancelMeetingTranscriptionInput struct {
	SessionID      string
	MeetingID      string
	Reason         MeetingTranscriptionCancelReason
	IdempotencyKey string
}

type MeetingTranscriptionCancelReason uint8

const (
	MeetingTranscriptionCancelReasonUnspecified MeetingTranscriptionCancelReason = iota
	MeetingTranscriptionCancelReasonUserCancelled
	MeetingTranscriptionCancelReasonPrepareCompensation
)

type PrepareMeetingSummaryInput struct {
	MeetingID                string
	UserID                   string
	Version                  int64
	SourceTranscriptRevision int64
	Language                 MeetingLanguage
	IdempotencyKey           string
}

type CreateMeetingResult struct {
	Meeting *Meeting
	Session *MeetingTranscriptionSession
}

type ListMeetingTranscriptInput struct {
	UserID        string
	MeetingID     string
	PageSize      int32
	AfterSequence int64
}

type ListMeetingTranscriptResult struct {
	Segments      []*MeetingTranscriptSegment
	NextPageToken string
}

type FinalizeMeetingTranscriptionCommand struct {
	MeetingID            string
	SessionID            string
	ReservationID        string
	TotalAcceptedSeconds int64
	ProviderUsageSeconds int64
	SettlementReason     MeetingUsageSettlementReason
	MeetingStatus        MeetingStatus
	TranscriptionStatus  MeetingTranscriptionStatus
	FinalizedAt          time.Time
}

type FinalizeMeetingTranscriptionResult struct {
	Meeting *Meeting
	Usage   *MeetingUsageRecord
}

var (
	ErrMeetingNotFound            = stderrors.New("meeting not found")
	ErrMeetingIdempotencyConflict = stderrors.New("meeting idempotency conflict")
	ErrMeetingStopKeyConflict     = stderrors.New("meeting stop idempotency key conflict")
	ErrMeetingStateConflict       = stderrors.New("meeting state conflict")
	ErrMeetingSessionMismatch     = stderrors.New("meeting transcription session mismatch")
	ErrTranscriptSegmentConflict  = stderrors.New("transcript segment conflict")
)

type MeetingRepo interface {
	FindByCreateIdempotency(context.Context, string, string) (*Meeting, error)
	CreateWithQuota(context.Context, MeetingCreatePersistenceInput, MeetingQuotaReserveInput) (*MeetingCreatePersistenceResult, error)
	MarkTranscriptionPrepared(context.Context, string, string, *MeetingTranscriptionSession, time.Time) (*Meeting, error)
	FailPreparationAndRelease(context.Context, string, string, time.Time) (*Meeting, error)
	Stop(context.Context, string, string, string, time.Time) (*Meeting, error)
	Get(context.Context, string, string) (*Meeting, error)
	AppendFinalTranscriptSegments(context.Context, string, string, string, []*MeetingTranscriptSegment) (int64, error)
	ValidateTranscriptionIdentity(context.Context, string, string, string) error
	FinalizeTranscription(context.Context, FinalizeMeetingTranscriptionCommand) (*FinalizeMeetingTranscriptionResult, error)
	ListTranscriptSegments(context.Context, ListMeetingTranscriptInput) ([]*MeetingTranscriptSegment, bool, error)
}

type VisionTranscriptionGateway interface {
	PrepareTranscription(context.Context, PrepareMeetingTranscriptionInput) (*MeetingTranscriptionSession, error)
	CancelTranscription(context.Context, CancelMeetingTranscriptionInput) error
}

type MeetingUsecase struct {
	repo          MeetingRepo
	summaryRepo   MeetingSummaryRepo
	quota         *MeetingQuotaUsecase
	vision        VisionTranscriptionGateway
	summaryVision VisionMeetingSummaryGateway
}

func NewMeetingUsecase(repo MeetingRepo, quota *MeetingQuotaUsecase, vision VisionTranscriptionGateway) (*MeetingUsecase, error) {
	if repo == nil {
		return nil, fmt.Errorf("meeting repository is required")
	}
	if quota == nil {
		return nil, fmt.Errorf("meeting quota usecase is required")
	}
	if vision == nil {
		return nil, fmt.Errorf("vision transcription gateway is required")
	}
	summaryRepo, _ := repo.(MeetingSummaryRepo)
	summaryVision, _ := vision.(VisionMeetingSummaryGateway)
	return &MeetingUsecase{repo: repo, summaryRepo: summaryRepo, quota: quota, vision: vision, summaryVision: summaryVision}, nil
}

func (uc *MeetingUsecase) Create(ctx context.Context, command CreateMeetingCommand) (*CreateMeetingResult, error) {
	if err := validateCreateMeetingCommand(command); err != nil {
		return nil, err
	}
	command.Now = normalizedMeetingTime(command.Now)
	fingerprint := meetingCreateFingerprint(command.Language, command.RetainAudio)
	existing, err := uc.repo.FindByCreateIdempotency(ctx, command.UserID, command.IdempotencyKey)
	if err == nil {
		return uc.prepareExisting(ctx, existing, fingerprint, command.Now)
	}
	if !stderrors.Is(err, ErrMeetingNotFound) {
		return nil, err
	}

	meetingID := uuid.NewString()
	quotaInput, err := uc.quota.AuthorizeReservation(ctx, command.UserID, meetingID, command.Now)
	if err != nil {
		return nil, err
	}
	persisted, err := uc.repo.CreateWithQuota(ctx, MeetingCreatePersistenceInput{
		MeetingID: meetingID, UserID: command.UserID, IdempotencyKey: command.IdempotencyKey,
		RequestFingerprint: fingerprint, Language: command.Language, RetainAudio: command.RetainAudio, Now: command.Now,
	}, quotaInput)
	if stderrors.Is(err, ErrMeetingIdempotencyConflict) {
		return nil, kratoserrors.Conflict("MEETING_IDEMPOTENCY_CONFLICT", "idempotency key was used with different meeting parameters")
	}
	if stderrors.Is(err, ErrMeetingQuotaExceeded) {
		return nil, kratoserrors.TooManyRequests("MEETING_QUOTA_EXCEEDED", "meeting audio quota is exhausted")
	}
	if err != nil {
		return nil, err
	}
	if persisted == nil || persisted.Meeting == nil || persisted.Reservation == nil {
		return nil, kratoserrors.InternalServer("MEETING_CREATE_RESULT_INVALID", "meeting creation returned invalid data")
	}
	if persisted.Existing {
		return uc.prepareExisting(ctx, persisted.Meeting, fingerprint, command.Now)
	}
	return uc.prepareNew(ctx, persisted.Meeting, command.Now)
}

func (uc *MeetingUsecase) prepareExisting(ctx context.Context, meeting *Meeting, fingerprint string, now time.Time) (*CreateMeetingResult, error) {
	if meeting == nil {
		return nil, kratoserrors.InternalServer("MEETING_DATA_INVALID", "meeting data is invalid")
	}
	if meeting.CreateRequestFingerprint != fingerprint {
		return nil, kratoserrors.Conflict("MEETING_IDEMPOTENCY_CONFLICT", "idempotency key was used with different meeting parameters")
	}
	if meeting.Status != MeetingStatusRecording || (meeting.TranscriptionStatus != MeetingTranscriptionStatusPending && meeting.TranscriptionStatus != MeetingTranscriptionStatusConnecting) {
		return nil, kratoserrors.Conflict("MEETING_STATE_CONFLICT", "meeting cannot be created again in its current state")
	}
	return uc.prepareNew(ctx, meeting, now)
}

func (uc *MeetingUsecase) prepareNew(ctx context.Context, meeting *Meeting, now time.Time) (*CreateMeetingResult, error) {
	session, err := uc.vision.PrepareTranscription(ctx, PrepareMeetingTranscriptionInput{
		MeetingID: meeting.ID, UserID: meeting.UserID, ReservationID: meeting.ReservationID,
		Language: meeting.Language, GrantedSeconds: meeting.GrantedAudioSeconds,
		IdempotencyKey: "prepare:" + meeting.ID,
	})
	if err != nil {
		compensateErr := uc.compensatePreparation(ctx, meeting, now)
		if compensateErr != nil {
			return nil, kratoserrors.ServiceUnavailable("VISION_PREPARATION_COMPENSATION_FAILED", "failed to prepare transcription session").WithCause(stderrors.Join(err, compensateErr))
		}
		return nil, err
	}
	if err := validateMeetingTranscriptionSession(session, meeting.GrantedAudioSeconds, now); err != nil {
		compensateErr := uc.compensatePreparation(ctx, meeting, now)
		return nil, kratoserrors.ServiceUnavailable("VISION_SESSION_INVALID", "vision returned an invalid transcription session").WithCause(stderrors.Join(err, compensateErr))
	}
	updated, err := uc.repo.MarkTranscriptionPrepared(ctx, meeting.UserID, meeting.ID, session, now)
	if err == nil {
		return &CreateMeetingResult{Meeting: updated, Session: session}, nil
	}
	cancelErr := uc.vision.CancelTranscription(ctx, CancelMeetingTranscriptionInput{
		SessionID: session.ID, MeetingID: meeting.ID, Reason: MeetingTranscriptionCancelReasonPrepareCompensation,
		IdempotencyKey: "prepare-compensation:" + meeting.ID,
	})
	compensateErr := uc.compensatePreparation(ctx, meeting, now)
	return nil, kratoserrors.InternalServer("MEETING_PREPARATION_COMMIT_FAILED", "failed to persist transcription session").WithCause(stderrors.Join(err, cancelErr, compensateErr))
}

func (uc *MeetingUsecase) compensatePreparation(ctx context.Context, meeting *Meeting, now time.Time) error {
	_, err := uc.repo.FailPreparationAndRelease(ctx, meeting.UserID, meeting.ID, now)
	return err
}

func (uc *MeetingUsecase) Stop(ctx context.Context, userID, meetingID, idempotencyKey string, now time.Time) (*Meeting, error) {
	if err := validateMeetingIdentity(userID, meetingID); err != nil {
		return nil, err
	}
	if err := validateMeetingIdempotencyKey(idempotencyKey); err != nil {
		return nil, err
	}
	meeting, err := uc.repo.Stop(ctx, userID, meetingID, idempotencyKey, normalizedMeetingTime(now))
	if err != nil {
		return nil, mapMeetingRepoError(err)
	}
	if meeting == nil {
		return nil, kratoserrors.InternalServer("MEETING_STOP_RESULT_INVALID", "meeting stop returned invalid data")
	}
	// The browser normally finishes WSS first. This cancellation is the
	// server-side fallback for startup failures, lost sockets, and clients that
	// disappear before sending the finish handshake. Vision persists a terminal
	// outbox event before returning, so Core can release the quota reservation.
	if meeting.TranscriptionSessionID != "" && !meeting.TranscriptionStatus.IsTerminal() {
		if err := uc.vision.CancelTranscription(ctx, CancelMeetingTranscriptionInput{
			SessionID: meeting.TranscriptionSessionID, MeetingID: meeting.ID,
			Reason: MeetingTranscriptionCancelReasonUserCancelled, IdempotencyKey: "user-stop:" + meeting.ID,
		}); err != nil {
			return nil, kratoserrors.ServiceUnavailable("VISION_CANCELLATION_PENDING", "meeting stopped but transcription cancellation is pending").WithCause(err)
		}
	}
	return meeting, nil
}

func (uc *MeetingUsecase) Get(ctx context.Context, userID, meetingID string) (*Meeting, error) {
	if err := validateMeetingIdentity(userID, meetingID); err != nil {
		return nil, err
	}
	meeting, err := uc.repo.Get(ctx, userID, meetingID)
	return meeting, mapMeetingRepoError(err)
}

func (uc *MeetingUsecase) GetQuota(ctx context.Context, userID string, now time.Time) (*MeetingQuotaSnapshot, error) {
	return uc.quota.GetQuota(ctx, userID, now)
}

func (uc *MeetingUsecase) AppendFinalTranscriptSegments(ctx context.Context, meetingID, sessionID, batchID string, segments []*MeetingTranscriptSegment) (int64, error) {
	for name, value := range map[string]string{"meeting ID": meetingID, "session ID": sessionID, "batch ID": batchID} {
		if _, err := uuid.Parse(value); err != nil {
			return 0, kratoserrors.BadRequest("TRANSCRIPT_IDENTIFIER_INVALID", name+" must be a UUID")
		}
	}
	if len(segments) == 0 || len(segments) > maxTranscriptBatchSize {
		return 0, kratoserrors.BadRequest("TRANSCRIPT_BATCH_SIZE_INVALID", "transcript batch must contain between 1 and 100 segments")
	}
	previousSequence := int64(0)
	for _, segment := range segments {
		if err := validateFinalTranscriptSegment(segment, meetingID, previousSequence); err != nil {
			return 0, err
		}
		previousSequence = segment.SequenceNo
	}
	last, err := uc.repo.AppendFinalTranscriptSegments(ctx, meetingID, sessionID, batchID, segments)
	if stderrors.Is(err, ErrMeetingNotFound) {
		return 0, kratoserrors.NotFound("MEETING_NOT_FOUND", "meeting not found")
	}
	if stderrors.Is(err, ErrMeetingSessionMismatch) {
		return 0, kratoserrors.Conflict("TRANSCRIPTION_SESSION_MISMATCH", "transcription session does not belong to meeting")
	}
	if stderrors.Is(err, ErrTranscriptSegmentConflict) {
		return 0, kratoserrors.Conflict("TRANSCRIPT_SEGMENT_CONFLICT", "transcript segment conflicts with existing data")
	}
	return last, err
}

func (uc *MeetingUsecase) ReportTranscriptionUsage(ctx context.Context, meetingID, sessionID, reservationID string, totalSeconds int64, observedAt time.Time) (*MeetingUsageReservation, error) {
	if err := validateTranscriptionCommand(meetingID, sessionID, reservationID, totalSeconds, 0); err != nil {
		return nil, err
	}
	if err := uc.repo.ValidateTranscriptionIdentity(ctx, meetingID, sessionID, reservationID); err != nil {
		return nil, mapMeetingRepoError(err)
	}
	reservation, err := uc.quota.ReportUsage(ctx, reservationID, meetingID, totalSeconds, observedAt)
	if err == nil && reservation == nil {
		return nil, kratoserrors.InternalServer("TRANSCRIPTION_USAGE_RESULT_INVALID", "transcription usage result is invalid")
	}
	return reservation, err
}

func (uc *MeetingUsecase) CompleteTranscription(ctx context.Context, command FinalizeMeetingTranscriptionCommand) (*FinalizeMeetingTranscriptionResult, error) {
	command.SettlementReason = MeetingUsageSettlementReasonCompleted
	command.MeetingStatus = MeetingStatusProcessing
	command.TranscriptionStatus = MeetingTranscriptionStatusSucceeded
	result, err := uc.finalizeTranscription(ctx, command)
	if err != nil {
		return nil, err
	}
	if uc.summaryRepo == nil || uc.summaryVision == nil {
		return nil, kratoserrors.ServiceUnavailable("SUMMARY_SERVICE_UNAVAILABLE", "meeting summary service is unavailable")
	}
	task, err := uc.summaryRepo.EnsureSummaryTask(ctx, result.Meeting.ID, result.Meeting.UserID, "automatic:"+strconv.FormatInt(result.Meeting.TranscriptRevision, 10), command.FinalizedAt)
	if err != nil {
		return nil, mapMeetingRepoError(err)
	}
	if err := uc.submitSummaryTask(ctx, task); err != nil {
		return nil, err
	}
	return result, nil
}

func (uc *MeetingUsecase) FailTranscription(ctx context.Context, command FinalizeMeetingTranscriptionCommand) (*FinalizeMeetingTranscriptionResult, error) {
	if command.SettlementReason != MeetingUsageSettlementReasonFailed &&
		command.SettlementReason != MeetingUsageSettlementReasonCancelled &&
		command.SettlementReason != MeetingUsageSettlementReasonQuotaExhausted &&
		command.SettlementReason != MeetingUsageSettlementReasonExpired {
		return nil, kratoserrors.BadRequest("SETTLEMENT_REASON_INVALID", "transcription failure settlement reason is invalid")
	}
	switch command.SettlementReason {
	case MeetingUsageSettlementReasonCancelled:
		command.MeetingStatus = MeetingStatusCancelled
		command.TranscriptionStatus = MeetingTranscriptionStatusCancelled
	case MeetingUsageSettlementReasonExpired:
		command.MeetingStatus = MeetingStatusFailed
		command.TranscriptionStatus = MeetingTranscriptionStatusExpired
	default:
		command.MeetingStatus = MeetingStatusFailed
		command.TranscriptionStatus = MeetingTranscriptionStatusFailed
	}
	return uc.finalizeTranscription(ctx, command)
}

func (uc *MeetingUsecase) finalizeTranscription(ctx context.Context, command FinalizeMeetingTranscriptionCommand) (*FinalizeMeetingTranscriptionResult, error) {
	if err := validateTranscriptionCommand(command.MeetingID, command.SessionID, command.ReservationID, command.TotalAcceptedSeconds, command.ProviderUsageSeconds); err != nil {
		return nil, err
	}
	if command.FinalizedAt.IsZero() {
		command.FinalizedAt = time.Now().UTC()
	} else {
		command.FinalizedAt = command.FinalizedAt.UTC()
	}
	result, err := uc.repo.FinalizeTranscription(ctx, command)
	if err != nil {
		return nil, mapMeetingRepoError(err)
	}
	if result == nil || result.Meeting == nil || result.Usage == nil {
		return nil, kratoserrors.InternalServer("TRANSCRIPTION_FINALIZE_RESULT_INVALID", "transcription finalization result is invalid")
	}
	return result, nil
}

func validateTranscriptionCommand(meetingID, sessionID, reservationID string, totalSeconds, providerSeconds int64) error {
	for name, value := range map[string]string{"meeting ID": meetingID, "session ID": sessionID, "reservation ID": reservationID} {
		if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
			return kratoserrors.BadRequest("TRANSCRIPTION_IDENTIFIER_INVALID", name+" must be a UUID")
		}
	}
	if totalSeconds < 0 || providerSeconds < 0 {
		return kratoserrors.BadRequest("TRANSCRIPTION_USAGE_INVALID", "transcription usage must not be negative")
	}
	return nil
}

func (uc *MeetingUsecase) ListTranscriptSegments(ctx context.Context, userID, meetingID string, pageSize int32, pageToken string) (*ListMeetingTranscriptResult, error) {
	if err := validateMeetingIdentity(userID, meetingID); err != nil {
		return nil, err
	}
	if pageSize == 0 {
		pageSize = defaultTranscriptPageSize
	}
	if pageSize < 0 || pageSize > maxTranscriptPageSize {
		return nil, kratoserrors.BadRequest("PAGE_SIZE_INVALID", "page size must be between 1 and 200")
	}
	after, err := decodeTranscriptPageToken(pageToken)
	if err != nil {
		return nil, kratoserrors.BadRequest("PAGE_TOKEN_INVALID", "page token is invalid")
	}
	segments, hasMore, err := uc.repo.ListTranscriptSegments(ctx, ListMeetingTranscriptInput{
		UserID: userID, MeetingID: meetingID, PageSize: pageSize, AfterSequence: after,
	})
	if err != nil {
		return nil, mapMeetingRepoError(err)
	}
	result := &ListMeetingTranscriptResult{Segments: segments}
	if hasMore && len(segments) > 0 {
		result.NextPageToken = encodeTranscriptPageToken(segments[len(segments)-1].SequenceNo)
	}
	return result, nil
}

func validateCreateMeetingCommand(command CreateMeetingCommand) error {
	if _, err := uuid.Parse(command.UserID); err != nil {
		return kratoserrors.BadRequest("USER_ID_INVALID", "user ID must be a UUID")
	}
	if err := validateMeetingIdempotencyKey(command.IdempotencyKey); err != nil {
		return err
	}
	if command.Language != MeetingLanguageAuto && command.Language != MeetingLanguageZhCN && command.Language != MeetingLanguageEnUS {
		return kratoserrors.BadRequest("MEETING_LANGUAGE_INVALID", "meeting language is invalid")
	}
	if !command.TranscriptionConsent {
		return kratoserrors.BadRequest("TRANSCRIPTION_CONSENT_REQUIRED", "transcription consent is required")
	}
	return nil
}

func validateMeetingIdempotencyKey(value string) error {
	if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
		return kratoserrors.BadRequest("IDEMPOTENCY_KEY_INVALID", "Idempotency-Key must be a UUID")
	}
	return nil
}

func validateMeetingIdentity(userID, meetingID string) error {
	if _, err := uuid.Parse(userID); err != nil {
		return kratoserrors.BadRequest("USER_ID_INVALID", "user ID must be a UUID")
	}
	if _, err := uuid.Parse(meetingID); err != nil {
		return kratoserrors.BadRequest("MEETING_ID_INVALID", "meeting ID must be a UUID")
	}
	return nil
}

func validateMeetingTranscriptionSession(session *MeetingTranscriptionSession, grantedSeconds int64, now time.Time) error {
	if session == nil {
		return fmt.Errorf("transcription session is nil")
	}
	if _, err := uuid.Parse(session.ID); err != nil {
		return fmt.Errorf("transcription session ID is invalid")
	}
	parsedURL, err := url.Parse(session.WebSocketURL)
	secureWebSocket := err == nil && parsedURL.Scheme == "wss"
	localDevelopmentWebSocket := err == nil && parsedURL.Scheme == "ws" && isLoopbackMeetingWebSocketHost(parsedURL.Hostname())
	if (!secureWebSocket && !localDevelopmentWebSocket) || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return fmt.Errorf("transcription WebSocket URL is invalid")
	}
	if strings.TrimSpace(session.Ticket) == "" || len(session.Ticket) > 512 {
		return fmt.Errorf("transcription ticket is invalid")
	}
	if !session.ExpiresAt.After(now) {
		return fmt.Errorf("transcription session is already expired")
	}
	if session.GrantedAudioSeconds != grantedSeconds || grantedSeconds <= 0 {
		return fmt.Errorf("transcription grant does not match core grant")
	}
	if session.Audio.MIMEType != "audio/pcm;rate=16000" || session.Audio.SampleRate != 16_000 || session.Audio.Channels != 1 {
		return fmt.Errorf("transcription audio format is invalid")
	}
	if session.Audio.ChunkDuration < 100*time.Millisecond || session.Audio.ChunkDuration > 200*time.Millisecond || session.Audio.MaxChunkBytes <= 0 {
		return fmt.Errorf("transcription audio chunk limits are invalid")
	}
	return nil
}

func isLoopbackMeetingWebSocketHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateFinalTranscriptSegment(segment *MeetingTranscriptSegment, meetingID string, previousSequence int64) error {
	if segment == nil {
		return kratoserrors.BadRequest("TRANSCRIPT_SEGMENT_INVALID", "transcript segment is required")
	}
	if _, err := uuid.Parse(segment.ID); err != nil {
		return kratoserrors.BadRequest("SEGMENT_ID_INVALID", "segment ID must be a UUID")
	}
	if segment.MeetingID != "" && segment.MeetingID != meetingID {
		return kratoserrors.BadRequest("SEGMENT_MEETING_MISMATCH", "segment meeting ID does not match")
	}
	segment.MeetingID = meetingID
	if segment.SequenceNo <= previousSequence || segment.SequenceNo <= 0 {
		return kratoserrors.BadRequest("SEGMENT_SEQUENCE_INVALID", "segment sequence must be positive and strictly increasing")
	}
	if segment.StartOffset < 0 || segment.EndOffset < segment.StartOffset {
		return kratoserrors.BadRequest("SEGMENT_OFFSET_INVALID", "segment offsets are invalid")
	}
	if segment.StartOffset%time.Millisecond != 0 || segment.EndOffset%time.Millisecond != 0 {
		return kratoserrors.BadRequest("SEGMENT_OFFSET_PRECISION_INVALID", "segment offsets must use millisecond precision")
	}
	segment.Content = strings.TrimSpace(segment.Content)
	if segment.Content == "" || utf8.RuneCountInString(segment.Content) > maxTranscriptContentRunes {
		return kratoserrors.BadRequest("SEGMENT_CONTENT_INVALID", "segment content is empty or too long")
	}
	if utf8.RuneCountInString(segment.SpeakerLabel) > maxSpeakerLabelRunes {
		return kratoserrors.BadRequest("SPEAKER_LABEL_TOO_LONG", "speaker label is too long")
	}
	if segment.Language != MeetingLanguageAuto && segment.Language != MeetingLanguageZhCN && segment.Language != MeetingLanguageEnUS {
		return kratoserrors.BadRequest("SEGMENT_LANGUAGE_INVALID", "final segment language is invalid")
	}
	if segment.Confidence != nil && (*segment.Confidence < 0 || *segment.Confidence > 1 || float32IsNaN(*segment.Confidence)) {
		return kratoserrors.BadRequest("SEGMENT_CONFIDENCE_INVALID", "segment confidence must be between 0 and 1")
	}
	segment.CreatedAt = normalizedMeetingTime(segment.CreatedAt).Truncate(time.Microsecond)
	return nil
}

func meetingCreateFingerprint(language MeetingLanguage, retainAudio bool) string {
	raw := language.String() + "|" + strconv.FormatBool(retainAudio)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func encodeTranscriptPageToken(sequence int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(sequence, 10)))
}

func decodeTranscriptPageToken(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	if len(token) > 64 {
		return 0, fmt.Errorf("page token is too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, err
	}
	sequence, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || sequence < 0 {
		return 0, fmt.Errorf("page token sequence is invalid")
	}
	return sequence, nil
}

func mapMeetingRepoError(err error) error {
	if stderrors.Is(err, ErrMeetingNotFound) {
		return kratoserrors.NotFound("MEETING_NOT_FOUND", "meeting not found")
	}
	if stderrors.Is(err, ErrMeetingStateConflict) {
		return kratoserrors.Conflict("MEETING_STATE_CONFLICT", "meeting state does not allow this operation")
	}
	if stderrors.Is(err, ErrMeetingStopKeyConflict) {
		return kratoserrors.Conflict("MEETING_STOP_IDEMPOTENCY_CONFLICT", "stop idempotency key belongs to another meeting")
	}
	return err
}

func normalizedMeetingTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func float32IsNaN(value float32) bool {
	return math.IsNaN(float64(value))
}
