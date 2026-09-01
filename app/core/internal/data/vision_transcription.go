package data

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	visionv1 "github.com/tiehu-ai/tiehu-fitness/api/vision/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

const maxVisionGRPCRequestTimeout = 30 * time.Second

type VisionTranscriptionGateway struct {
	client        visionTranscriptionControlClient
	summaryClient visionMeetingSummaryClient
	conn          *grpc.ClientConn
}

type visionTranscriptionControlClient interface {
	PrepareTranscription(context.Context, *visionv1.PrepareTranscriptionRequest, ...grpc.CallOption) (*visionv1.PrepareTranscriptionResponse, error)
	CancelTranscription(context.Context, *visionv1.CancelTranscriptionRequest, ...grpc.CallOption) (*visionv1.CancelTranscriptionResponse, error)
}

type visionMeetingSummaryClient interface {
	PrepareMeetingSummary(context.Context, *visionv1.PrepareMeetingSummaryRequest, ...grpc.CallOption) (*visionv1.PrepareMeetingSummaryResponse, error)
}

var _ biz.VisionTranscriptionGateway = (*VisionTranscriptionGateway)(nil)

func NewVisionTranscriptionGateway(ctx context.Context, cfg *conf.VisionGRPCClient) (*VisionTranscriptionGateway, error) {
	if err := ValidateVisionGRPCClientConfig(cfg); err != nil {
		return nil, err
	}
	opts := []kratosgrpc.ClientOption{
		kratosgrpc.WithEndpoint(cfg.GetEndpoint()),
		kratosgrpc.WithTimeout(cfg.GetRequestTimeout().AsDuration()),
	}
	if cfg.GetTlsEnabled() {
		opts = append(opts, kratosgrpc.WithTLSConfig(&tls.Config{
			MinVersion: tls.VersionTLS12, ServerName: cfg.GetTlsServerName(),
		}))
	}
	conn, err := kratosgrpc.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create vision gRPC client: %w", err)
	}
	client := visionv1.NewMeetingTranscriptionInternalServiceClient(conn)
	return &VisionTranscriptionGateway{client: client, summaryClient: client, conn: conn}, nil
}

func ValidateVisionGRPCClientConfig(cfg *conf.VisionGRPCClient) error {
	if cfg == nil {
		return fmt.Errorf("vision gRPC client config is required")
	}
	endpoint := strings.TrimSpace(cfg.GetEndpoint())
	if endpoint == "" || strings.ContainsAny(endpoint, " \t\r\n") {
		return fmt.Errorf("vision gRPC endpoint is invalid")
	}
	if cfg.GetRequestTimeout() == nil {
		return fmt.Errorf("vision gRPC request timeout is required")
	}
	timeout := cfg.GetRequestTimeout().AsDuration()
	if timeout <= 0 || timeout > maxVisionGRPCRequestTimeout {
		return fmt.Errorf("vision gRPC request timeout must be between zero and %s", maxVisionGRPCRequestTimeout)
	}
	if !cfg.GetTlsEnabled() && !cfg.GetAllowInsecure() {
		return fmt.Errorf("vision gRPC plaintext requires allow_insecure")
	}
	if cfg.GetTlsEnabled() {
		serverName := strings.TrimSpace(cfg.GetTlsServerName())
		if serverName == "" || strings.ContainsAny(serverName, " \t\r\n/:") {
			return fmt.Errorf("vision gRPC TLS server name is invalid")
		}
	}
	return nil
}

func (g *VisionTranscriptionGateway) PrepareTranscription(ctx context.Context, input biz.PrepareMeetingTranscriptionInput) (*biz.MeetingTranscriptionSession, error) {
	language, err := toMeetingLanguageProto(input.Language)
	if err != nil {
		return nil, err
	}
	response, err := g.client.PrepareTranscription(ctx, &visionv1.PrepareTranscriptionRequest{
		MeetingId: input.MeetingID, UserId: input.UserID, ReservationId: input.ReservationID,
		Language: language, GrantedAudioDuration: durationpb.New(time.Duration(input.GrantedSeconds) * time.Second),
		IdempotencyKey: input.IdempotencyKey,
	})
	if err != nil {
		return nil, mapVisionGatewayError(err)
	}
	if response == nil || response.GetTranscriptionSession() == nil {
		return nil, kratoserrors.ServiceUnavailable("VISION_INVALID_RESPONSE", "vision returned an invalid response")
	}
	session := response.GetTranscriptionSession()
	audio := session.GetAudio()
	if audio == nil || session.GetExpiresAt() == nil || session.GetGrantedAudioDuration() == nil || session.GetAudio().GetChunkDuration() == nil {
		return nil, kratoserrors.ServiceUnavailable("VISION_INVALID_RESPONSE", "vision returned an incomplete transcription session")
	}
	if audio.GetFormat() != meetingv1.AudioFormat_AUDIO_FORMAT_PCM_S16LE {
		return nil, kratoserrors.ServiceUnavailable("VISION_INVALID_RESPONSE", "vision returned an unsupported audio format")
	}
	return &biz.MeetingTranscriptionSession{
		ID: session.GetSessionId(), WebSocketURL: session.GetWebsocketUrl(), Ticket: session.GetSessionTicket(),
		ExpiresAt: session.GetExpiresAt().AsTime(), GrantedAudioSeconds: int64(session.GetGrantedAudioDuration().AsDuration() / time.Second),
		Audio: biz.MeetingAudioSpec{
			MIMEType: audio.GetMimeType(), SampleRate: audio.GetSampleRate(), Channels: audio.GetChannels(),
			ChunkDuration: audio.GetChunkDuration().AsDuration(), MaxChunkBytes: audio.GetMaxChunkBytes(),
		},
	}, nil
}

