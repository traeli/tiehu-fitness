package service

import (
	"context"
	"strings"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	visionv1 "github.com/tiehu-ai/tiehu-fitness/api/vision/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type MeetingTranscriptionInternalService struct {
	visionv1.UnimplementedMeetingTranscriptionInternalServiceServer
	uc        *biz.TranscriptionUsecase
	summaryUC *biz.MeetingSummaryUsecase
}

func NewMeetingTranscriptionInternalService(uc *biz.TranscriptionUsecase, summaryUC ...*biz.MeetingSummaryUsecase) *MeetingTranscriptionInternalService {
	service := &MeetingTranscriptionInternalService{uc: uc}
	if len(summaryUC) > 0 {
		service.summaryUC = summaryUC[0]
	}
	return service
}

func (s *MeetingTranscriptionInternalService) PrepareMeetingSummary(ctx context.Context, req *visionv1.PrepareMeetingSummaryRequest) (*visionv1.PrepareMeetingSummaryResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	if s.summaryUC == nil {
		return nil, kratoserrors.ServiceUnavailable("SUMMARY_SERVICE_UNAVAILABLE", "meeting summary service is unavailable")
	}
	language, err := transcriptionLanguageFromProto(req.GetLanguage())
	if err != nil {
		return nil, err
	}
	job, err := s.summaryUC.Prepare(ctx, biz.PrepareMeetingSummaryInput{
		MeetingID: req.GetMeetingId(), UserID: req.GetUserId(), Version: req.GetVersion(),
		SourceTranscriptRevision: req.GetSourceTranscriptRevision(), Language: language,
		IdempotencyKey: req.GetIdempotencyKey(),
	})
	if err != nil {
		return nil, kratoserrors.BadRequest("SUMMARY_JOB_INVALID", "meeting summary job is invalid").WithCause(err)
	}
	return &visionv1.PrepareMeetingSummaryResponse{
		JobId: job.ID, Status: meetingSummaryJobStatusToProto(job.Status),
	}, nil
}

func meetingSummaryJobStatusToProto(status biz.MeetingSummaryJobStatus) meetingv1.MeetingSummaryStatus {
	switch status {
	case biz.MeetingSummaryJobStatusPending:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_PENDING
	case biz.MeetingSummaryJobStatusProcessing, biz.MeetingSummaryJobStatusDeliveryPending, biz.MeetingSummaryJobStatusFailureDeliveryPending:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_PROCESSING
	case biz.MeetingSummaryJobStatusSucceeded:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_SUCCEEDED
	case biz.MeetingSummaryJobStatusFailed:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_FAILED
	default:
		return meetingv1.MeetingSummaryStatus_MEETING_SUMMARY_STATUS_UNSPECIFIED
	}
}

func (s *MeetingTranscriptionInternalService) PrepareTranscription(ctx context.Context, req *visionv1.PrepareTranscriptionRequest) (*visionv1.PrepareTranscriptionResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	language, err := transcriptionLanguageFromProto(req.GetLanguage())
	if err != nil {
		return nil, err
	}
	grant := req.GetGrantedAudioDuration()
	if grant == nil || grant.CheckValid() != nil {
		return nil, kratoserrors.BadRequest("TRANSCRIPTION_GRANT_INVALID", "granted_audio_duration is invalid")
	}
	connection, err := s.uc.Prepare(ctx, biz.PrepareTranscriptionInput{
		MeetingID: req.GetMeetingId(), UserID: req.GetUserId(), ReservationID: req.GetReservationId(),
		Language: language, GrantedAudioDuration: grant.AsDuration(), IdempotencyKey: req.GetIdempotencyKey(),
	})
	if err != nil {
		return nil, err
	}
	mapped, err := transcriptionConnectionToProto(connection)
	if err != nil {
		return nil, err
	}
	return &visionv1.PrepareTranscriptionResponse{TranscriptionSession: mapped}, nil
}

func transcriptionConnectionToProto(connection *biz.TranscriptionConnection) (*meetingv1.TranscriptionSession, error) {
	if connection == nil || connection.Session == nil {
		return nil, kratoserrors.InternalServer("TRANSCRIPTION_CONNECTION_INVALID", "transcription connection is invalid")
	}
	return &meetingv1.TranscriptionSession{
		SessionId: connection.Session.ID, WebsocketUrl: connection.WebSocketURL,
		SessionTicket: connection.Ticket.Value, ExpiresAt: timestamppb.New(connection.Ticket.ExpiresAt),
		Audio: &meetingv1.AudioSpec{
			Format: meetingv1.AudioFormat_AUDIO_FORMAT_PCM_S16LE, MimeType: pcmMIMEType,
			SampleRate: connection.Audio.SampleRate, Channels: connection.Audio.Channels,
			ChunkDuration: durationpb.New(connection.Audio.ChunkDuration), MaxChunkBytes: connection.Audio.MaxChunkBytes,
		},
		GrantedAudioDuration: durationpb.New(connection.Session.GrantedAudioDuration.Duration()),
	}, nil
}

