package biz

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

type quotaFakeRepo struct {
	defaultPolicy  MeetingQuotaPolicy
	policyErr      error
	reservation    *MeetingUsageReservation
	snapshot       *MeetingQuotaSnapshot
	reportErr      error
	finalizeErr    error
	finalizeInput  MeetingQuotaFinalizeInput
	snapshotPolicy MeetingQuotaPolicy
}

func (r *quotaFakeRepo) GetDefaultPolicy(context.Context) (MeetingQuotaPolicy, error) {
	if r.policyErr != nil {
		return MeetingQuotaPolicy{}, r.policyErr
	}
	return r.defaultPolicy, nil
}

func (r *quotaFakeRepo) ReportUsage(_ context.Context, _, _ string, total int64, _ time.Time) (*MeetingUsageReservation, error) {
	if r.reportErr != nil {
		return nil, r.reportErr
	}
	r.reservation.ReportedSeconds = total
	return r.reservation, nil
}

func (r *quotaFakeRepo) Finalize(_ context.Context, input MeetingQuotaFinalizeInput) (*MeetingUsageRecord, error) {
	r.finalizeInput = input
	if r.finalizeErr != nil {
		return nil, r.finalizeErr
	}
	return &MeetingUsageRecord{
		ID: uuid.NewString(), ReservationID: input.ReservationID, MeetingID: input.MeetingID,
		ActualSeconds: input.TotalAcceptedSeconds, ProviderUsageSeconds: input.ProviderUsageSeconds,
		Reason: input.Reason, Kind: input.Kind, SettledAt: input.FinalizedAt,
	}, nil
}

func (r *quotaFakeRepo) GetSnapshot(_ context.Context, _ string, _ MeetingBillingPeriod, policy MeetingQuotaPolicy, _ time.Time) (*MeetingQuotaSnapshot, error) {
	r.snapshotPolicy = policy
	if r.snapshot == nil {
		return &MeetingQuotaSnapshot{}, nil
	}
	return r.snapshot, nil
}

