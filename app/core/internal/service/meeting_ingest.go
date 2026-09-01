package service

import (
	"context"
	"math"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MeetingIngestInternalService adapts vision's trusted internal commands to
// the meeting domain. Idempotency is enforced by the core PostgreSQL ledger.
type MeetingIngestInternalService struct {
	meetingv1.UnimplementedMeetingIngestInternalServiceServer
	uc *biz.MeetingUsecase
}

func NewMeetingIngestInternalService(uc *biz.MeetingUsecase) *MeetingIngestInternalService {
	return &MeetingIngestInternalService{uc: uc}
}

func (s *MeetingIngestInternalService) AppendFinalTranscriptSegments(ctx context.Context, req *meetingv1.AppendFinalTranscriptSegmentsRequest) (*meetingv1.AppendFinalTranscriptSegmentsResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	segments := make([]*biz.MeetingTranscriptSegment, 0, len(req.GetSegments()))
	for _, segment := range req.GetSegments() {
		if segment == nil {
			return nil, kratoserrors.BadRequest("TRANSCRIPT_SEGMENT_INVALID", "transcript segment is required")
		}
		language, err := meetingLanguageFromProto(segment.GetLanguage())
		if err != nil {
			return nil, err
		}
		if segment.GetStartOffset() == nil || segment.GetStartOffset().CheckValid() != nil ||
			segment.GetEndOffset() == nil || segment.GetEndOffset().CheckValid() != nil ||
			segment.GetCreatedAt() == nil || segment.GetCreatedAt().CheckValid() != nil {
			return nil, kratoserrors.BadRequest("TRANSCRIPT_SEGMENT_INVALID", "transcript segment timestamps are invalid")
		}
		segments = append(segments, &biz.MeetingTranscriptSegment{
			ID: segment.GetSegmentId(), MeetingID: req.GetMeetingId(), SequenceNo: segment.GetSequenceNo(),
			StartOffset: segment.GetStartOffset().AsDuration(), EndOffset: segment.GetEndOffset().AsDuration(),
			SpeakerLabel: segment.GetSpeakerLabel(), Content: segment.GetContent(), Language: language,
			Confidence: segment.Confidence, CreatedAt: segment.GetCreatedAt().AsTime(),
		})
	}
	last, err := s.uc.AppendFinalTranscriptSegments(ctx, req.GetMeetingId(), req.GetSessionId(), req.GetBatchId(), segments)
	if err != nil {
		return nil, err
	}
	return &meetingv1.AppendFinalTranscriptSegmentsResponse{LastPersistedSequenceNo: last}, nil
}

func (s *MeetingIngestInternalService) ReportTranscriptionUsage(ctx context.Context, req *meetingv1.ReportTranscriptionUsageRequest) (*meetingv1.ReportTranscriptionUsageResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	total, err := billableAudioSeconds(req.GetTotalAcceptedAudioDuration(), "total accepted audio duration")
	if err != nil {
		return nil, err
	}
	if req.GetObservedAt() == nil || req.GetObservedAt().CheckValid() != nil {
		return nil, kratoserrors.BadRequest("OBSERVED_AT_INVALID", "observed_at is invalid")
	}
	reservation, err := s.uc.ReportTranscriptionUsage(ctx, req.GetMeetingId(), req.GetSessionId(), req.GetReservationId(), total, req.GetObservedAt().AsTime())
	if err != nil {
		return nil, err
	}
	return &meetingv1.ReportTranscriptionUsageResponse{RecordedAudioDuration: durationpb.New(time.Duration(reservation.ReportedSeconds) * time.Second)}, nil
}

func (s *MeetingIngestInternalService) CompleteTranscription(ctx context.Context, req *meetingv1.CompleteTranscriptionRequest) (*meetingv1.CompleteTranscriptionResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	command, err := finalizeCommand(req.GetMeetingId(), req.GetSessionId(), req.GetReservationId(), req.GetTotalAcceptedAudioDuration(), req.GetProviderUsageDuration(), req.GetCompletedAt())
	if err != nil {
		return nil, err
	}
	result, err := s.uc.CompleteTranscription(ctx, command)
	if err != nil {
		return nil, err
	}
	return &meetingv1.CompleteTranscriptionResponse{
		Status:               transcriptionStatusToProto(result.Meeting.TranscriptionStatus),
		SettledAudioDuration: durationpb.New(time.Duration(result.Usage.ActualSeconds) * time.Second),
	}, nil
}

func (s *MeetingIngestInternalService) FailTranscription(ctx context.Context, req *meetingv1.FailTranscriptionRequest) (*meetingv1.FailTranscriptionResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	command, err := finalizeCommand(req.GetMeetingId(), req.GetSessionId(), req.GetReservationId(), req.GetTotalAcceptedAudioDuration(), nil, req.GetFailedAt())
	if err != nil {
		return nil, err
	}
	command.SettlementReason, err = failureSettlementReason(req.GetReason())
	if err != nil {
		return nil, err
	}
	result, err := s.uc.FailTranscription(ctx, command)
	if err != nil {
		return nil, err
	}
	return &meetingv1.FailTranscriptionResponse{
		Status:               transcriptionStatusToProto(result.Meeting.TranscriptionStatus),
		SettledAudioDuration: durationpb.New(time.Duration(result.Usage.ActualSeconds) * time.Second),
	}, nil
}

func (s *MeetingIngestInternalService) GetMeetingTranscriptSnapshot(ctx context.Context, req *meetingv1.GetMeetingTranscriptSnapshotRequest) (*meetingv1.GetMeetingTranscriptSnapshotResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	snapshot, err := s.uc.GetTranscriptSnapshot(ctx, req.GetMeetingId(), req.GetTranscriptRevision())
	if err != nil {
		return nil, err
	}
	segments := make([]*meetingv1.TranscriptSegment, 0, len(snapshot.Segments))
	for _, segment := range snapshot.Segments {
		segments = append(segments, toTranscriptSegmentProto(segment))
	}
	return &meetingv1.GetMeetingTranscriptSnapshotResponse{
		Language: meetingLanguageToProto(snapshot.Language), TranscriptRevision: snapshot.TranscriptRevision,
		Segments: segments,
	}, nil
}

func (s *MeetingIngestInternalService) CompleteMeetingSummary(ctx context.Context, req *meetingv1.CompleteMeetingSummaryRequest) (*meetingv1.CompleteMeetingSummaryResponse, error) {
	if req == nil || req.GetSummary() == nil {
		return nil, kratoserrors.BadRequest("SUMMARY_RESULT_REQUIRED", "meeting summary result is required")
	}
	summary, err := meetingSummaryFromProto(req.GetSummary())
	if err != nil {
		return nil, err
	}
	result, err := s.uc.CompleteSummary(ctx, biz.CompleteMeetingSummaryCommand{
		MeetingID: req.GetMeetingId(), Version: req.GetVersion(),
		SourceTranscriptRevision: req.GetSourceTranscriptRevision(), Summary: summary,
	})
	if err != nil {
		return nil, err
	}
	return &meetingv1.CompleteMeetingSummaryResponse{Status: summaryStatusToProto(result.Status)}, nil
}

func (s *MeetingIngestInternalService) FailMeetingSummary(ctx context.Context, req *meetingv1.FailMeetingSummaryRequest) (*meetingv1.FailMeetingSummaryResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	if req.GetFailedAt() == nil || req.GetFailedAt().CheckValid() != nil {
		return nil, kratoserrors.BadRequest("SUMMARY_FAILED_AT_INVALID", "failed_at is invalid")
	}
	reason, err := meetingSummaryFailureReasonFromProto(req.GetReason())
	if err != nil {
		return nil, err
	}
	err = s.uc.FailSummary(ctx, biz.FailMeetingSummaryCommand{
		MeetingID: req.GetMeetingId(), Version: req.GetVersion(),
		SourceTranscriptRevision: req.GetSourceTranscriptRevision(), Reason: reason,
		FailedAt: req.GetFailedAt().AsTime(),
	})
	if err != nil {
		return nil, err
	}
	return &meetingv1.FailMeetingSummaryResponse{Status: meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_FAILED}, nil
}

func meetingSummaryFromProto(input *meetingv1.MeetingSummary) (*biz.MeetingSummary, error) {
	if input == nil || input.GetGeneratedAt() == nil || input.GetGeneratedAt().CheckValid() != nil {
		return nil, kratoserrors.BadRequest("SUMMARY_RESULT_INVALID", "meeting summary generated_at is invalid")
	}
	actionItems := make([]biz.MeetingActionItem, 0, len(input.GetActionItems()))
	for _, item := range input.GetActionItems() {
		if item == nil {
			return nil, kratoserrors.BadRequest("SUMMARY_RESULT_INVALID", "meeting summary action item is required")
		}
		status, err := meetingActionItemStatusFromProto(item.GetStatus())
		if err != nil {
			return nil, err
		}
		actionItems = append(actionItems, biz.MeetingActionItem{
			Assignee: item.GetAssignee(), Task: item.GetTask(), DueText: item.GetDueText(), Status: status,
		})
	}
	generatedAt := input.GetGeneratedAt().AsTime()
	return &biz.MeetingSummary{
		MeetingID: input.GetMeetingId(), Version: input.GetVersion(),
		SourceTranscriptRevision: input.GetSourceTranscriptRevision(),
		Status:                   biz.MeetingSummaryStatusSucceeded, Topic: input.GetTopic(), Abstract: input.GetAbstract(),
		KeyDiscussions: input.GetKeyDiscussions(), Decisions: input.GetDecisions(),
		ActionItems: actionItems, Risks: input.GetRisks(), Provider: input.GetProvider(),
		ModelName: input.GetModelName(), PromptVersion: input.GetPromptVersion(),
		InputTokens: input.GetInputTokens(), OutputTokens: input.GetOutputTokens(),
		GeneratedAt: &generatedAt, UpdatedAt: generatedAt,
	}, nil
}

func meetingActionItemStatusFromProto(status meetingv1.MeetingActionItemStatus) (biz.MeetingActionItemStatus, error) {
	switch status {
	case meetingv1.MeetingActionItemStatus_MEETING_ACTION_ITEM_STATUS_PENDING:
		return biz.MeetingActionItemStatusPending, nil
	case meetingv1.MeetingActionItemStatus_MEETING_ACTION_ITEM_STATUS_UNSPECIFIED:
		return biz.MeetingActionItemStatusUnspecified, kratoserrors.BadRequest("SUMMARY_ACTION_STATUS_REQUIRED", "action item status is required")
	default:
		return biz.MeetingActionItemStatusUnspecified, kratoserrors.BadRequest("SUMMARY_ACTION_STATUS_INVALID", "action item status is invalid")
	}
}

func meetingSummaryFailureReasonFromProto(reason meetingv1.MeetingSummaryFailureReason) (biz.MeetingSummaryFailureReason, error) {
	switch reason {
	case meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_PROVIDER_UNAVAILABLE:
		return biz.MeetingSummaryFailureReasonProviderUnavailable, nil
	case meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_PROVIDER_REJECTED:
		return biz.MeetingSummaryFailureReasonProviderRejected, nil
	case meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_OUTPUT_INVALID:
		return biz.MeetingSummaryFailureReasonOutputInvalid, nil
	case meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_TRANSCRIPT_INVALID:
		return biz.MeetingSummaryFailureReasonTranscriptInvalid, nil
	case meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_TIMEOUT:
		return biz.MeetingSummaryFailureReasonTimeout, nil
	case meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_INTERNAL:
		return biz.MeetingSummaryFailureReasonInternal, nil
	case meetingv1.MeetingSummaryFailureReason_MEETING_SUMMARY_FAILURE_REASON_UNSPECIFIED:
		return biz.MeetingSummaryFailureReasonUnspecified, kratoserrors.BadRequest("SUMMARY_FAILURE_REASON_REQUIRED", "summary failure reason is required")
	default:
		return biz.MeetingSummaryFailureReasonUnspecified, kratoserrors.BadRequest("SUMMARY_FAILURE_REASON_INVALID", "summary failure reason is invalid")
	}
}

func finalizeCommand(meetingID, sessionID, reservationID string, accepted, provider *durationpb.Duration, at *timestamppb.Timestamp) (biz.FinalizeMeetingTranscriptionCommand, error) {
	totalSeconds, err := billableAudioSeconds(accepted, "total accepted audio duration")
	if err != nil {
		return biz.FinalizeMeetingTranscriptionCommand{}, err
	}
	providerSeconds := int64(0)
	if provider != nil {
		providerSeconds, err = durationSecondsCeil(provider, "provider usage duration")
		if err != nil {
			return biz.FinalizeMeetingTranscriptionCommand{}, err
		}
	}
	if at == nil || at.CheckValid() != nil {
		return biz.FinalizeMeetingTranscriptionCommand{}, kratoserrors.BadRequest("TRANSCRIPTION_FINALIZED_AT_INVALID", "transcription finalization timestamp is invalid")
	}
	return biz.FinalizeMeetingTranscriptionCommand{
		MeetingID: meetingID, SessionID: sessionID, ReservationID: reservationID,
		TotalAcceptedSeconds: totalSeconds, ProviderUsageSeconds: providerSeconds, FinalizedAt: at.AsTime(),
	}, nil
}

func durationSecondsCeil(value *durationpb.Duration, field string) (int64, error) {
	if value == nil || value.CheckValid() != nil || value.AsDuration() < 0 {
		return 0, kratoserrors.BadRequest("TRANSCRIPTION_USAGE_INVALID", field+" is invalid")
	}
	duration := value.AsDuration()
	if duration > time.Duration(math.MaxInt64-time.Second+1) {
		return 0, kratoserrors.BadRequest("TRANSCRIPTION_USAGE_INVALID", field+" is out of range")
	}
	return int64((duration + time.Second - 1) / time.Second), nil
}

func billableAudioSeconds(value *durationpb.Duration, field string) (int64, error) {
	if value == nil || value.CheckValid() != nil || value.AsDuration() < 0 {
		return 0, kratoserrors.BadRequest("TRANSCRIPTION_USAGE_INVALID", field+" is invalid")
	}
	return biz.RoundMeetingAudioUsage(value.AsDuration())
}

func failureSettlementReason(reason meetingv1.TranscriptionFailureReason) (biz.MeetingUsageSettlementReason, error) {
	switch reason {
	case meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_QUOTA_EXHAUSTED:
		return biz.MeetingUsageSettlementReasonQuotaExhausted, nil
	case meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_CANCELLED:
		return biz.MeetingUsageSettlementReasonCancelled, nil
	case meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_PROVIDER_UNAVAILABLE,
		meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_PROVIDER_REJECTED,
		meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_AUDIO_INVALID,
		meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_TIMEOUT,
		meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_INTERNAL:
		return biz.MeetingUsageSettlementReasonFailed, nil
	case meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_UNSPECIFIED:
		return biz.MeetingUsageSettlementReasonUnspecified, kratoserrors.BadRequest("TRANSCRIPTION_FAILURE_REASON_REQUIRED", "failure reason is required")
	default:
		return biz.MeetingUsageSettlementReasonUnspecified, kratoserrors.BadRequest("TRANSCRIPTION_FAILURE_REASON_INVALID", "failure reason is invalid")
	}
}
