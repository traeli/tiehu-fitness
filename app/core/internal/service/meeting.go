package service

import (
	"context"
	"strings"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type MeetingService struct {
	meetingv1.UnimplementedMeetingServiceServer
	uc *biz.MeetingUsecase
}

func NewMeetingService(uc *biz.MeetingUsecase) *MeetingService {
	return &MeetingService{uc: uc}
}

func (s *MeetingService) CreateMeeting(ctx context.Context, req *meetingv1.CreateMeetingRequest) (*meetingv1.CreateMeetingResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	userID, err := biz.RequireCurrentUserID(ctx, "")
	if err != nil {
		return nil, err
	}
	idempotencyKey, err := requiredIdempotencyKey(ctx)
	if err != nil {
		return nil, err
	}
	language, err := meetingLanguageFromProto(req.GetLanguage())
	if err != nil {
		return nil, err
	}
	result, err := s.uc.Create(ctx, biz.CreateMeetingCommand{
		UserID: userID, IdempotencyKey: idempotencyKey, Language: language,
		RetainAudio: req.GetRetainAudio(), TranscriptionConsent: req.GetTranscriptionConsent(),
	})
	if err != nil {
		return nil, err
	}
	return &meetingv1.CreateMeetingResponse{
		Meeting: toMeetingProto(result.Meeting), TranscriptionSession: toTranscriptionSessionProto(result.Session),
	}, nil
}

func (s *MeetingService) StopMeeting(ctx context.Context, req *meetingv1.StopMeetingRequest) (*meetingv1.StopMeetingResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	userID, err := biz.RequireCurrentUserID(ctx, "")
	if err != nil {
		return nil, err
	}
	idempotencyKey, err := requiredIdempotencyKey(ctx)
	if err != nil {
		return nil, err
	}
	meeting, err := s.uc.Stop(ctx, userID, req.GetMeetingId(), idempotencyKey, time.Time{})
	if err != nil {
		return nil, err
	}
	return &meetingv1.StopMeetingResponse{Meeting: toMeetingProto(meeting)}, nil
}

func (s *MeetingService) GetMeeting(ctx context.Context, req *meetingv1.GetMeetingRequest) (*meetingv1.GetMeetingResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	userID, err := biz.RequireCurrentUserID(ctx, "")
	if err != nil {
		return nil, err
	}
	meeting, err := s.uc.Get(ctx, userID, req.GetMeetingId())
	if err != nil {
		return nil, err
	}
	return &meetingv1.GetMeetingResponse{Meeting: toMeetingProto(meeting)}, nil
}

func (s *MeetingService) ListTranscriptSegments(ctx context.Context, req *meetingv1.ListTranscriptSegmentsRequest) (*meetingv1.ListTranscriptSegmentsResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	userID, err := biz.RequireCurrentUserID(ctx, "")
	if err != nil {
		return nil, err
	}
	result, err := s.uc.ListTranscriptSegments(ctx, userID, req.GetMeetingId(), req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	segments := make([]*meetingv1.TranscriptSegment, 0, len(result.Segments))
	for _, segment := range result.Segments {
		segments = append(segments, toTranscriptSegmentProto(segment))
	}
	return &meetingv1.ListTranscriptSegmentsResponse{Segments: segments, NextPageToken: result.NextPageToken}, nil
}

func (s *MeetingService) GetMeetingSummary(ctx context.Context, req *meetingv1.GetMeetingSummaryRequest) (*meetingv1.GetMeetingSummaryResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	userID, err := biz.RequireCurrentUserID(ctx, "")
	if err != nil {
		return nil, err
	}
	view, err := s.uc.GetSummary(ctx, userID, req.GetMeetingId())
	if err != nil {
		return nil, err
	}
	return &meetingv1.GetMeetingSummaryResponse{
		Status: summaryStatusToProto(view.Status), Summary: toMeetingSummaryProto(view.Summary),
		FailureReason: view.FailureReason,
	}, nil
}

func (s *MeetingService) RegenerateMeetingSummary(ctx context.Context, req *meetingv1.RegenerateMeetingSummaryRequest) (*meetingv1.RegenerateMeetingSummaryResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	userID, err := biz.RequireCurrentUserID(ctx, "")
	if err != nil {
		return nil, err
	}
	idempotencyKey, err := requiredIdempotencyKey(ctx)
	if err != nil {
		return nil, err
	}
	task, err := s.uc.RegenerateSummary(ctx, userID, req.GetMeetingId(), idempotencyKey, time.Time{})
	if err != nil {
		return nil, err
	}
	return &meetingv1.RegenerateMeetingSummaryResponse{
		Status: meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_PROCESSING, Version: task.Version,
	}, nil
}

func (s *MeetingService) GetMeetingQuota(ctx context.Context, req *meetingv1.GetMeetingQuotaRequest) (*meetingv1.GetMeetingQuotaResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	userID, err := biz.RequireCurrentUserID(ctx, "")
	if err != nil {
		return nil, err
	}
	quota, err := s.uc.GetQuota(ctx, userID, time.Time{})
	if err != nil {
		return nil, err
	}
	return &meetingv1.GetMeetingQuotaResponse{Quota: toMeetingQuotaProto(quota)}, nil
}

func requiredIdempotencyKey(ctx context.Context) (string, error) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok || tr.RequestHeader() == nil {
		return "", kratoserrors.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")
	}
	key := strings.TrimSpace(tr.RequestHeader().Get("Idempotency-Key"))
	if key == "" {
		key = strings.TrimSpace(tr.RequestHeader().Get("idempotency-key"))
	}
	if key == "" {
		return "", kratoserrors.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")
	}
	return key, nil
}

func meetingLanguageFromProto(language meetingv1.MeetingLanguage) (biz.MeetingLanguage, error) {
	switch language {
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_AUTO:
		return biz.MeetingLanguageAuto, nil
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN:
		return biz.MeetingLanguageZhCN, nil
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_EN_US:
		return biz.MeetingLanguageEnUS, nil
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_UNSPECIFIED:
		return biz.MeetingLanguageUnspecified, kratoserrors.BadRequest("MEETING_LANGUAGE_REQUIRED", "meeting language is required")
	default:
		return biz.MeetingLanguageUnspecified, kratoserrors.BadRequest("MEETING_LANGUAGE_INVALID", "meeting language is invalid")
	}
}

func meetingLanguageToProto(language biz.MeetingLanguage) meetingv1.MeetingLanguage {
	switch language {
	case biz.MeetingLanguageAuto:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_AUTO
	case biz.MeetingLanguageZhCN:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN
	case biz.MeetingLanguageEnUS:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_EN_US
	default:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_UNSPECIFIED
	}
}

func meetingStatusToProto(status biz.MeetingStatus) meetingv1.MeetingStatus {
	switch status {
	case biz.MeetingStatusRecording:
		return meetingv1.MeetingStatus_MEETING_STATUS_RECORDING
	case biz.MeetingStatusProcessing:
		return meetingv1.MeetingStatus_MEETING_STATUS_PROCESSING
	case biz.MeetingStatusCompleted:
		return meetingv1.MeetingStatus_MEETING_STATUS_COMPLETED
	case biz.MeetingStatusPartiallyCompleted:
		return meetingv1.MeetingStatus_MEETING_STATUS_PARTIALLY_COMPLETED
	case biz.MeetingStatusFailed:
		return meetingv1.MeetingStatus_MEETING_STATUS_FAILED
	case biz.MeetingStatusCancelled:
		return meetingv1.MeetingStatus_MEETING_STATUS_CANCELLED
	default:
		return meetingv1.MeetingStatus_MEETING_STATUS_UNSPECIFIED
	}
}

func transcriptionStatusToProto(status biz.MeetingTranscriptionStatus) meetingv1.TranscriptionStatus {
	switch status {
	case biz.MeetingTranscriptionStatusPending:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_PENDING
	case biz.MeetingTranscriptionStatusConnecting:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_CONNECTING
	case biz.MeetingTranscriptionStatusStreaming:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_STREAMING
	case biz.MeetingTranscriptionStatusFinishing:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_FINISHING
	case biz.MeetingTranscriptionStatusSucceeded:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_SUCCEEDED
	case biz.MeetingTranscriptionStatusFailed:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_FAILED
	case biz.MeetingTranscriptionStatusCancelled:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_CANCELLED
	case biz.MeetingTranscriptionStatusExpired:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_EXPIRED
	default:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_UNSPECIFIED
	}
}

func summaryStatusToProto(status biz.MeetingSummaryStatus) meetingv1.MeetingSummaryStatus {
	switch status {
	case biz.MeetingSummaryStatusNotStarted:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_NOT_STARTED
	case biz.MeetingSummaryStatusPending:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_PENDING
	case biz.MeetingSummaryStatusProcessing:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_PROCESSING
	case biz.MeetingSummaryStatusSucceeded:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_SUCCEEDED
	case biz.MeetingSummaryStatusFailed:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_FAILED
	default:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_UNSPECIFIED
	}
}

func toMeetingProto(meeting *biz.Meeting) *meetingv1.Meeting {
	if meeting == nil {
		return nil
	}
	reply := &meetingv1.Meeting{
		MeetingId: meeting.ID, Status: meetingStatusToProto(meeting.Status),
		TranscriptionStatus: transcriptionStatusToProto(meeting.TranscriptionStatus),
		Language:            meetingLanguageToProto(meeting.Language), RetainAudio: meeting.RetainAudio,
		GrantedAudioDuration: durationpb.New(time.Duration(meeting.GrantedAudioSeconds) * time.Second),
		StartedAt:            timestamppb.New(meeting.StartedAt), CreatedAt: timestamppb.New(meeting.CreatedAt), UpdatedAt: timestamppb.New(meeting.UpdatedAt),
		SummaryStatus: summaryStatusToProto(meeting.SummaryStatus), SummaryVersion: meeting.SummaryVersion,
		TranscriptRevision: meeting.TranscriptRevision,
	}
	if meeting.StoppedAt != nil {
		reply.StoppedAt = timestamppb.New(*meeting.StoppedAt)
	}
	return reply
}

func toMeetingSummaryProto(summary *biz.MeetingSummary) *meetingv1.MeetingSummary {
	if summary == nil {
		return nil
	}
	actionItems := make([]*meetingv1.MeetingActionItem, 0, len(summary.ActionItems))
	for _, item := range summary.ActionItems {
		actionItems = append(actionItems, &meetingv1.MeetingActionItem{
			Assignee: item.Assignee, Task: item.Task, DueText: item.DueText,
			Status: meetingv1.MeetingActionItemStatus_MEETING_ACTION_ITEM_STATUS_PENDING,
		})
	}
	reply := &meetingv1.MeetingSummary{
		MeetingId: summary.MeetingID, Version: summary.Version,
		SourceTranscriptRevision: summary.SourceTranscriptRevision,
		Topic:                    summary.Topic, Abstract: summary.Abstract,
		KeyDiscussions: summary.KeyDiscussions, Decisions: summary.Decisions,
		ActionItems: actionItems, Risks: summary.Risks,
	}
	if summary.GeneratedAt != nil {
		reply.GeneratedAt = timestamppb.New(*summary.GeneratedAt)
	}
	return reply
}

func toTranscriptionSessionProto(session *biz.MeetingTranscriptionSession) *meetingv1.TranscriptionSession {
	if session == nil {
		return nil
	}
	return &meetingv1.TranscriptionSession{
		SessionId: session.ID, WebsocketUrl: session.WebSocketURL, SessionTicket: session.Ticket,
		ExpiresAt: timestamppb.New(session.ExpiresAt),
		Audio: &meetingv1.AudioSpec{
			Format: meetingv1.AudioFormat_AUDIO_FORMAT_PCM_S16LE, MimeType: session.Audio.MIMEType,
			SampleRate: session.Audio.SampleRate, Channels: session.Audio.Channels,
			ChunkDuration: durationpb.New(session.Audio.ChunkDuration), MaxChunkBytes: session.Audio.MaxChunkBytes,
		},
		GrantedAudioDuration: durationpb.New(time.Duration(session.GrantedAudioSeconds) * time.Second),
	}
}

func toTranscriptSegmentProto(segment *biz.MeetingTranscriptSegment) *meetingv1.TranscriptSegment {
	if segment == nil {
		return nil
	}
	return &meetingv1.TranscriptSegment{
		SegmentId: segment.ID, SequenceNo: segment.SequenceNo,
		StartOffset: durationpb.New(segment.StartOffset), EndOffset: durationpb.New(segment.EndOffset),
		SpeakerLabel: segment.SpeakerLabel, Content: segment.Content,
		Language: meetingLanguageToProto(segment.Language), Confidence: segment.Confidence,
		CreatedAt: timestamppb.New(segment.CreatedAt),
	}
}

func toMeetingQuotaProto(quota *biz.MeetingQuotaSnapshot) *meetingv1.MeetingQuota {
	if quota == nil {
		return nil
	}
	return &meetingv1.MeetingQuota{
		PeriodStart: timestamppb.New(quota.Period.Start), PeriodEnd: timestamppb.New(quota.Period.End),
		Limit:                 durationpb.New(time.Duration(quota.TotalLimitSeconds) * time.Second),
		Consumed:              durationpb.New(time.Duration(quota.ConsumedSeconds) * time.Second),
		Reserved:              durationpb.New(time.Duration(quota.ReservedSeconds) * time.Second),
		Remaining:             durationpb.New(time.Duration(quota.RemainingSeconds) * time.Second),
		MaxMeetingDuration:    durationpb.New(time.Duration(quota.MaxMeetingSeconds) * time.Second),
		MaxConcurrentMeetings: quota.MaxConcurrentMeetings, ActiveMeetings: quota.ActiveMeetings,
		BaseLimit:      durationpb.New(time.Duration(quota.BaseLimitSeconds) * time.Second),
		PurchasedLimit: durationpb.New(time.Duration(quota.PurchasedLimitSeconds) * time.Second),
		TotalLimit:     durationpb.New(time.Duration(quota.TotalLimitSeconds) * time.Second),
	}
}