func TestMeetingQuotaGetQuotaUsesDefaultPolicyWhenOverrideIsMissing(t *testing.T) {
	policy, err := NewMeetingQuotaPolicy(validMeetingQuotaConfig())
	if err != nil {
		t.Fatal(err)
	}
	repo := &quotaFakeRepo{defaultPolicy: policy}
	uc, err := NewMeetingQuotaUsecase(repo, repo, &quotaFakeRateLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.GetQuota(context.Background(), uuid.NewString(), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if repo.snapshotPolicy != policy {
		t.Fatalf("snapshot policy = %#v, want default %#v", repo.snapshotPolicy, policy)
	}
}

func (r *quotaFakeRepo) ListExpiredReservations(context.Context, time.Time, int) ([]*MeetingUsageReservation, error) {
	if r.reservation == nil {
		return nil, nil
	}
	return []*MeetingUsageReservation{r.reservation}, nil
}

type quotaFakeRateLimiter struct {
	decision MeetingCreateRateDecision
	err      error
	calls    int
}

func (l *quotaFakeRateLimiter) Allow(context.Context, string, time.Time, int32, time.Duration) (MeetingCreateRateDecision, error) {
	l.calls++
	return l.decision, l.err
}

func TestMeetingQuotaPolicyUsesShanghaiNaturalMonth(t *testing.T) {
	policy, err := NewMeetingQuotaPolicy(validMeetingQuotaConfig())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		at        time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "last second of August in Shanghai",
			at:        time.Date(2026, 8, 31, 15, 59, 59, 0, time.UTC),
			wantStart: time.Date(2026, 7, 31, 16, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC),
		},
		{
			name:      "first second of September in Shanghai",
			at:        time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC),
			wantStart: time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			period := policy.PeriodAt(tt.at)
			if !period.Start.Equal(tt.wantStart) || !period.End.Equal(tt.wantEnd) {
				t.Fatalf("PeriodAt() = [%s, %s), want [%s, %s)", period.Start, period.End, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestMeetingQuotaAuthorizationUsesDefaultPolicy(t *testing.T) {
	policy, err := NewMeetingQuotaPolicy(validMeetingQuotaConfig())
	if err != nil {
		t.Fatal(err)
	}
	repo := &quotaFakeRepo{defaultPolicy: policy}
	limiter := &quotaFakeRateLimiter{decision: MeetingCreateRateDecision{Allowed: true}}
	uc, err := NewMeetingQuotaUsecase(repo, repo, limiter)
	if err != nil {
		t.Fatal(err)
	}
	userID, meetingID := uuid.NewString(), uuid.NewString()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	first, err := uc.AuthorizeReservation(context.Background(), userID, meetingID, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Policy != policy || limiter.calls != 1 {
		t.Fatalf("AuthorizeReservation() = %#v, rate limiter calls = %d", first, limiter.calls)
	}
}

func TestMeetingQuotaReserveEnforcesRateAndRedisFailurePolicy(t *testing.T) {
	base, err := NewMeetingQuotaPolicy(validMeetingQuotaConfig())
	if err != nil {
		t.Fatal(err)
	}
	userID, meetingID := uuid.NewString(), uuid.NewString()
	t.Run("rate limited", func(t *testing.T) {
		repo := &quotaFakeRepo{defaultPolicy: base}
		uc, newErr := NewMeetingQuotaUsecase(repo, repo, &quotaFakeRateLimiter{
			decision: MeetingCreateRateDecision{RetryAfter: 1500 * time.Millisecond},
		})
		if newErr != nil {
			t.Fatal(newErr)
		}
		_, reserveErr := uc.AuthorizeReservation(context.Background(), userID, meetingID, time.Now())
		if kratoserrors.Reason(reserveErr) != "MEETING_RATE_LIMITED" {
			t.Fatalf("Reserve() error = %v", reserveErr)
		}
		var statusErr *kratoserrors.Error
		if !stderrors.As(reserveErr, &statusErr) || statusErr.Metadata["retry_after_seconds"] != "2" {
			t.Fatalf("rate metadata = %#v", statusErr)
		}
	})
	t.Run("deny Redis failure", func(t *testing.T) {
		repo := &quotaFakeRepo{defaultPolicy: base}
		uc, newErr := NewMeetingQuotaUsecase(repo, repo, &quotaFakeRateLimiter{err: stderrors.New("redis unavailable")})
		if newErr != nil {
			t.Fatal(newErr)
		}
		_, reserveErr := uc.AuthorizeReservation(context.Background(), userID, meetingID, time.Now())
		if kratoserrors.Reason(reserveErr) != "MEETING_RATE_LIMIT_UNAVAILABLE" {
			t.Fatalf("Reserve() error = %v", reserveErr)
		}
	})
	t.Run("PostgreSQL fallback", func(t *testing.T) {
		fallback := base
		fallback.RedisFailurePolicy = RedisQuotaFailurePolicyPostgresFallback
		repo := &quotaFakeRepo{defaultPolicy: fallback}
		uc, newErr := NewMeetingQuotaUsecase(repo, repo, &quotaFakeRateLimiter{err: stderrors.New("redis unavailable")})
		if newErr != nil {
			t.Fatal(newErr)
		}
		if _, reserveErr := uc.AuthorizeReservation(context.Background(), userID, meetingID, time.Now()); reserveErr != nil {
			t.Fatalf("AuthorizeReservation() error = %v", reserveErr)
		}
	})
}

func TestMeetingQuotaPolicyChangeAppliesToNextAuthorization(t *testing.T) {
	initial, err := NewMeetingQuotaPolicy(validMeetingQuotaConfig())
	if err != nil {
		t.Fatal(err)
	}
	repo := &quotaFakeRepo{defaultPolicy: initial}
	limiter := &quotaFakeRateLimiter{decision: MeetingCreateRateDecision{Allowed: true}}
	uc, err := NewMeetingQuotaUsecase(repo, repo, limiter)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	first, err := uc.AuthorizeReservation(context.Background(), userID, uuid.NewString(), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	updated := initial
	updated.MaxMeetingAudioSeconds = 600
	updated.CreateRateLimit = 9
	repo.defaultPolicy = updated
	second, err := uc.AuthorizeReservation(context.Background(), userID, uuid.NewString(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.Policy.MaxMeetingAudioSeconds == second.Policy.MaxMeetingAudioSeconds || second.Policy.MaxMeetingAudioSeconds != 600 {
		t.Fatalf("authorization policies = first %#v, second %#v", first.Policy, second.Policy)
	}
	if limiter.calls != 2 {
		t.Fatalf("rate limiter calls = %d, want 2", limiter.calls)
	}
}

func TestMeetingQuotaPolicyProviderFailureIsStableUnavailableError(t *testing.T) {
	repo := &quotaFakeRepo{policyErr: stderrors.New("database unavailable")}
	uc, err := NewMeetingQuotaUsecase(repo, repo, &quotaFakeRateLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = uc.AuthorizeReservation(context.Background(), uuid.NewString(), uuid.NewString(), time.Now())
	if kratoserrors.Reason(err) != "MEETING_QUOTA_POLICY_UNAVAILABLE" {
		t.Fatalf("AuthorizeReservation() error = %v", err)
	}
}

func TestMeetingQuotaFinalizeValidatesClosedReason(t *testing.T) {
	policy, err := NewMeetingQuotaPolicy(validMeetingQuotaConfig())
	if err != nil {
		t.Fatal(err)
	}
	repo := &quotaFakeRepo{defaultPolicy: policy}
	uc, err := NewMeetingQuotaUsecase(repo, repo, &quotaFakeRateLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	command := MeetingUsageFinalizeCommand{ReservationID: uuid.NewString(), MeetingID: uuid.NewString()}
	if _, err := uc.Finalize(context.Background(), command); kratoserrors.Reason(err) != "SETTLEMENT_REASON_INVALID" {
		t.Fatalf("Finalize() error = %v", err)
	}
	command.Reason = MeetingUsageSettlementReasonCompleted
	command.ProviderUsageSeconds = -1
	if _, err := uc.Finalize(context.Background(), command); kratoserrors.Reason(err) != "PROVIDER_USAGE_INVALID" {
		t.Fatalf("Finalize() error = %v", err)
	}
}

func TestMeetingQuotaClosedSetsRejectUnknownValues(t *testing.T) {
	tests := []struct {
		name  string
		parse func(string) error
	}{
		{name: "reservation status", parse: func(raw string) error { _, err := ParseMeetingUsageReservationStatus(raw); return err }},
		{name: "settlement reason", parse: func(raw string) error { _, err := ParseMeetingUsageSettlementReason(raw); return err }},
		{name: "usage kind", parse: func(raw string) error { _, err := ParseMeetingUsageKind(raw); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.parse("unknown"); err == nil {
				t.Fatal("parser accepted unknown value")
			}
		})
	}
}

func TestRoundMeetingAudioUsage(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     int64
		wantErr  bool
	}{
		{name: "zero"},
		{name: "partial second", duration: time.Nanosecond, want: 1},
		{name: "exact second", duration: time.Second, want: 1},
		{name: "partial second after whole", duration: time.Second + time.Nanosecond, want: 2},
		{name: "negative", duration: -time.Nanosecond, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RoundMeetingAudioUsage(tt.duration)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("RoundMeetingAudioUsage() = (%d, %v), want (%d, err=%v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
