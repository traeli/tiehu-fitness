package biz

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"
)

type TranscriptionOutboxEventType string

const (
	TranscriptionOutboxEventTypeFinalTranscriptReady TranscriptionOutboxEventType = "final_transcript_ready"
	TranscriptionOutboxEventTypeUsageReady           TranscriptionOutboxEventType = "transcription_usage_ready"
)

func ParseTranscriptionOutboxEventType(raw string) (TranscriptionOutboxEventType, error) {
	switch TranscriptionOutboxEventType(raw) {
	case TranscriptionOutboxEventTypeFinalTranscriptReady, TranscriptionOutboxEventTypeUsageReady:
		return TranscriptionOutboxEventType(raw), nil
	default:
		return "", fmt.Errorf("unknown transcription outbox event type %q", raw)
	}
}

type TranscriptionOutboxStatus string

const (
	TranscriptionOutboxStatusPending    TranscriptionOutboxStatus = "pending"
	TranscriptionOutboxStatusProcessing TranscriptionOutboxStatus = "processing"
	TranscriptionOutboxStatusDelivered  TranscriptionOutboxStatus = "delivered"
	TranscriptionOutboxStatusFailed     TranscriptionOutboxStatus = "failed"
)

func ParseTranscriptionOutboxStatus(raw string) (TranscriptionOutboxStatus, error) {
	switch TranscriptionOutboxStatus(raw) {
	case TranscriptionOutboxStatusPending, TranscriptionOutboxStatusProcessing,
		TranscriptionOutboxStatusDelivered, TranscriptionOutboxStatusFailed:
		return TranscriptionOutboxStatus(raw), nil
	default:
		return "", fmt.Errorf("unknown transcription outbox status %q", raw)
	}
}

type TranscriptionFailureReason uint8

const (
	TranscriptionFailureReasonUnspecified TranscriptionFailureReason = iota
	TranscriptionFailureReasonProviderUnavailable
	TranscriptionFailureReasonProviderRejected
	TranscriptionFailureReasonAudioInvalid
	TranscriptionFailureReasonTimeout
	TranscriptionFailureReasonQuotaExhausted
	TranscriptionFailureReasonCancelled
	TranscriptionFailureReasonInternal
)

type TranscriptionOutboxDelivery struct {
	ID           string
	Type         TranscriptionOutboxEventType
	AttemptCount int32
	Session      *TranscriptionSession
	Segments     []TranscriptSegment
}

type TranscriptionOutboxRepo interface {
	ClaimTranscriptionDeliveries(context.Context, time.Time, time.Duration, int, int32) ([]*TranscriptionOutboxDelivery, error)
	MarkTranscriptionDeliveryDelivered(context.Context, string, time.Time) error
	RetryTranscriptionDelivery(context.Context, string, time.Time, bool) error
}

type CoreMeetingIngestSink interface {
	AppendFinalTranscriptSegments(context.Context, string, *TranscriptionSession, []TranscriptSegment) error
	ReportTranscriptionUsage(context.Context, *TranscriptionSession, time.Duration, time.Time) error
	CompleteTranscription(context.Context, *TranscriptionSession, time.Duration, time.Duration, time.Time) error
	FailTranscription(context.Context, *TranscriptionSession, time.Duration, TranscriptionFailureReason, time.Time) error
}

type TranscriptionOutboxPolicy struct {
	LeaseTimeout   time.Duration
	BatchSize      int
	MaxAttempts    int32
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Audio          AudioSpec
}

type TranscriptionOutboxUsecase struct {
	repo   TranscriptionOutboxRepo
	sink   CoreMeetingIngestSink
	policy TranscriptionOutboxPolicy
}

func NewTranscriptionOutboxUsecase(repo TranscriptionOutboxRepo, sink CoreMeetingIngestSink, policy TranscriptionOutboxPolicy) (*TranscriptionOutboxUsecase, error) {
	if repo == nil || sink == nil {
		return nil, fmt.Errorf("transcription outbox repository and core sink are required")
	}
	if policy.LeaseTimeout <= 0 || policy.LeaseTimeout > 10*time.Minute || policy.BatchSize <= 0 || policy.BatchSize > 100 ||
		policy.MaxAttempts <= 0 || policy.MaxAttempts > 1_000 || policy.InitialBackoff <= 0 ||
		policy.MaxBackoff < policy.InitialBackoff || policy.MaxBackoff > time.Hour {
		return nil, fmt.Errorf("transcription outbox policy is invalid")
	}
	if err := policy.Audio.Validate(); err != nil {
		return nil, err
	}
	return &TranscriptionOutboxUsecase{repo: repo, sink: sink, policy: policy}, nil
}

