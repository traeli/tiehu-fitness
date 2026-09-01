package biz

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

// MeetingUsageReservationStatus is the lifecycle of reserved monthly quota.
type MeetingUsageReservationStatus uint8

const (
	MeetingUsageReservationStatusUnspecified MeetingUsageReservationStatus = iota
	MeetingUsageReservationStatusActive
	MeetingUsageReservationStatusSettled
	MeetingUsageReservationStatusReleased
	MeetingUsageReservationStatusExpired
)

func (s MeetingUsageReservationStatus) String() string {
	switch s {
	case MeetingUsageReservationStatusActive:
		return "active"
	case MeetingUsageReservationStatusSettled:
		return "settled"
	case MeetingUsageReservationStatusReleased:
		return "released"
	case MeetingUsageReservationStatusExpired:
		return "expired"
	default:
		return ""
	}
}

func ParseMeetingUsageReservationStatus(raw string) (MeetingUsageReservationStatus, error) {
	switch raw {
	case MeetingUsageReservationStatusActive.String():
		return MeetingUsageReservationStatusActive, nil
	case MeetingUsageReservationStatusSettled.String():
		return MeetingUsageReservationStatusSettled, nil
	case MeetingUsageReservationStatusReleased.String():
		return MeetingUsageReservationStatusReleased, nil
	case MeetingUsageReservationStatusExpired.String():
		return MeetingUsageReservationStatusExpired, nil
	default:
		return MeetingUsageReservationStatusUnspecified, fmt.Errorf("unknown meeting usage reservation status %q", raw)
	}
}

func (s MeetingUsageReservationStatus) IsTerminal() bool {
	return s == MeetingUsageReservationStatusSettled ||
		s == MeetingUsageReservationStatusReleased ||
		s == MeetingUsageReservationStatusExpired
}

// MeetingUsageSettlementReason classifies why reserved quota was finalized.
type MeetingUsageSettlementReason uint8

const (
	MeetingUsageSettlementReasonUnspecified MeetingUsageSettlementReason = iota
	MeetingUsageSettlementReasonCompleted
	MeetingUsageSettlementReasonQuotaExhausted
	MeetingUsageSettlementReasonCancelled
	MeetingUsageSettlementReasonFailed
	MeetingUsageSettlementReasonExpired
	MeetingUsageSettlementReasonPreparationFailed
)

func (r MeetingUsageSettlementReason) String() string {
	switch r {
	case MeetingUsageSettlementReasonCompleted:
		return "completed"
	case MeetingUsageSettlementReasonQuotaExhausted:
		return "quota_exhausted"
	case MeetingUsageSettlementReasonCancelled:
		return "cancelled"
	case MeetingUsageSettlementReasonFailed:
		return "failed"
	case MeetingUsageSettlementReasonExpired:
		return "expired"
	case MeetingUsageSettlementReasonPreparationFailed:
		return "preparation_failed"
	default:
		return ""
	}
}

func ParseMeetingUsageSettlementReason(raw string) (MeetingUsageSettlementReason, error) {
	switch raw {
	case MeetingUsageSettlementReasonCompleted.String():
		return MeetingUsageSettlementReasonCompleted, nil
	case MeetingUsageSettlementReasonQuotaExhausted.String():
		return MeetingUsageSettlementReasonQuotaExhausted, nil
	case MeetingUsageSettlementReasonCancelled.String():
		return MeetingUsageSettlementReasonCancelled, nil
	case MeetingUsageSettlementReasonFailed.String():
		return MeetingUsageSettlementReasonFailed, nil
	case MeetingUsageSettlementReasonExpired.String():
		return MeetingUsageSettlementReasonExpired, nil
	case MeetingUsageSettlementReasonPreparationFailed.String():
		return MeetingUsageSettlementReasonPreparationFailed, nil
	default:
		return MeetingUsageSettlementReasonUnspecified, fmt.Errorf("unknown meeting usage settlement reason %q", raw)
	}
}

// MeetingUsageKind separates customer quota from future billable dimensions.
type MeetingUsageKind uint8

const (
	MeetingUsageKindUnspecified MeetingUsageKind = iota
	MeetingUsageKindASRAudio
)