func (s *MeetingTranscriptionInternalService) CancelTranscription(ctx context.Context, req *visionv1.CancelTranscriptionRequest) (*visionv1.CancelTranscriptionResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	if err := validateCancelReason(req.GetReason()); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetIdempotencyKey()) == "" || len(req.GetIdempotencyKey()) > 128 {
		return nil, kratoserrors.BadRequest("IDEMPOTENCY_KEY_INVALID", "idempotency_key is invalid")
	}
	session, err := s.uc.Cancel(ctx, req.GetSessionId(), req.GetMeetingId())
	if err != nil {
		return nil, err
	}
	status, err := transcriptionStatusToProto(session.Status)
	if err != nil {
		return nil, err
	}
	return &visionv1.CancelTranscriptionResponse{Status: status}, nil
}

func (s *MeetingTranscriptionInternalService) GetTranscriptionStatus(ctx context.Context, req *visionv1.GetTranscriptionStatusRequest) (*visionv1.GetTranscriptionStatusResponse, error) {
	if req == nil {
		return nil, kratoserrors.BadRequest("REQUEST_REQUIRED", "request is required")
	}
	session, err := s.uc.Get(ctx, req.GetSessionId(), req.GetMeetingId())
	if err != nil {
		return nil, err
	}
	status, err := transcriptionStatusToProto(session.Status)
	if err != nil {
		return nil, err
	}
	accepted, err := session.AcceptedAudioDuration(s.uc.AudioSpec())
	if err != nil {
		return nil, kratoserrors.InternalServer("TRANSCRIPTION_USAGE_INVALID", "stored transcription usage is invalid").WithCause(err)
	}
	return &visionv1.GetTranscriptionStatusResponse{
		SessionId: session.ID, Status: status,
		GrantedAudioDuration:  durationpb.New(session.GrantedAudioDuration.Duration()),
		AcceptedAudioDuration: durationpb.New(accepted.Duration()), UpdatedAt: timestamppb.New(session.UpdatedAt),
	}, nil
}

func transcriptionLanguageFromProto(language meetingv1.MeetingLanguage) (biz.MeetingLanguage, error) {
	switch language {
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_AUTO:
		return biz.MeetingLanguageAuto, nil
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN:
		return biz.MeetingLanguageZhCN, nil
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_EN_US:
		return biz.MeetingLanguageEnUS, nil
	case meetingv1.MeetingLanguage_MEETING_LANGUAGE_UNSPECIFIED:
		return "", kratoserrors.BadRequest("TRANSCRIPTION_LANGUAGE_REQUIRED", "language is required")
	default:
		return "", kratoserrors.BadRequest("TRANSCRIPTION_LANGUAGE_INVALID", "language is invalid")
	}
}

func transcriptionStatusToProto(status biz.TranscriptionSessionStatus) (meetingv1.TranscriptionStatus, error) {
	switch status {
	case biz.TranscriptionSessionStatusPending:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_PENDING, nil
	case biz.TranscriptionSessionStatusConnecting:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_CONNECTING, nil
	case biz.TranscriptionSessionStatusStreaming:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_STREAMING, nil
	case biz.TranscriptionSessionStatusFinishing:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_FINISHING, nil
	case biz.TranscriptionSessionStatusSucceeded:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_SUCCEEDED, nil
	case biz.TranscriptionSessionStatusFailed:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_FAILED, nil
	case biz.TranscriptionSessionStatusCancelled:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_CANCELLED, nil
	case biz.TranscriptionSessionStatusExpired:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_EXPIRED, nil
	default:
		return meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_UNSPECIFIED,
			kratoserrors.InternalServer("TRANSCRIPTION_STATUS_CORRUPT", "stored transcription status is invalid")
	}
}

func validateCancelReason(reason visionv1.TranscriptionCancelReason) error {
	switch reason {
	case visionv1.TranscriptionCancelReason_TRANSCRIPTION_CANCEL_REASON_USER_CANCELLED,
		visionv1.TranscriptionCancelReason_TRANSCRIPTION_CANCEL_REASON_PREPARE_COMPENSATION,
		visionv1.TranscriptionCancelReason_TRANSCRIPTION_CANCEL_REASON_MEETING_DELETED,
		visionv1.TranscriptionCancelReason_TRANSCRIPTION_CANCEL_REASON_POLICY_ENFORCED:
		return nil
	case visionv1.TranscriptionCancelReason_TRANSCRIPTION_CANCEL_REASON_UNSPECIFIED:
		return kratoserrors.BadRequest("TRANSCRIPTION_CANCEL_REASON_REQUIRED", "cancel reason is required")
	default:
		return kratoserrors.BadRequest("TRANSCRIPTION_CANCEL_REASON_INVALID", "cancel reason is invalid")
	}
}