func (uc *TranscriptionOutboxUsecase) ProcessBatch(ctx context.Context, now time.Time) (int, error) {
	if ctx == nil {
		return 0, fmt.Errorf("context is required")
	}
	now = now.UTC()
	deliveries, err := uc.repo.ClaimTranscriptionDeliveries(ctx, now, uc.policy.LeaseTimeout, uc.policy.BatchSize, uc.policy.MaxAttempts)
	if err != nil {
		return 0, err
	}
	delivered := 0
	var deliveryErrors []error
	for _, delivery := range deliveries {
		if delivery == nil || delivery.Session == nil {
			return delivered, fmt.Errorf("claimed transcription delivery is invalid")
		}
		deliveryErr := uc.deliver(ctx, delivery)
		if deliveryErr == nil {
			if err := uc.repo.MarkTranscriptionDeliveryDelivered(ctx, delivery.ID, now); err != nil {
				return delivered, err
			}
			delivered++
			continue
		}
		if stderrors.Is(deliveryErr, context.Canceled) || stderrors.Is(deliveryErr, context.DeadlineExceeded) {
			return delivered, deliveryErr
		}
		failedAttempts := delivery.AttemptCount + 1
		terminal := failedAttempts >= uc.policy.MaxAttempts
		next := now.Add(uc.retryBackoff(failedAttempts))
		if err := uc.repo.RetryTranscriptionDelivery(ctx, delivery.ID, next, terminal); err != nil {
			return delivered, stderrors.Join(deliveryErr, err)
		}
		deliveryErrors = append(deliveryErrors, fmt.Errorf("deliver transcription outbox event %s: %w", delivery.ID, deliveryErr))
	}
	return delivered, stderrors.Join(deliveryErrors...)
}

func (uc *TranscriptionOutboxUsecase) deliver(ctx context.Context, delivery *TranscriptionOutboxDelivery) error {
	switch delivery.Type {
	case TranscriptionOutboxEventTypeFinalTranscriptReady:
		if len(delivery.Segments) == 0 {
			return nil
		}
		return uc.sink.AppendFinalTranscriptSegments(ctx, delivery.ID, delivery.Session, delivery.Segments)
	case TranscriptionOutboxEventTypeUsageReady:
		accepted, err := delivery.Session.AcceptedAudioDuration(uc.policy.Audio)
		if err != nil {
			return err
		}
		at := delivery.Session.UpdatedAt
		if delivery.Session.FinishedAt != nil {
			at = *delivery.Session.FinishedAt
		}
		if err := uc.sink.ReportTranscriptionUsage(ctx, delivery.Session, accepted.Duration(), at); err != nil {
			return err
		}
		switch delivery.Session.Status {
		case TranscriptionSessionStatusSucceeded:
			return uc.sink.CompleteTranscription(ctx, delivery.Session, accepted.Duration(), 0, at)
		case TranscriptionSessionStatusFailed, TranscriptionSessionStatusCancelled, TranscriptionSessionStatusExpired:
			return uc.sink.FailTranscription(ctx, delivery.Session, accepted.Duration(), transcriptionFailureReason(delivery.Session), at)
		default:
			return fmt.Errorf("transcription usage delivery requires a terminal session")
		}
	default:
		return fmt.Errorf("unsupported transcription outbox event type %q", delivery.Type)
	}
}

func (uc *TranscriptionOutboxUsecase) retryBackoff(attempt int32) time.Duration {
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

func transcriptionFailureReason(session *TranscriptionSession) TranscriptionFailureReason {
	if session.Status == TranscriptionSessionStatusCancelled {
		return TranscriptionFailureReasonCancelled
	}
	if session.Status == TranscriptionSessionStatusExpired {
		return TranscriptionFailureReasonTimeout
	}
	switch session.FailureCode {
	case "ASR_PROVIDER_REJECTED":
		return TranscriptionFailureReasonProviderRejected
	case "AUDIO_INVALID":
		return TranscriptionFailureReasonAudioInvalid
	case "QUOTA_EXHAUSTED":
		return TranscriptionFailureReasonQuotaExhausted
	case "ASR_START_FAILED", "ASR_PUSH_FAILED", "ASR_FINISH_FAILED", "ASR_PROVIDER_UNAVAILABLE":
		return TranscriptionFailureReasonProviderUnavailable
	default:
		return TranscriptionFailureReasonInternal
	}
}
