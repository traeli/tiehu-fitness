package biz

import (
	"testing"
	"time"
)

func TestNewMeetingQuotaPolicyValidatesStoredInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MeetingQuotaPolicyInput)
	}{
		{name: "valid"},
		{name: "zero monthly quota", mutate: func(c *MeetingQuotaPolicyInput) { c.MonthlyAudioSeconds = 0 }},
		{name: "meeting too long", mutate: func(c *MeetingQuotaPolicyInput) { c.MaxMeetingAudioSeconds = maxConfiguredMeetingAudio + 1 }},
		{name: "zero concurrency", mutate: func(c *MeetingQuotaPolicyInput) { c.MaxConcurrentMeetings = 0 }},
		{name: "missing rate window", mutate: func(c *MeetingQuotaPolicyInput) { c.CreateRateWindow = 0 }},
		{name: "wrong timezone", mutate: func(c *MeetingQuotaPolicyInput) { c.PeriodTimezone = "UTC" }},
		{name: "report interval exceeds ttl", mutate: func(c *MeetingQuotaPolicyInput) {
			c.UsageReportInterval = 5 * time.Hour
			c.ReservationTTL = 4 * time.Hour
		}},
		{name: "report interval exceeds upper bound", mutate: func(c *MeetingQuotaPolicyInput) { c.UsageReportInterval = 2 * time.Hour }},
		{name: "reservation ttl exceeds upper bound", mutate: func(c *MeetingQuotaPolicyInput) { c.ReservationTTL = 26 * time.Hour }},
		{name: "reservation shorter than meeting", mutate: func(c *MeetingQuotaPolicyInput) { c.ReservationTTL = 3 * time.Hour }},
		{name: "missing redis failure policy", mutate: func(c *MeetingQuotaPolicyInput) {
			c.RedisFailurePolicy = RedisQuotaFailurePolicyUnspecified
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validMeetingQuotaConfig()
			if tt.mutate != nil {
				tt.mutate(&input)
			}
			_, err := NewMeetingQuotaPolicy(input)
			if tt.name == "valid" && err != nil {
				t.Fatalf("NewMeetingQuotaPolicy() error = %v", err)
			}
			if tt.name != "valid" && err == nil {
				t.Fatal("NewMeetingQuotaPolicy() expected error")
			}
		})
	}
}

func validMeetingQuotaConfig() MeetingQuotaPolicyInput {
	return MeetingQuotaPolicyInput{
		MonthlyAudioSeconds:    7_200,
		MaxMeetingAudioSeconds: 14_400,
		MaxConcurrentMeetings:  1,
		CreateRateLimit:        5,
		CreateRateWindow:       10 * time.Minute,
		PeriodTimezone:         "Asia/Shanghai",
		UsageReportInterval:    30 * time.Second,
		ReservationTTL:         4*time.Hour + 30*time.Second,
		RedisFailurePolicy:     RedisQuotaFailurePolicyDeny,
	}
}
