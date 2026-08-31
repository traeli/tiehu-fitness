package biz

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

const (
	maxSummaryTopicRunes    = 300
	maxSummaryAbstractRunes = 20_000
	maxSummaryListItems     = 100
	maxSummaryItemRunes     = 2_000
	maxSummaryAssigneeRunes = 200
	maxSummaryDueTextRunes  = 500
	maxSummarySnapshotItems = 10_000
)

type MeetingSummaryStatus uint8

const (
	MeetingSummaryStatusUnspecified MeetingSummaryStatus = iota
	MeetingSummaryStatusNotStarted
	MeetingSummaryStatusPending
	MeetingSummaryStatusProcessing
	MeetingSummaryStatusSucceeded
	MeetingSummaryStatusFailed
)

func (s MeetingSummaryStatus) String() string {
	switch s {
	case MeetingSummaryStatusNotStarted:
		return "not_started"
	case MeetingSummaryStatusPending:
		return "pending"
	case MeetingSummaryStatusProcessing:
		return "processing"
	case MeetingSummaryStatusSucceeded:
		return "succeeded"
	case MeetingSummaryStatusFailed:
		return "failed"
	default:
		return ""
	}
}

func ParseMeetingSummaryStatus(raw string) (MeetingSummaryStatus, error) {
	for _, status := range []MeetingSummaryStatus{
		MeetingSummaryStatusNotStarted,
		MeetingSummaryStatusPending,
		MeetingSummaryStatusProcessing,
		MeetingSummaryStatusSucceeded,
		MeetingSummaryStatusFailed,
	} {
		if raw == status.String() {
			return status, nil
		}
	}
	return MeetingSummaryStatusUnspecified, fmt.Errorf("unknown meeting summary status %q", raw)
}

func (s MeetingSummaryStatus) CanTransitionTo(next MeetingSummaryStatus) bool {
	switch s {
	case MeetingSummaryStatusNotStarted, MeetingSummaryStatusFailed, MeetingSummaryStatusSucceeded:
		return next == MeetingSummaryStatusPending
	case MeetingSummaryStatusPending:
		return next == MeetingSummaryStatusProcessing || next == MeetingSummaryStatusSucceeded || next == MeetingSummaryStatusFailed
	case MeetingSummaryStatusProcessing:
		return next == MeetingSummaryStatusSucceeded || next == MeetingSummaryStatusFailed
	default:
		return false
	}
}

type MeetingActionItemStatus uint8

const (
	MeetingActionItemStatusUnspecified MeetingActionItemStatus = iota
	MeetingActionItemStatusPending
)

func (s MeetingActionItemStatus) String() string {
	if s == MeetingActionItemStatusPending {
		return "pending"
	}
	return ""
}

func ParseMeetingActionItemStatus(raw string) (MeetingActionItemStatus, error) {
	if raw == MeetingActionItemStatusPending.String() {
		return MeetingActionItemStatusPending, nil
	}
	return MeetingActionItemStatusUnspecified, fmt.Errorf("unknown meeting action item status %q", raw)
}

type MeetingSummaryFailureReason uint8

const (
	MeetingSummaryFailureReasonUnspecified MeetingSummaryFailureReason = iota
	MeetingSummaryFailureReasonProviderUnavailable
	MeetingSummaryFailureReasonProviderRejected
	MeetingSummaryFailureReasonOutputInvalid
	MeetingSummaryFailureReasonTranscriptInvalid
	MeetingSummaryFailureReasonTimeout
	MeetingSummaryFailureReasonInternal
)

func (r MeetingSummaryFailureReason) String() string {
	switch r {
	case MeetingSummaryFailureReasonProviderUnavailable:
		return "LLM_PROVIDER_UNAVAILABLE"
	case MeetingSummaryFailureReasonProviderRejected:
		return "LLM_PROVIDER_REJECTED"
	case MeetingSummaryFailureReasonOutputInvalid:
		return "SUMMARY_OUTPUT_INVALID"
	case MeetingSummaryFailureReasonTranscriptInvalid:
		return "SUMMARY_TRANSCRIPT_INVALID"
	case MeetingSummaryFailureReasonTimeout:
		return "SUMMARY_TIMEOUT"
	case MeetingSummaryFailureReasonInternal:
		return "SUMMARY_INTERNAL"
	default:
		return ""
	}
}

type MeetingActionItem struct {
	Assignee string
	Task     string
	DueText  string
	Status   MeetingActionItemStatus
}

