package biz

import (
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxSummaryTopicRunes          = 300
	maxSummaryAbstractRunes       = 20_000
	maxSummaryItems               = 100
	maxSummaryItemRunes           = 2_000
	maxSummaryAssigneeRunes       = 200
	maxSummaryDueTextRunes        = 500
	maxSummaryTranscriptSegments  = 10_000
	maxSummaryTranscriptTotalRune = 2_000_000
)

type MeetingSummaryJobStatus string

const (
	MeetingSummaryJobStatusPending                MeetingSummaryJobStatus = "pending"
	MeetingSummaryJobStatusProcessing             MeetingSummaryJobStatus = "processing"
	MeetingSummaryJobStatusDeliveryPending        MeetingSummaryJobStatus = "delivery_pending"
	MeetingSummaryJobStatusFailureDeliveryPending MeetingSummaryJobStatus = "failure_delivery_pending"
	MeetingSummaryJobStatusSucceeded              MeetingSummaryJobStatus = "succeeded"
	MeetingSummaryJobStatusFailed                 MeetingSummaryJobStatus = "failed"
)

func ParseMeetingSummaryJobStatus(raw string) (MeetingSummaryJobStatus, error) {
	switch MeetingSummaryJobStatus(raw) {
	case MeetingSummaryJobStatusPending, MeetingSummaryJobStatusProcessing,
		MeetingSummaryJobStatusDeliveryPending, MeetingSummaryJobStatusFailureDeliveryPending,
		MeetingSummaryJobStatusSucceeded, MeetingSummaryJobStatusFailed:
		return MeetingSummaryJobStatus(raw), nil
	default:
		return "", fmt.Errorf("unknown meeting summary job status %q", raw)
	}
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

func ParseMeetingSummaryFailureReason(raw string) (MeetingSummaryFailureReason, error) {
	for _, reason := range []MeetingSummaryFailureReason{
		MeetingSummaryFailureReasonProviderUnavailable, MeetingSummaryFailureReasonProviderRejected,
		MeetingSummaryFailureReasonOutputInvalid, MeetingSummaryFailureReasonTranscriptInvalid,
		MeetingSummaryFailureReasonTimeout, MeetingSummaryFailureReasonInternal,
	} {
		if raw == reason.String() {
			return reason, nil
		}
	}
	return MeetingSummaryFailureReasonUnspecified, fmt.Errorf("unknown meeting summary failure reason %q", raw)
}

type MeetingActionItem struct {
	Assignee string `json:"assignee,omitempty"`
	Task     string `json:"task"`
	DueText  string `json:"due_text,omitempty"`
	Status   string `json:"status"`
}

type MeetingSummary struct {
	Topic          string                     `json:"topic"`
	Abstract       string                     `json:"abstract"`
	KeyDiscussions []string                   `json:"key_discussions"`
	Decisions      []string                   `json:"decisions"`
	ActionItems    []MeetingActionItem        `json:"action_items"`
	Risks          []string                   `json:"risks"`
	Provider       MeetingSummaryProviderName `json:"-"`
	ModelName      string                     `json:"-"`
	PromptVersion  string                     `json:"-"`
	InputTokens    int64                      `json:"-"`
	OutputTokens   int64                      `json:"-"`
	GeneratedAt    time.Time                  `json:"-"`
}

func (s *MeetingSummary) Validate() error {
	if s == nil {
		return fmt.Errorf("meeting summary is required")
	}
	if err := validateSummaryText("topic", s.Topic, 1, maxSummaryTopicRunes); err != nil {
		return err
	}
	if err := validateSummaryText("abstract", s.Abstract, 1, maxSummaryAbstractRunes); err != nil {
		return err
	}
	if _, err := ParseMeetingSummaryProviderName(string(s.Provider)); err != nil {
		return fmt.Errorf("meeting summary provider is invalid: %w", err)
	}
	if utf8.RuneCountInString(string(s.Provider)) > 32 ||
		strings.TrimSpace(s.ModelName) == "" || utf8.RuneCountInString(s.ModelName) > 128 ||
		strings.TrimSpace(s.PromptVersion) == "" || utf8.RuneCountInString(s.PromptVersion) > 64 ||
		s.InputTokens < 0 || s.OutputTokens < 0 || s.GeneratedAt.IsZero() {
		return fmt.Errorf("meeting summary generation metadata is invalid")
	}
	for name, values := range map[string][]string{
		"key discussions": s.KeyDiscussions, "decisions": s.Decisions, "risks": s.Risks,
	} {
		if len(values) > maxSummaryItems {
			return fmt.Errorf("meeting summary %s exceeds item limit", name)
		}
		for _, value := range values {
			if err := validateSummaryText(name, value, 1, maxSummaryItemRunes); err != nil {
				return err
			}
		}
	}
	if len(s.ActionItems) > maxSummaryItems {
		return fmt.Errorf("meeting summary action items exceed item limit")
	}
	for _, item := range s.ActionItems {
		if err := validateSummaryText("action task", item.Task, 1, maxSummaryItemRunes); err != nil {
			return err
		}
		if err := validateSummaryText("action assignee", item.Assignee, 0, maxSummaryAssigneeRunes); err != nil {
			return err
		}
		if err := validateSummaryText("action due text", item.DueText, 0, maxSummaryDueTextRunes); err != nil {
			return err
		}
		if item.Status != "pending" {
			return fmt.Errorf("meeting summary action item status is invalid")
		}
	}
	return nil
}

type MeetingSummaryJob struct {
	ID                       string
	ProviderConfigID         string
	MeetingID                string
	UserID                   string
	Version                  int64
	SourceTranscriptRevision int64
	Language                 MeetingLanguage
	IdempotencyKey           string
	Status                   MeetingSummaryJobStatus
	Provider                 MeetingSummaryProviderName
	ModelName                string
	PromptVersion            string
	Result                   *MeetingSummary
	FailureReason            MeetingSummaryFailureReason
	AttemptCount             int32
	AvailableAt              time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type PrepareMeetingSummaryInput struct {
	MeetingID                string
	UserID                   string
	Version                  int64
	SourceTranscriptRevision int64
	Language                 MeetingLanguage
	IdempotencyKey           string
	Now                      time.Time
}

type MeetingTranscriptSnapshot struct {
	MeetingID          string
	Language           MeetingLanguage
	TranscriptRevision int64
	Segments           []TranscriptSegment
}

type MeetingSummaryGenerationRequest struct {
	JobID                    string
	MeetingID                string
	Version                  int64
	AttemptNumber            int32
	SourceTranscriptRevision int64
	Snapshot                 *MeetingTranscriptSnapshot
}

// MeetingSummaryLLMExchangeRecorder stores the latest provider exchange on
// the existing worker job. RequestPayload is the JSON body only and must never
// contain authorization headers or provider credentials.
type MeetingSummaryLLMExchangeRecorder interface {
	RecordLLMRequest(context.Context, string, string, time.Time) error
	RecordLLMResponse(context.Context, string, string, int32, time.Duration, int64, int64, string, time.Time) error
}

type MeetingSummaryProviderError struct {
	Reason    MeetingSummaryFailureReason
	Retryable bool
	Err       error
}

func (e *MeetingSummaryProviderError) Error() string {
	if e == nil || e.Err == nil {
		return "meeting summary provider failed"
	}
	return e.Err.Error()
}

func (e *MeetingSummaryProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type MeetingSummarizer interface {
	Summarize(context.Context, *MeetingSummaryGenerationRequest) (*MeetingSummary, error)
}

type MeetingSummaryProviderSnapshot struct {
	ConfigID      string
	Provider      MeetingSummaryProviderName
	ModelName     string
	PromptVersion string
}

func (s MeetingSummaryProviderSnapshot) Validate() error {
	if _, err := uuid.Parse(s.ConfigID); err != nil {
		return fmt.Errorf("meeting summary provider snapshot config ID is invalid: %w", err)
	}
	if _, err := ParseMeetingSummaryProviderName(string(s.Provider)); err != nil {
		return err
	}
	if !providerModelNamePattern.MatchString(s.ModelName) || !promptVersionPattern.MatchString(s.PromptVersion) {
		return fmt.Errorf("meeting summary provider snapshot metadata is invalid")
	}
	return nil
}

type MeetingSummaryRepo interface {
	MeetingSummaryLLMExchangeRecorder
	EnsureJob(context.Context, PrepareMeetingSummaryInput, MeetingSummaryProviderSnapshot) (*MeetingSummaryJob, error)
	ClaimJobs(context.Context, time.Time, time.Duration, int) ([]*MeetingSummaryJob, error)
	SaveGenerated(context.Context, string, *MeetingSummary, time.Time) error
	SaveFailureForDelivery(context.Context, string, MeetingSummaryFailureReason, time.Time) error
	RetryJob(context.Context, string, time.Time) error
	MarkSucceeded(context.Context, string, time.Time) error
	MarkFailed(context.Context, string, time.Time) error
}

type CoreMeetingSummarySink interface {
	GetTranscriptSnapshot(context.Context, string, int64) (*MeetingTranscriptSnapshot, error)
	CompleteMeetingSummary(context.Context, *MeetingSummaryJob) error
	FailMeetingSummary(context.Context, *MeetingSummaryJob, time.Time) error
}

type MeetingSummaryPolicy struct {
	LeaseTimeout   time.Duration
	BatchSize      int
	MaxAttempts    int32
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type MeetingSummaryUsecase struct {
	repo      MeetingSummaryRepo
	sink      CoreMeetingSummarySink
	providers MeetingSummarizerResolver
	policy    MeetingSummaryPolicy
	logger    *slog.Logger
}

func NewMeetingSummaryUsecase(repo MeetingSummaryRepo, sink CoreMeetingSummarySink, providers MeetingSummarizerResolver, policy MeetingSummaryPolicy, logger *slog.Logger) (*MeetingSummaryUsecase, error) {
	if repo == nil || sink == nil || providers == nil {
		return nil, fmt.Errorf("meeting summary repository, core sink and provider are required")
	}
	if policy.LeaseTimeout <= 0 || policy.LeaseTimeout > 10*time.Minute || policy.BatchSize <= 0 || policy.BatchSize > 20 ||
		policy.MaxAttempts <= 0 || policy.MaxAttempts > 100 || policy.InitialBackoff <= 0 ||
		policy.MaxBackoff < policy.InitialBackoff || policy.MaxBackoff > time.Hour {
		return nil, fmt.Errorf("meeting summary policy is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &MeetingSummaryUsecase{repo: repo, sink: sink, providers: providers, policy: policy, logger: logger}, nil
}

func (uc *MeetingSummaryUsecase) Prepare(ctx context.Context, input PrepareMeetingSummaryInput) (*MeetingSummaryJob, error) {
	if _, err := uuid.Parse(input.MeetingID); err != nil {
		return nil, fmt.Errorf("meeting ID is invalid")
	}
	if _, err := uuid.Parse(input.UserID); err != nil {
		return nil, fmt.Errorf("meeting summary user ID is invalid")
	}
	if input.Version <= 0 || input.SourceTranscriptRevision <= 0 || strings.TrimSpace(string(input.Language)) == "" ||
		strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 128 {
		return nil, fmt.Errorf("meeting summary input is invalid")
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	binding, err := uc.providers.ResolveActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve active meeting summary provider: %w", err)
	}
	if err := binding.Validate(); err != nil {
		return nil, fmt.Errorf("active meeting summary provider is invalid: %w", err)
	}
	selection := MeetingSummaryProviderSnapshot{
		ConfigID: binding.ConfigID, Provider: binding.Provider,
		ModelName: binding.ModelName, PromptVersion: binding.PromptVersion,
	}
	return uc.repo.EnsureJob(ctx, input, selection)
}

func (uc *MeetingSummaryUsecase) ProcessBatch(ctx context.Context, now time.Time) (int, error) {
	jobs, err := uc.repo.ClaimJobs(ctx, now.UTC(), uc.policy.LeaseTimeout, uc.policy.BatchSize)
	if err != nil {
		return 0, err
	}
	processed := 0
	var batchErrors []error
	for _, job := range jobs {
		if job == nil {
			return processed, fmt.Errorf("claimed meeting summary job is nil")
		}
		if err := uc.processJob(ctx, job, now.UTC()); err != nil {
			if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
				return processed, err
			}
			batchErrors = append(batchErrors, fmt.Errorf("process meeting summary job %s: %w", job.ID, err))
			continue
		}
		processed++
	}
	return processed, stderrors.Join(batchErrors...)
}

func (uc *MeetingSummaryUsecase) processJob(ctx context.Context, job *MeetingSummaryJob, now time.Time) error {
	switch job.Status {
	case MeetingSummaryJobStatusDeliveryPending:
		if job.Result == nil {
			return fmt.Errorf("meeting summary delivery result is missing")
		}
		if err := uc.sink.CompleteMeetingSummary(ctx, job); err != nil {
			return uc.retryDelivery(ctx, job, err, now)
		}
		return uc.repo.MarkSucceeded(ctx, job.ID, now)
	case MeetingSummaryJobStatusFailureDeliveryPending:
		if err := uc.sink.FailMeetingSummary(ctx, job, now); err != nil {
			return uc.retryDelivery(ctx, job, err, now)
		}
		return uc.repo.MarkFailed(ctx, job.ID, now)
	case MeetingSummaryJobStatusPending, MeetingSummaryJobStatusProcessing:
		return uc.generateAndDeliver(ctx, job, now)
	default:
		return fmt.Errorf("meeting summary job cannot be processed from status %q", job.Status)
	}
}

func (uc *MeetingSummaryUsecase) generateAndDeliver(ctx context.Context, job *MeetingSummaryJob, now time.Time) error {
	uc.logger.Info("meeting summary generation started",
		"job_id", job.ID,
		"meeting_id", job.MeetingID,
		"version", job.Version,
		"transcript_revision", job.SourceTranscriptRevision,
		"provider", job.Provider,
		"model", job.ModelName,
		"attempt", job.AttemptCount+1,
	)
	binding, err := uc.providers.Resolve(ctx, job.ProviderConfigID)
	if err != nil {
		return uc.handleGenerationFailure(ctx, job, MeetingSummaryFailureReasonProviderUnavailable, true, err, now)
	}
	if err := binding.Validate(); err != nil {
		return uc.handleGenerationFailure(ctx, job, MeetingSummaryFailureReasonInternal, false, err, now)
	}
	if binding.Provider != job.Provider || binding.ModelName != job.ModelName || binding.PromptVersion != job.PromptVersion {
		return uc.handleGenerationFailure(ctx, job, MeetingSummaryFailureReasonInternal, false, fmt.Errorf("meeting summary provider snapshot does not match job"), now)
	}
	snapshot, err := uc.sink.GetTranscriptSnapshot(ctx, job.MeetingID, job.SourceTranscriptRevision)
	if err != nil {
		return uc.handleGenerationFailure(ctx, job, MeetingSummaryFailureReasonTranscriptInvalid, false, err, now)
	}
	if err := validateTranscriptSnapshot(snapshot, job); err != nil {
		return uc.handleGenerationFailure(ctx, job, MeetingSummaryFailureReasonTranscriptInvalid, false, err, now)
	}
	result, err := binding.Summarizer.Summarize(ctx, &MeetingSummaryGenerationRequest{
		JobID: job.ID, MeetingID: job.MeetingID, Version: job.Version,
		AttemptNumber: job.AttemptCount + 1, SourceTranscriptRevision: job.SourceTranscriptRevision,
		Snapshot: snapshot,
	})
	if err != nil {
		var providerErr *MeetingSummaryProviderError
		if stderrors.As(err, &providerErr) {
			return uc.handleGenerationFailure(ctx, job, providerErr.Reason, providerErr.Retryable, err, now)
		}
		return uc.handleGenerationFailure(ctx, job, MeetingSummaryFailureReasonInternal, true, err, now)
	}
	if result == nil {
		return uc.handleGenerationFailure(ctx, job, MeetingSummaryFailureReasonOutputInvalid, false, fmt.Errorf("provider returned nil summary"), now)
	}
	result.Provider = job.Provider
	result.ModelName = job.ModelName
	result.PromptVersion = job.PromptVersion
	result.GeneratedAt = now
	if err := result.Validate(); err != nil {
		return uc.handleGenerationFailure(ctx, job, MeetingSummaryFailureReasonOutputInvalid, false, err, now)
	}
	if err := uc.repo.SaveGenerated(ctx, job.ID, result, now); err != nil {
		return err
	}
	job.Status = MeetingSummaryJobStatusDeliveryPending
	job.Result = result
	if err := uc.sink.CompleteMeetingSummary(ctx, job); err != nil {
		return uc.retryDelivery(ctx, job, err, now)
	}
	if err := uc.repo.MarkSucceeded(ctx, job.ID, now); err != nil {
		return err
	}
	uc.logger.Info("meeting summary generation completed",
		"job_id", job.ID,
		"meeting_id", job.MeetingID,
		"version", job.Version,
		"provider", job.Provider,
		"model", job.ModelName,
		"input_tokens", result.InputTokens,
		"output_tokens", result.OutputTokens,
	)
	return nil
}

func (uc *MeetingSummaryUsecase) handleGenerationFailure(ctx context.Context, job *MeetingSummaryJob, reason MeetingSummaryFailureReason, retryable bool, cause error, now time.Time) error {
	uc.logger.Error("meeting summary generation failed",
		"job_id", job.ID,
		"meeting_id", job.MeetingID,
		"version", job.Version,
		"transcript_revision", job.SourceTranscriptRevision,
		"provider", job.Provider,
		"model", job.ModelName,
		"attempt", job.AttemptCount+1,
		"reason", reason.String(),
		"retryable", retryable,
		"error", cause,
	)
	failedAttempts := job.AttemptCount + 1
	if retryable && failedAttempts < uc.policy.MaxAttempts {
		if err := uc.repo.RetryJob(ctx, job.ID, now.Add(uc.retryBackoff(failedAttempts))); err != nil {
			return stderrors.Join(cause, err)
		}
		return cause
	}
	if err := uc.repo.SaveFailureForDelivery(ctx, job.ID, reason, now); err != nil {
		return stderrors.Join(cause, err)
	}
	job.Status = MeetingSummaryJobStatusFailureDeliveryPending
	job.FailureReason = reason
	if err := uc.sink.FailMeetingSummary(ctx, job, now); err != nil {
		return uc.retryDelivery(ctx, job, err, now)
	}
	if err := uc.repo.MarkFailed(ctx, job.ID, now); err != nil {
		return err
	}
	// The provider failure has been persisted and reported to core. Treat this
	// execution as handled so the worker does not emit a misleading batch error.
	return nil
}

func (uc *MeetingSummaryUsecase) retryDelivery(ctx context.Context, job *MeetingSummaryJob, cause error, now time.Time) error {
	if err := uc.repo.RetryJob(ctx, job.ID, now.Add(uc.retryBackoff(job.AttemptCount+1))); err != nil {
		return stderrors.Join(cause, err)
	}
	return cause
}

func (uc *MeetingSummaryUsecase) retryBackoff(attempt int32) time.Duration {
	backoff := uc.policy.InitialBackoff
	for step := int32(1); step < attempt && backoff < uc.policy.MaxBackoff; step++ {
		if backoff > uc.policy.MaxBackoff/2 {
			return uc.policy.MaxBackoff
		}
		backoff *= 2
	}
	if backoff > uc.policy.MaxBackoff {
		return uc.policy.MaxBackoff
	}
	return backoff
}

func validateTranscriptSnapshot(snapshot *MeetingTranscriptSnapshot, job *MeetingSummaryJob) error {
	if snapshot == nil || snapshot.MeetingID != job.MeetingID || snapshot.TranscriptRevision != job.SourceTranscriptRevision ||
		len(snapshot.Segments) == 0 || len(snapshot.Segments) > maxSummaryTranscriptSegments {
		return fmt.Errorf("meeting transcript snapshot identity or size is invalid")
	}
	totalRunes := 0
	previous := int64(0)
	for _, segment := range snapshot.Segments {
		if segment.Sequence <= previous || strings.TrimSpace(segment.Content) == "" {
			return fmt.Errorf("meeting transcript snapshot ordering is invalid")
		}
		previous = segment.Sequence
		totalRunes += utf8.RuneCountInString(segment.Content)
		if totalRunes > maxSummaryTranscriptTotalRune {
			return fmt.Errorf("meeting transcript snapshot exceeds text limit")
		}
	}
	return nil
}

func validateSummaryText(name, value string, minimum, maximum int) error {
	count := utf8.RuneCountInString(strings.TrimSpace(value))
	if count < minimum || count > maximum {
		return fmt.Errorf("meeting summary %s length is invalid", name)
	}
	return nil
}
