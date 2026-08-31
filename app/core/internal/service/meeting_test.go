package service

import (
	"context"
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
)

func TestMeetingLanguageProtoMappingIsExplicit(t *testing.T) {
	tests := []struct {
		input      meetingv1.MeetingLanguage
		want       biz.MeetingLanguage
		wantReason string
	}{
		{meetingv1.MeetingLanguage_MEETING_LANGUAGE_AUTO, biz.MeetingLanguageAuto, ""},
		{meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN, biz.MeetingLanguageZhCN, ""},
		{meetingv1.MeetingLanguage_MEETING_LANGUAGE_EN_US, biz.MeetingLanguageEnUS, ""},
		{meetingv1.MeetingLanguage_MEETING_LANGUAGE_UNSPECIFIED, biz.MeetingLanguageUnspecified, "MEETING_LANGUAGE_REQUIRED"},
		{meetingv1.MeetingLanguage(99), biz.MeetingLanguageUnspecified, "MEETING_LANGUAGE_INVALID"},
	}
	for _, tt := range tests {
		got, err := meetingLanguageFromProto(tt.input)
		if got != tt.want || kratoserrors.Reason(err) != tt.wantReason {
			t.Errorf("meetingLanguageFromProto(%d) = (%d, %v), want (%d, %q)", tt.input, got, err, tt.want, tt.wantReason)
		}
	}
}

func TestRequiredMeetingIdempotencyKey(t *testing.T) {
	ctx := transport.NewServerContext(context.Background(), &authTestTransport{
		header: authTestHeader{"Idempotency-Key": "idempotency-key"},
	})
	key, err := requiredIdempotencyKey(ctx)
	if err != nil || key != "idempotency-key" {
		t.Fatalf("requiredIdempotencyKey() = (%q, %v)", key, err)
	}
	if _, err := requiredIdempotencyKey(context.Background()); kratoserrors.Reason(err) != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("missing transport error = %v", err)
	}
}

func TestMeetingDomainToProtoMapping(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	stoppedAt := now.Add(time.Minute)
	meeting := &biz.Meeting{
		ID: "meeting-id", Status: biz.MeetingStatusProcessing,
		TranscriptionStatus: biz.MeetingTranscriptionStatusFinishing,
		Language:            biz.MeetingLanguageZhCN, RetainAudio: true, GrantedAudioSeconds: 300,
		StartedAt: now, StoppedAt: &stoppedAt, CreatedAt: now, UpdatedAt: stoppedAt,
	}
	got := toMeetingProto(meeting)
	if got == nil || got.GetMeetingId() != meeting.ID ||
		got.GetStatus() != meetingv1.MeetingStatus_MEETING_STATUS_PROCESSING ||
		got.GetTranscriptionStatus() != meetingv1.TranscriptionStatus_TRANSCRIPTION_STATUS_FINISHING ||
		got.GetLanguage() != meetingv1.MeetingLanguage_MEETING_LANGUAGE_ZH_CN ||
		got.GetGrantedAudioDuration().AsDuration() != 300*time.Second || got.GetStoppedAt() == nil {
		t.Fatalf("toMeetingProto() = %#v", got)
	}
	if toMeetingProto(nil) != nil {
		t.Fatal("toMeetingProto(nil) must return nil")
	}
}

func TestMeetingQuotaDomainToProtoMapping(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	quota := &biz.MeetingQuotaSnapshot{
		Period:                biz.MeetingBillingPeriod{Start: now, End: now.AddDate(0, 1, 0)},
		BaseLimitSeconds:      7_200,
		PurchasedLimitSeconds: 1_800,
		TotalLimitSeconds:     9_000,
		LimitSeconds:          9_000,
		ConsumedSeconds:       1_200,
		ReservedSeconds:       600,
		RemainingSeconds:      7_200,
		MaxMeetingSeconds:     14_400,
		MaxConcurrentMeetings: 1,
		ActiveMeetings:        1,
	}
	got := toMeetingQuotaProto(quota)
	if got == nil || got.GetBaseLimit().AsDuration() != 7_200*time.Second ||
		got.GetPurchasedLimit().AsDuration() != 1_800*time.Second ||
		got.GetTotalLimit().AsDuration() != 9_000*time.Second ||
		got.GetLimit().AsDuration() != got.GetTotalLimit().AsDuration() ||
		got.GetRemaining().AsDuration() != 7_200*time.Second {
		t.Fatalf("toMeetingQuotaProto() = %#v", got)
	}
}