func (k MeetingUsageKind) String() string {
	if k == MeetingUsageKindASRAudio {
		return "asr_audio"
	}
	return ""
}

func ParseMeetingUsageKind(raw string) (MeetingUsageKind, error) {
	if raw == MeetingUsageKindASRAudio.String() {
		return MeetingUsageKindASRAudio, nil
	}
	return MeetingUsageKindUnspecified, fmt.Errorf("unknown meeting usage kind %q", raw)
}

// RedisQuotaFailurePolicy controls meeting creation when Redis rate limiting is unavailable.
type RedisQuotaFailurePolicy uint8

const (
	RedisQuotaFailurePolicyUnspecified RedisQuotaFailurePolicy = iota
	RedisQuotaFailurePolicyDeny
	RedisQuotaFailurePolicyPostgresFallback
)

func (p RedisQuotaFailurePolicy) String() string {
	switch p {
	case RedisQuotaFailurePolicyDeny:
		return "deny"
	case RedisQuotaFailurePolicyPostgresFallback:
		return "postgres_fallback"
	default:
		return ""
	}
}

func ParseRedisQuotaFailurePolicy(raw string) (RedisQuotaFailurePolicy, error) {
	switch raw {
	case RedisQuotaFailurePolicyDeny.String():
		return RedisQuotaFailurePolicyDeny, nil
	case RedisQuotaFailurePolicyPostgresFallback.String():
		return RedisQuotaFailurePolicyPostgresFallback, nil
	default:
		return RedisQuotaFailurePolicyUnspecified, fmt.Errorf("unknown meeting quota Redis failure policy %q", raw)
	}
}

// MeetingQuotaPolicy is the validated effective policy for one user.
type MeetingQuotaPolicy struct {
	MonthlyAudioSeconds    int64
	MaxMeetingAudioSeconds int64
	MaxConcurrentMeetings  int32
	CreateRateLimit        int32
	CreateRateWindow       time.Duration
	UsageReportInterval    time.Duration
	ReservationTTL         time.Duration
	PeriodLocation         *time.Location
	RedisFailurePolicy     RedisQuotaFailurePolicy
}

// MeetingBillingPeriod is a half-open monthly interval stored in UTC.
type MeetingBillingPeriod struct {
	Start time.Time
	End   time.Time
}

type MeetingUsageReservation struct {
	ID              string
	UserID          string
	MeetingID       string
	Period          MeetingBillingPeriod
	GrantedSeconds  int64
	ReportedSeconds int64
	Status          MeetingUsageReservationStatus
	ExpiresAt       time.Time
	FinalizedAt     *time.Time
}

type MeetingUsageRecord struct {
	ID                   string
	ReservationID        string
	UserID               string
	MeetingID            string
	Period               MeetingBillingPeriod
	Kind                 MeetingUsageKind
	ActualSeconds        int64
	ProviderUsageSeconds int64
	Reason               MeetingUsageSettlementReason
	SettledAt            time.Time
}

type MeetingQuotaSnapshot struct {
	Period                MeetingBillingPeriod
	BaseLimitSeconds      int64
	PurchasedLimitSeconds int64
	TotalLimitSeconds     int64
	// LimitSeconds is the compatibility alias for TotalLimitSeconds.
	LimitSeconds          int64
	ConsumedSeconds       int64
	ReservedSeconds       int64
	RemainingSeconds      int64
	MaxMeetingSeconds     int64
	MaxConcurrentMeetings int32
	ActiveMeetings        int32
}

type MeetingQuotaReservationResult struct {
	Reservation *MeetingUsageReservation
	Quota       *MeetingQuotaSnapshot
	Existing    bool
}

type MeetingUsageFinalizeCommand struct {
	ReservationID        string
	MeetingID            string
	TotalAcceptedSeconds int64
	ProviderUsageSeconds int64
	Reason               MeetingUsageSettlementReason
	FinalizedAt          time.Time
}

type MeetingQuotaReserveInput struct {
	ReservationID string
	UserID        string
	MeetingID     string
	Period        MeetingBillingPeriod
	Policy        MeetingQuotaPolicy
	Now           time.Time
	ExpiresAt     time.Time
}

type MeetingQuotaFinalizeInput struct {
	MeetingUsageFinalizeCommand
	Kind MeetingUsageKind
}