func (g *VisionTranscriptionGateway) CancelTranscription(ctx context.Context, input biz.CancelMeetingTranscriptionInput) error {
	reason, err := meetingTranscriptionCancelReasonToProto(input.Reason)
	if err != nil {
		return err
	}
	_, err = g.client.CancelTranscription(ctx, &visionv1.CancelTranscriptionRequest{
		SessionId: input.SessionID, MeetingId: input.MeetingID,
		Reason:         reason,
		IdempotencyKey: input.IdempotencyKey,
	})
	if err != nil {
		return mapVisionGatewayError(err)
	}
	return nil
}

func meetingTranscriptionCancelReasonToProto(reason biz.MeetingTranscriptionCancelReason) (visionv1.TranscriptionCancelReason, error) {
	switch reason {
	case biz.MeetingTranscriptionCancelReasonUserCancelled:
		return visionv1.TranscriptionCancelReason_TRANSCRIPTION_CANCEL_REASON_USER_CANCELLED, nil
	case biz.MeetingTranscriptionCancelReasonPrepareCompensation:
		return visionv1.TranscriptionCancelReason_TRANSCRIPTION_CANCEL_REASON_PREPARE_COMPENSATION, nil
	default:
		return visionv1.TranscriptionCancelReason_TRANSCRIPTION_CANCEL_REASON_UNSPECIFIED, fmt.Errorf("meeting transcription cancel reason is invalid")
	}
}

func (g *VisionTranscriptionGateway) PrepareMeetingSummary(ctx context.Context, input biz.PrepareMeetingSummaryInput) error {
	if g.summaryClient == nil {
		return kratoserrors.ServiceUnavailable("SUMMARY_SERVICE_UNAVAILABLE", "vision meeting summary service is unavailable")
	}
	language, err := toMeetingLanguageProto(input.Language)
	if err != nil {
		return err
	}
	response, err := g.summaryClient.PrepareMeetingSummary(ctx, &visionv1.PrepareMeetingSummaryRequest{
		MeetingId: input.MeetingID, UserId: input.UserID, Version: input.Version,
		SourceTranscriptRevision: input.SourceTranscriptRevision, Language: language,
		IdempotencyKey: input.IdempotencyKey,
	})
	if err != nil {
		return mapVisionGatewayError(err)
	}
	if response == nil || strings.TrimSpace(response.GetJobId()) == "" {
		return kratoserrors.ServiceUnavailable("VISION_INVALID_RESPONSE", "vision returned an invalid summary job")
	}
	return nil
}

func (g *VisionTranscriptionGateway) Close() error {
	if g == nil || g.conn == nil {
		return nil
	}
	return g.conn.Close()
}

func toMeetingLanguageProto(language biz.MeetingLanguage) (meetingv1.MeetingLanguage, error) {
	switch language {
	case biz.MeetingLanguageAuto:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_AUTO, nil
	case biz.MeetingLanguageZhCN:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN, nil
	case biz.MeetingLanguageEnUS:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_EN_US, nil
	default:
		return meetingv1.MeetingLanguage_MEETING_LANGUAGE_UNSPECIFIED, kratoserrors.BadRequest("MEETING_LANGUAGE_INVALID", "meeting language is invalid")
	}
}

func mapVisionGatewayError(err error) error {
	if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch status.Code(err) {
	case codes.DeadlineExceeded:
		return kratoserrors.GatewayTimeout("VISION_TIMEOUT", "vision request timed out").WithCause(err)
	case codes.Unavailable:
		return kratoserrors.ServiceUnavailable("VISION_UNAVAILABLE", "vision service is unavailable").WithCause(err)
	case codes.InvalidArgument, codes.FailedPrecondition, codes.ResourceExhausted:
		return kratoserrors.ServiceUnavailable("VISION_PREPARATION_REJECTED", "vision rejected the transcription session").WithCause(err)
	default:
		return kratoserrors.ServiceUnavailable("VISION_REQUEST_FAILED", "vision request failed").WithCause(err)
	}
}
