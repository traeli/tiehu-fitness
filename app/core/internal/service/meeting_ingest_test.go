package service

import (
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestDurationSecondsCeil(t *testing.T) {
	tests := []struct {
		name       string
		value      *durationpb.Duration
		want       int64
		wantReason string
	}{
		{name: "zero", value: durationpb.New(0), want: 0},
		{name: "whole", value: durationpb.New(2 * time.Second), want: 2},
		{name: "partial", value: durationpb.New(time.Second + time.Nanosecond), want: 2},
		{name: "negative", value: durationpb.New(-time.Nanosecond), wantReason: "TRANSCRIPTION_USAGE_INVALID"},
		{name: "missing", wantReason: "TRANSCRIPTION_USAGE_INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := durationSecondsCeil(test.value, "test duration")
			if got != test.want || kratoserrors.Reason(err) != test.wantReason {
				t.Fatalf("durationSecondsCeil() = (%d, %v), want (%d, %q)", got, err, test.want, test.wantReason)
			}
		})
	}
}

func TestBillableAudioSecondsRoundsNonZeroAudioToMinutes(t *testing.T) {
	tests := []struct {
		name       string
		value      *durationpb.Duration
		want       int64
		wantReason string
	}{
		{name: "zero", value: durationpb.New(0), want: 0},
		{name: "under one minute", value: durationpb.New(10 * time.Second), want: 60},
		{name: "exact minute", value: durationpb.New(time.Minute), want: 60},
		{name: "over one minute", value: durationpb.New(time.Minute + time.Nanosecond), want: 120},
		{name: "missing", wantReason: "TRANSCRIPTION_USAGE_INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := billableAudioSeconds(test.value, "test duration")
			if got != test.want || kratoserrors.Reason(err) != test.wantReason {
				t.Fatalf("billableAudioSeconds() = (%d, %v), want (%d, %q)", got, err, test.want, test.wantReason)
			}
		})
	}
}

func TestFailureSettlementReasonMappingIsExplicit(t *testing.T) {
	tests := []struct {
		input      meetingv1.TranscriptionFailureReason
		want       biz.MeetingUsageSettlementReason
		wantReason string
	}{
		{meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_PROVIDER_UNAVAILABLE, biz.MeetingUsageSettlementReasonFailed, ""},
		{meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_QUOTA_EXHAUSTED, biz.MeetingUsageSettlementReasonQuotaExhausted, ""},
		{meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_CANCELLED, biz.MeetingUsageSettlementReasonCancelled, ""},
		{meetingv1.TranscriptionFailureReason_TRANSCRIPTION_FAILURE_REASON_UNSPECIFIED, biz.MeetingUsageSettlementReasonUnspecified, "TRANSCRIPTION_FAILURE_REASON_REQUIRED"},
		{meetingv1.TranscriptionFailureReason(99), biz.MeetingUsageSettlementReasonUnspecified, "TRANSCRIPTION_FAILURE_REASON_INVALID"},
	}
	for _, test := range tests {
		got, err := failureSettlementReason(test.input)
		if got != test.want || kratoserrors.Reason(err) != test.wantReason {
			t.Errorf("failureSettlementReason(%d) = (%d, %v), want (%d, %q)", test.input, got, err, test.want, test.wantReason)
		}
	}
}