type MeetingCreateRateDecision struct {
	Allowed    bool
	RetryAfter time.Duration
}

var (
	ErrMeetingQuotaReservationNotFound = stderrors.New("meeting quota reservation not found")
	ErrMeetingQuotaExceeded            = stderrors.New("meeting quota exceeded")
)

type MeetingQuotaRepo interface {
	ReportUsage(context.Context, string, string, int64, time.Time) (*MeetingUsageReservation, error)
	Finalize(context.Context, MeetingQuotaFinalizeInput) (*MeetingUsageRecord, error)
	GetSnapshot(context.Context, string, MeetingBillingPeriod, MeetingQuotaPolicy, time.Time) (*MeetingQuotaSnapshot, error)
	ListExpiredReservations(context.Context, time.Time, int) ([]*MeetingUsageReservation, error)
}

// MeetingQuotaPolicyProvider owns the current default policy. The PostgreSQL
// adapter is intentionally queried for each quota decision so operator updates
// take effect on the next request without a process restart or stale cache.
type MeetingQuotaPolicyProvider interface {
	GetDefaultPolicy(context.Context) (MeetingQuotaPolicy, error)
}

type MeetingCreateRateLimiter interface {
	Allow(context.Context, string, time.Time, int32, time.Duration) (MeetingCreateRateDecision, error)
}

type MeetingQuotaUsecase struct {
	policies    MeetingQuotaPolicyProvider
	repo        MeetingQuotaRepo
	rateLimiter MeetingCreateRateLimiter
}

func NewMeetingQuotaUsecase(policies MeetingQuotaPolicyProvider, repo MeetingQuotaRepo, limiter MeetingCreateRateLimiter) (*MeetingQuotaUsecase, error) {
	if policies == nil {
		return nil, fmt.Errorf("meeting quota policy provider is required")
	}
	if repo == nil {
		return nil, fmt.Errorf("meeting quota repository is required")
	}
	if limiter == nil {
		return nil, fmt.Errorf("meeting create rate limiter is required")
	}
	return &MeetingQuotaUsecase{policies: policies, repo: repo, rateLimiter: limiter}, nil
}

// AuthorizeReservation performs boundary, rate-limit and policy checks without
// writing PostgreSQL. Meeting creation uses the returned input in its own
// transaction so the meeting and reservation commit atomically.
func (uc *MeetingQuotaUsecase) AuthorizeReservation(ctx context.Context, userID, meetingID string, now time.Time) (MeetingQuotaReserveInput, error) {
	if err := validateQuotaIdentifiers(userID, meetingID); err != nil {
		return MeetingQuotaReserveInput{}, err
	}
	now = normalizedQuotaTime(now)
	defaults, err := uc.policies.GetDefaultPolicy(ctx)
	if err != nil {
		return MeetingQuotaReserveInput{}, kratoserrors.ServiceUnavailable("MEETING_QUOTA_POLICY_UNAVAILABLE", "meeting quota policy is temporarily unavailable").WithCause(err)
	}
	decision, rateErr := uc.rateLimiter.Allow(ctx, userID, now, defaults.CreateRateLimit, defaults.CreateRateWindow)
	if rateErr != nil && defaults.RedisFailurePolicy == RedisQuotaFailurePolicyDeny {
		return MeetingQuotaReserveInput{}, kratoserrors.ServiceUnavailable("MEETING_RATE_LIMIT_UNAVAILABLE", "meeting creation is temporarily unavailable").WithCause(rateErr)
	}
	if rateErr == nil && !decision.Allowed {
		retrySeconds := int64(decision.RetryAfter / time.Second)
		if decision.RetryAfter%time.Second != 0 {
			retrySeconds++
		}
		return MeetingQuotaReserveInput{}, kratoserrors.TooManyRequests("MEETING_RATE_LIMITED", "meeting creation rate limit reached").WithMetadata(map[string]string{
			"retry_after_seconds": strconv.FormatInt(maxInt64(retrySeconds, 1), 10),
		})
	}

	policy := defaults
	return MeetingQuotaReserveInput{
		ReservationID: uuid.NewString(), UserID: userID, MeetingID: meetingID, Period: policy.PeriodAt(now), Policy: policy,
		Now: now, ExpiresAt: now.Add(policy.ReservationTTL),
	}, nil
}

