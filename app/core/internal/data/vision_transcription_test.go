package data

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	visionv1 "github.com/tiehu-ai/tiehu-fitness/api/vision/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type visionTranscriptionFakeClient struct {
	prepareRequest *visionv1.PrepareTranscriptionRequest
	prepareReply   *visionv1.PrepareTranscriptionResponse
	prepareErr     error
	cancelRequest  *visionv1.CancelTranscriptionRequest
}

func (c *visionTranscriptionFakeClient) PrepareTranscription(_ context.Context, request *visionv1.PrepareTranscriptionRequest, _ ...grpc.CallOption) (*visionv1.PrepareTranscriptionResponse, error) {
	c.prepareRequest = request
	return c.prepareReply, c.prepareErr
}

func (c *visionTranscriptionFakeClient) CancelTranscription(_ context.Context, request *visionv1.CancelTranscriptionRequest, _ ...grpc.CallOption) (*visionv1.CancelTranscriptionResponse, error) {
	c.cancelRequest = request
	return &visionv1.CancelTranscriptionResponse{}, nil
}

func (*visionTranscriptionFakeClient) GetTranscriptionStatus(context.Context, *visionv1.GetTranscriptionStatusRequest, ...grpc.CallOption) (*visionv1.GetTranscriptionStatusResponse, error) {
	return nil, nil
}

func TestVisionTranscriptionGatewayMapsTypedRequestAndResponse(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Minute)
	client := &visionTranscriptionFakeClient{prepareReply: &visionv1.PrepareTranscriptionResponse{
		TranscriptionSession: &meetingv1.TranscriptionSession{
			SessionId: "session-id", WebsocketUrl: "wss://vision.example.test/v1/realtime/transcriptions",
			SessionTicket: "ticket", ExpiresAt: timestamppb.New(expiresAt),
			GrantedAudioDuration: durationpb.New(300 * time.Second),
			Audio: &meetingv1.AudioSpec{
				Format: meetingv1.AudioFormat_AUDIO_FORMAT_PCM_S16LE, MimeType: "audio/pcm;rate=16000",
				SampleRate: 16_000, Channels: 1, ChunkDuration: durationpb.New(100 * time.Millisecond), MaxChunkBytes: 6_400,
			},
		},
	}}
	gateway := &VisionTranscriptionGateway{client: client}
	input := biz.PrepareMeetingTranscriptionInput{
		MeetingID: "meeting-id", UserID: "user-id", ReservationID: "reservation-id",
		Language: biz.MeetingLanguageZhCN, GrantedSeconds: 300, IdempotencyKey: "prepare:meeting-id",
	}
	session, err := gateway.PrepareTranscription(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if client.prepareRequest == nil || client.prepareRequest.GetLanguage() != meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN ||
		client.prepareRequest.GetGrantedAudioDuration().AsDuration() != 300*time.Second {
		t.Fatalf("prepare request = %#v", client.prepareRequest)
	}
	if session == nil || session.ID != "session-id" || session.GrantedAudioSeconds != 300 ||
		session.Audio.SampleRate != 16_000 || session.Audio.ChunkDuration != 100*time.Millisecond {
		t.Fatalf("mapped session = %#v", session)
	}
	if err := gateway.CancelTranscription(context.Background(), biz.CancelMeetingTranscriptionInput{
		SessionID: "session-id", MeetingID: "meeting-id", IdempotencyKey: "compensate:meeting-id",
	}); err != nil {
		t.Fatal(err)
	}
	if client.cancelRequest == nil || client.cancelRequest.GetReason() != visionv1.TranscriptionCancelReason_TRANSCRIPTION_CANCEL_REASON_PREPARE_COMPENSATION {
		t.Fatalf("cancel request = %#v", client.cancelRequest)
	}
}

func TestVisionTranscriptionGatewayRejectsIncompleteResponseAndMapsErrors(t *testing.T) {
	gateway := &VisionTranscriptionGateway{client: &visionTranscriptionFakeClient{
		prepareReply: &visionv1.PrepareTranscriptionResponse{},
	}}
	_, err := gateway.PrepareTranscription(context.Background(), biz.PrepareMeetingTranscriptionInput{Language: biz.MeetingLanguageAuto})
	if kratoserrors.Reason(err) != "VISION_INVALID_RESPONSE" {
		t.Fatalf("incomplete response error = %v", err)
	}

	for _, tt := range []struct {
		err        error
		wantReason string
	}{
		{status.Error(codes.Unavailable, "unavailable"), "VISION_UNAVAILABLE"},
		{status.Error(codes.DeadlineExceeded, "timeout"), "VISION_TIMEOUT"},
		{status.Error(codes.InvalidArgument, "invalid"), "VISION_PREPARATION_REJECTED"},
	} {
		if got := mapVisionGatewayError(tt.err); kratoserrors.Reason(got) != tt.wantReason {
			t.Errorf("mapVisionGatewayError(%v) = %v", tt.err, got)
		}
	}
	if got := mapVisionGatewayError(context.Canceled); !stderrors.Is(got, context.Canceled) {
		t.Fatalf("context cancellation was not preserved: %v", got)
	}
}

func TestValidateVisionGRPCClientConfig(t *testing.T) {
	valid := &conf.VisionGRPCClient{
		Endpoint: "dns:///127.0.0.1:9100", RequestTimeout: durationpb.New(5 * time.Second), AllowInsecure: true,
	}
	if err := ValidateVisionGRPCClientConfig(valid); err != nil {
		t.Fatal(err)
	}
	invalid := &conf.VisionGRPCClient{
		Endpoint: valid.GetEndpoint(), RequestTimeout: valid.GetRequestTimeout(), AllowInsecure: false,
	}
	if err := ValidateVisionGRPCClientConfig(invalid); err == nil {
		t.Fatal("plaintext config without allow_insecure must fail")
	}
}
