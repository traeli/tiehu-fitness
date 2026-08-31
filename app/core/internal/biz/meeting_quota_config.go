package biz

import (
	"fmt"
	"time"
)

const (
	maxConfiguredMonthlyAudio = int64(366 * 24 * 60 * 60)
	maxConfiguredMeetingAudio = int64(24 * 60 * 60)
	maxConfiguredConcurrency  = int32(1_000)
	maxConfiguredCreateRate   = int32(10_000)
)

// MeetingQuotaPolicyInput is the persistence-neutral input used to construct
// the default quota policy loaded from PostgreSQL. Keeping validation in biz
// prevents invalid stored values from entering meeting authorization.
type MeetingQuotaPolicyInput struct {
	MonthlyAudioSeconds    int64
	MaxMeetingAudioSeconds int64
	MaxConcurrentMeetings  int32
	CreateRateLimit        int32
	CreateRateWindow       time.Duration
	PeriodTimezone         string
	UsageReportInterval    time.Duration
	ReservationTTL         time.Duration
	RedisFailurePolicy     RedisQuotaFailurePolicy
}

func NewMeetingQuotaPolicy(input MeetingQuotaPolicyInput) (MeetingQuotaPolicy, error) {
	location, err := time.LoadLocation(input.PeriodTimezone)
	if err != nil {
		return MeetingQuotaPolicy{}, fmt.Errorf("load meeting quota period timezone: %w", err)
	}
	policy := MeetingQuotaPolicy{
		MonthlyAudioSeconds: input.MonthlyAudioSeconds, MaxMeetingAudioSeconds: input.MaxMeetingAudioSeconds,
		MaxConcurrentMeetings: input.MaxConcurrentMeetings, CreateRateLimit: input.CreateRateLimit,
		CreateRateWindow: input.CreateRateWindow, UsageReportInterval: input.UsageReportInterval,
		ReservationTTL: input.ReservationTTL, PeriodLocation: location, RedisFailurePolicy: input.RedisFailurePolicy,
	}
	if err := validateMeetingQuotaPolicy(policy); err != nil {
		return MeetingQuotaPolicy{}, err
	}
	return policy, nil
}