func (uc *MeetingQuotaUsecase) ReportUsage(ctx context.Context, reservationID, meetingID string, totalSeconds int64, observedAt time.Time) (*MeetingUsageReservation, error) {
	if err := validateReservationCommand(reservationID, meetingID, totalSeconds); err != nil {
		return nil, err
	}
	reservation, err := uc.repo.ReportUsage(ctx, reservationID, meetingID, totalSeconds, normalizedQuotaTime(observedAt))
	if stderrors.Is(err, ErrMeetingQuotaReservationNotFound) {
		return nil, kratoserrors.NotFound("MEETING_QUOTA_RESERVATION_NOT_FOUND", "meeting quota reservation not found")
	}
	return reservation, err
}

func (uc *MeetingQuotaUsecase) Finalize(ctx context.Context, command MeetingUsageFinalizeCommand) (*MeetingUsageRecord, error) {
	if err := validateReservationCommand(command.ReservationID, command.MeetingID, command.TotalAcceptedSeconds); err != nil {
		return nil, err
	}
	if command.ProviderUsageSeconds < 0 {
		return nil, kratoserrors.BadRequest("PROVIDER_USAGE_INVALID", "provider usage must not be negative")
	}
	if command.Reason == MeetingUsageSettlementReasonUnspecified || command.Reason.String() == "" {
		return nil, kratoserrors.BadRequest("SETTLEMENT_REASON_INVALID", "settlement reason is invalid")
	}
	command.FinalizedAt = normalizedQuotaTime(command.FinalizedAt)
	record, err := uc.repo.Finalize(ctx, MeetingQuotaFinalizeInput{
		MeetingUsageFinalizeCommand: command, Kind: MeetingUsageKindASRAudio,
	})
	if stderrors.Is(err, ErrMeetingQuotaReservationNotFound) {
		return nil, kratoserrors.NotFound("MEETING_QUOTA_RESERVATION_NOT_FOUND", "meeting quota reservation not found")
	}
	return record, err
}

func (uc *MeetingQuotaUsecase) ReleasePreparationFailure(ctx context.Context, reservationID, meetingID string, at time.Time) (*MeetingUsageRecord, error) {
	return uc.Finalize(ctx, MeetingUsageFinalizeCommand{
		ReservationID: reservationID, MeetingID: meetingID,
		Reason: MeetingUsageSettlementReasonPreparationFailed, FinalizedAt: at,
	})
}

func (uc *MeetingQuotaUsecase) GetQuota(ctx context.Context, userID string, now time.Time) (*MeetingQuotaSnapshot, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, kratoserrors.BadRequest("USER_ID_INVALID", "user ID must be a UUID")
	}
	policy, err := uc.effectivePolicy(ctx)
	if err != nil {
		return nil, err
	}
	now = normalizedQuotaTime(now)
	return uc.repo.GetSnapshot(ctx, userID, policy.PeriodAt(now), policy, now)
}