type MeetingSummary struct {
	ID                       string
	MeetingID                string
	Version                  int64
	SourceTranscriptRevision int64
	Status                   MeetingSummaryStatus
	Topic                    string
	Abstract                 string
	KeyDiscussions           []string
	Decisions                []string
	ActionItems              []MeetingActionItem
	Risks                    []string
	Provider                 string
	ModelName                string
	PromptVersion            string
	InputTokens              int64
	OutputTokens             int64
	FailureReason            string
	GeneratedAt              *time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

func (s *MeetingSummary) ValidateCompleted() error {
	if s == nil {
		return fmt.Errorf("meeting summary is required")
	}
	if _, err := uuid.Parse(s.MeetingID); err != nil || s.Version <= 0 || s.SourceTranscriptRevision <= 0 {
		return fmt.Errorf("meeting summary identity is invalid")
	}
	if s.Status != MeetingSummaryStatusSucceeded || s.GeneratedAt == nil || s.GeneratedAt.IsZero() {
		return fmt.Errorf("completed meeting summary state is invalid")
	}
	if err := validateSummaryText("topic", s.Topic, 1, maxSummaryTopicRunes); err != nil {
		return err
	}
	if err := validateSummaryText("abstract", s.Abstract, 1, maxSummaryAbstractRunes); err != nil {
		return err
	}
	if strings.TrimSpace(s.ModelName) == "" || utf8.RuneCountInString(s.ModelName) > 128 ||
		strings.TrimSpace(s.PromptVersion) == "" || utf8.RuneCountInString(s.PromptVersion) > 64 ||
		strings.TrimSpace(s.Provider) == "" || utf8.RuneCountInString(s.Provider) > 32 {
		return fmt.Errorf("meeting summary provider metadata is invalid")
	}
	if s.InputTokens < 0 || s.OutputTokens < 0 {
		return fmt.Errorf("meeting summary token usage is invalid")
	}
	for name, values := range map[string][]string{
		"key discussions": s.KeyDiscussions,
		"decisions":       s.Decisions,
		"risks":           s.Risks,
	} {
		if len(values) > maxSummaryListItems {
			return fmt.Errorf("meeting summary %s exceeds item limit", name)
		}
		for _, value := range values {
			if err := validateSummaryText(name, value, 1, maxSummaryItemRunes); err != nil {
				return err
			}
		}
	}
	if len(s.ActionItems) > maxSummaryListItems {
		return fmt.Errorf("meeting summary action items exceed item limit")
	}
	for _, item := range s.ActionItems {
		if err := validateSummaryText("action item task", item.Task, 1, maxSummaryItemRunes); err != nil {
			return err
		}
		if err := validateSummaryText("action item assignee", item.Assignee, 0, maxSummaryAssigneeRunes); err != nil {
			return err
		}
		if err := validateSummaryText("action item due text", item.DueText, 0, maxSummaryDueTextRunes); err != nil {
			return err
		}
		if item.Status != MeetingActionItemStatusPending {
			return fmt.Errorf("meeting summary action item status is invalid")
		}
	}
	return nil
}

type MeetingSummaryTask struct {
	MeetingID                string
	UserID                   string
	Version                  int64
	SourceTranscriptRevision int64
	Language                 MeetingLanguage
	IdempotencyKey           string
}

type MeetingSummaryView struct {
	Status        MeetingSummaryStatus
	Summary       *MeetingSummary
	FailureReason string
}

type MeetingTranscriptSnapshot struct {
	MeetingID          string
	Language           MeetingLanguage
	TranscriptRevision int64
	Segments           []*MeetingTranscriptSegment
}

type CompleteMeetingSummaryCommand struct {
	MeetingID                string
	Version                  int64
	SourceTranscriptRevision int64
	Summary                  *MeetingSummary
}

type FailMeetingSummaryCommand struct {
	MeetingID                string
	Version                  int64
	SourceTranscriptRevision int64
	Reason                   MeetingSummaryFailureReason
	FailedAt                 time.Time
}

type MeetingSummaryRepo interface {
	EnsureSummaryTask(context.Context, string, string, string, time.Time) (*MeetingSummaryTask, error)
	MarkSummaryTaskProcessing(context.Context, string, int64, time.Time) error
	GetSummary(context.Context, string, string) (*MeetingSummaryView, error)
	GetTranscriptSnapshot(context.Context, string, int64, int) (*MeetingTranscriptSnapshot, error)
	CompleteSummary(context.Context, CompleteMeetingSummaryCommand) (*MeetingSummary, error)
	FailSummary(context.Context, FailMeetingSummaryCommand) error
}

type VisionMeetingSummaryGateway interface {
	PrepareMeetingSummary(context.Context, PrepareMeetingSummaryInput) error
}

func (uc *MeetingUsecase) GetSummary(ctx context.Context, userID, meetingID string) (*MeetingSummaryView, error) {
	if err := validateMeetingIdentity(userID, meetingID); err != nil {
		return nil, err
	}
	if uc.summaryRepo == nil {
		return nil, kratoserrors.ServiceUnavailable("SUMMARY_SERVICE_UNAVAILABLE", "meeting summary service is unavailable")
	}
	view, err := uc.summaryRepo.GetSummary(ctx, userID, meetingID)
	return view, mapMeetingRepoError(err)
}

func (uc *MeetingUsecase) RegenerateSummary(ctx context.Context, userID, meetingID, idempotencyKey string, now time.Time) (*MeetingSummaryTask, error) {
	if err := validateMeetingIdentity(userID, meetingID); err != nil {
		return nil, err
	}
	if err := validateMeetingIdempotencyKey(idempotencyKey); err != nil {
		return nil, err
	}
	if uc.summaryRepo == nil || uc.summaryVision == nil {
		return nil, kratoserrors.ServiceUnavailable("SUMMARY_SERVICE_UNAVAILABLE", "meeting summary service is unavailable")
	}
	task, err := uc.summaryRepo.EnsureSummaryTask(ctx, meetingID, userID, idempotencyKey, normalizedMeetingTime(now))
	if err != nil {
		return nil, mapMeetingRepoError(err)
	}
	if err := uc.submitSummaryTask(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (uc *MeetingUsecase) submitSummaryTask(ctx context.Context, task *MeetingSummaryTask) error {
	if task == nil {
		return kratoserrors.InternalServer("SUMMARY_TASK_INVALID", "meeting summary task is invalid")
	}
	if uc.summaryVision == nil || uc.summaryRepo == nil {
		return kratoserrors.ServiceUnavailable("SUMMARY_SERVICE_UNAVAILABLE", "meeting summary service is unavailable")
	}
	if err := uc.summaryVision.PrepareMeetingSummary(ctx, PrepareMeetingSummaryInput{
		MeetingID: task.MeetingID, UserID: task.UserID, Version: task.Version,
		SourceTranscriptRevision: task.SourceTranscriptRevision, Language: task.Language,
		IdempotencyKey: task.IdempotencyKey,
	}); err != nil {
		return err
	}
	if err := uc.summaryRepo.MarkSummaryTaskProcessing(ctx, task.MeetingID, task.Version, time.Now().UTC()); err != nil {
		return mapMeetingRepoError(err)
	}
	return nil
}

func (uc *MeetingUsecase) GetTranscriptSnapshot(ctx context.Context, meetingID string, revision int64) (*MeetingTranscriptSnapshot, error) {
	if _, err := uuid.Parse(meetingID); err != nil || revision <= 0 {
		return nil, kratoserrors.BadRequest("SUMMARY_TRANSCRIPT_IDENTITY_INVALID", "meeting and transcript revision are invalid")
	}
	if uc.summaryRepo == nil {
		return nil, kratoserrors.ServiceUnavailable("SUMMARY_SERVICE_UNAVAILABLE", "meeting summary service is unavailable")
	}
	snapshot, err := uc.summaryRepo.GetTranscriptSnapshot(ctx, meetingID, revision, maxSummarySnapshotItems)
	if err != nil {
		return nil, mapMeetingRepoError(err)
	}
	if snapshot == nil || len(snapshot.Segments) == 0 {
		return nil, kratoserrors.Conflict("SUMMARY_TRANSCRIPT_EMPTY", "meeting transcript is empty")
	}
	return snapshot, nil
}

func (uc *MeetingUsecase) CompleteSummary(ctx context.Context, command CompleteMeetingSummaryCommand) (*MeetingSummary, error) {
	if command.Summary == nil || command.MeetingID != command.Summary.MeetingID || command.Version != command.Summary.Version ||
		command.SourceTranscriptRevision != command.Summary.SourceTranscriptRevision {
		return nil, kratoserrors.BadRequest("SUMMARY_RESULT_INVALID", "meeting summary result identity is invalid")
	}
	if err := command.Summary.ValidateCompleted(); err != nil {
		return nil, kratoserrors.BadRequest("SUMMARY_RESULT_INVALID", "meeting summary result is invalid").WithCause(err)
	}
	if uc.summaryRepo == nil {
		return nil, kratoserrors.ServiceUnavailable("SUMMARY_SERVICE_UNAVAILABLE", "meeting summary service is unavailable")
	}
	result, err := uc.summaryRepo.CompleteSummary(ctx, command)
	return result, mapMeetingRepoError(err)
}

func (uc *MeetingUsecase) FailSummary(ctx context.Context, command FailMeetingSummaryCommand) error {
	if _, err := uuid.Parse(command.MeetingID); err != nil || command.Version <= 0 || command.SourceTranscriptRevision <= 0 ||
		command.Reason == MeetingSummaryFailureReasonUnspecified {
		return kratoserrors.BadRequest("SUMMARY_FAILURE_INVALID", "meeting summary failure command is invalid")
	}
	if command.FailedAt.IsZero() {
		command.FailedAt = time.Now().UTC()
	}
	if uc.summaryRepo == nil {
		return kratoserrors.ServiceUnavailable("SUMMARY_SERVICE_UNAVAILABLE", "meeting summary service is unavailable")
	}
	return mapMeetingRepoError(uc.summaryRepo.FailSummary(ctx, command))
}

func validateSummaryText(name, value string, minimum, maximum int) error {
	trimmed := strings.TrimSpace(value)
	count := utf8.RuneCountInString(trimmed)
	if count < minimum || count > maximum {
		return fmt.Errorf("meeting summary %s length is invalid", name)
	}
	return nil
}
