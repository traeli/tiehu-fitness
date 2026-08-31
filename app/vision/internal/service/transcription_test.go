package service

import (
	"context"
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	visionv1 "github.com/tiehu-ai/tiehu-fitness/api/vision/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

func TestTranscriptionStatusToProto(t *testing.T) {
	tests := []struct {
		domain biz.TranscriptionSessionStatus
		proto  meetingv1.TranscriptionStatus
	}{
		{biz.TranscriptionSessionStatusPending, meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_PENDING},
		{biz.TranscriptionSessionStatusConnecting, meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_CONNECTING},
		{biz.TranscriptionSessionStatusStreaming, meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_STREAMING},
		{biz.TranscriptionSessionStatusFinishing, meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_FINISHING},
		{biz.TranscriptionSessionStatusSucceeded, meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_SUCCEEDED},
		{biz.TranscriptionSessionStatusFailed, meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_FAILED},
		{biz.TranscriptionSessionStatusCancelled, meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_CANCELLED},
		{biz.TranscriptionSessionStatusExpired, meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_EXPIRED},
	}
	for _, test := range tests {
		got, err := transcriptionStatusToProto(test.domain)
		if err != nil || got != test.proto {
			t.Fatalf("transcriptionStatusToProto(%s) = %s, %v", test.domain, got, err)
		}
	}
	if _, err := transcriptionStatusToProto("unknown"); kratoserrors.Reason(err) != "TRANSCRIPTION_STATUS_CORRUPT" {
		t.Fatalf("unknown status reason = %q", kratoserrors.Reason(err))
	}
}

func TestTranscriptionLanguageFromProto(t *testing.T) {
	if got, err := transcriptionLanguageFromProto(meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN); err != nil || got != biz.MeetingLanguageZhCN {
		t.Fatalf("transcriptionLanguageFromProto() = %q, %v", got, err)
	}
	if _, err := transcriptionLanguageFromProto(meetingv1.MeetingLanguage_MEETING_LANGUAGE_UNSPECIFIED); kratoserrors.Reason(err) != "TRANSCRIPTION_LANGUAGE_REQUIRED" {
		t.Fatalf("unspecified language reason = %q", kratoserrors.Reason(err))
	}
}

func TestMeetingTranscriptionInternalServiceRejectsNilAndInvalidCancel(t *testing.T) {
	svc := NewMeetingTranscriptionInternalService(nil)
	if _, err := svc.PrepareTranscription(context.Background(), nil); kratoserrors.Reason(err) != "REQUEST_REQUIRED" {
		t.Fatalf("PrepareTranscription(nil) reason = %q", kratoserrors.Reason(err))
	}
	if _, err := svc.CancelTranscription(context.Background(), &visionv1.CancelTranscriptionRequest{}); kratoserrors.Reason(err) != "TRANSCRIPTION_CANCEL_REASON_REQUIRED" {
		t.Fatalf("CancelTranscription() reason = %q", kratoserrors.Reason(err))
	}
	if _, err := svc.GetTranscriptionStatus(context.Background(), nil); kratoserrors.Reason(err) != "REQUEST_REQUIRED" {
		t.Fatalf("GetTranscriptionStatus(nil) reason = %q", kratoserrors.Reason(err))
	}
}

func TestTranscriptionConnectionToProtoUsesPublicPCMMIMEType(t *testing.T) {
	connection := &biz.TranscriptionConnection{
		Session:      &biz.TranscriptionSession{ID: "session-id", GrantedAudioDuration: biz.GrantedAudioDuration(time.Minute)},
		WebSocketURL: "wss://vision.example.test/v1/realtime/transcriptions",
		Ticket:       biz.TranscriptionTicket{Value: "ticket", ExpiresAt: time.Now().Add(time.Minute)},
		Audio: biz.AudioSpec{
			Format: biz.AudioFormatPCMS16LE, MIMEType: "audio/pcm", SampleRate: 16_000,
			Channels: 1, ChunkDuration: 200 * time.Millisecond, MaxChunkBytes: 6_400,
		},
	}
	mapped, err := transcriptionConnectionToProto(connection)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.GetAudio().GetMimeType() != "audio/pcm;rate=16000" {
		t.Fatalf("audio MIME type = %q", mapped.GetAudio().GetMimeType())
	}
}