// ReconcileExpired finalizes a bounded batch independently of the current
// billing period. Concurrent reconcilers are safe because Finalize is idempotent.
func (uc *MeetingQuotaUsecase) ReconcileExpired(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 1_000 {
		return 0, kratoserrors.BadRequest("RECONCILIATION_LIMIT_INVALID", "reconciliation limit must be between 1 and 1000")
	}
	now = normalizedQuotaTime(now)
	reservations, err := uc.repo.ListExpiredReservations(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, reservation := range reservations {
		if reservation == nil {
			return completed, kratoserrors.InternalServer("QUOTA_RESERVATION_INVALID", "quota reservation data is invalid")
		}
		if _, err := uc.Finalize(ctx, MeetingUsageFinalizeCommand{
			ReservationID: reservation.ID, MeetingID: reservation.MeetingID,
			TotalAcceptedSeconds: reservation.ReportedSeconds,
			Reason:               MeetingUsageSettlementReasonExpired, FinalizedAt: now,
		}); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}

func (uc *MeetingQuotaUsecase) effectivePolicy(ctx context.Context) (MeetingQuotaPolicy, error) {
	defaults, err := uc.policies.GetDefaultPolicy(ctx)
	if err != nil {
		return MeetingQuotaPolicy{}, kratoserrors.ServiceUnavailable("MEETING_QUOTA_POLICY_UNAVAILABLE", "meeting quota policy is temporarily unavailable").WithCause(err)
	}
	return defaults, nil
}

func (p MeetingQuotaPolicy) PeriodAt(at time.Time) MeetingBillingPeriod {
	local := at.In(p.PeriodLocation)
	startLocal := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, p.PeriodLocation)
	return MeetingBillingPeriod{Start: startLocal.UTC(), End: startLocal.AddDate(0, 1, 0).UTC()}
}

// RoundMeetingAudioUsage converts accepted audio duration to billable whole
// seconds. A non-zero partial second is charged as one second.
func RoundMeetingAudioUsage(duration time.Duration) (int64, error) {
	if duration < 0 {
		return 0, kratoserrors.BadRequest("AUDIO_USAGE_INVALID", "audio usage must not be negative")
	}
	seconds := int64(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	return seconds, nil
}

func validateMeetingQuotaPolicy(policy MeetingQuotaPolicy) error {
	if policy.MonthlyAudioSeconds <= 0 || policy.MonthlyAudioSeconds > maxConfiguredMonthlyAudio {
		return fmt.Errorf("monthly audio seconds is out of range")
	}
	if policy.MaxMeetingAudioSeconds <= 0 || policy.MaxMeetingAudioSeconds > maxConfiguredMeetingAudio {
		return fmt.Errorf("maximum meeting audio seconds is out of range")
	}
	if policy.MaxConcurrentMeetings <= 0 || policy.MaxConcurrentMeetings > maxConfiguredConcurrency {
		return fmt.Errorf("maximum concurrent meetings is out of range")
	}
	if policy.CreateRateLimit <= 0 || policy.CreateRateLimit > maxConfiguredCreateRate {
		return fmt.Errorf("meeting create rate limit is out of range")
	}
	if policy.CreateRateWindow <= 0 || policy.CreateRateWindow > 24*time.Hour {
		return fmt.Errorf("meeting create rate window is out of range")
	}
	if policy.UsageReportInterval <= 0 || policy.UsageReportInterval > time.Hour ||
		policy.ReservationTTL <= policy.UsageReportInterval || policy.ReservationTTL > 25*time.Hour {
		return fmt.Errorf("meeting usage and reservation durations are invalid")
	}
	if policy.ReservationTTL < time.Duration(policy.MaxMeetingAudioSeconds)*time.Second {
		return fmt.Errorf("reservation TTL must cover maximum meeting duration")
	}
	if policy.PeriodLocation == nil || policy.PeriodLocation.String() != "Asia/Shanghai" {
		return fmt.Errorf("meeting quota period location must be Asia/Shanghai")
	}
	if policy.RedisFailurePolicy != RedisQuotaFailurePolicyDeny && policy.RedisFailurePolicy != RedisQuotaFailurePolicyPostgresFallback {
		return fmt.Errorf("meeting quota Redis failure policy is invalid")
	}
	return nil
}

func validateQuotaIdentifiers(userID, meetingID string) error {
	if _, err := uuid.Parse(strings.TrimSpace(userID)); err != nil {
		return kratoserrors.BadRequest("USER_ID_INVALID", "user ID must be a UUID")
	}
	if _, err := uuid.Parse(strings.TrimSpace(meetingID)); err != nil {
		return kratoserrors.BadRequest("MEETING_ID_INVALID", "meeting ID must be a UUID")
	}
	return nil
}

func validateReservationCommand(reservationID, meetingID string, totalSeconds int64) error {
	if _, err := uuid.Parse(strings.TrimSpace(reservationID)); err != nil {
		return kratoserrors.BadRequest("RESERVATION_ID_INVALID", "reservation ID must be a UUID")
	}
	if _, err := uuid.Parse(strings.TrimSpace(meetingID)); err != nil {
		return kratoserrors.BadRequest("MEETING_ID_INVALID", "meeting ID must be a UUID")
	}
	if totalSeconds < 0 {
		return kratoserrors.BadRequest("AUDIO_USAGE_INVALID", "audio usage must not be negative")
	}
	return nil
}

func normalizedQuotaTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
