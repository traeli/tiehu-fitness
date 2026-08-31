package data

import (
	"context"
	stderrors "errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	platformredis "github.com/tiehu-ai/tiehu-fitness/internal/platform/redis"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type staticMeetingQuotaPolicyProvider struct {
	policy biz.MeetingQuotaPolicy
}

func TestMeetingQuotaRepositoryLoadsCurrentDefaultPolicy(t *testing.T) {
	db := openQuotaTestDatabase(t)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil && !stderrors.Is(err, gorm.ErrInvalidTransaction) {
			t.Errorf("rollback quota policy test: %v", err)
		}
	})
	if err := tx.WithContext(context.Background()).Model(&model.MeetingQuotaPolicy{}).
		Where("id = ?", int16(1)).Updates(map[string]any{
		"monthly_audio_seconds": 3600, "max_meeting_audio_seconds": 1800,
		"max_concurrent_meetings": 2, "create_rate_limit": 7,
		"create_rate_window_seconds": 300, "period_timezone": "Asia/Shanghai",
		"usage_report_interval_seconds": 15, "reservation_ttl_seconds": 1815,
		"redis_failure_policy": "postgres_fallback", "version": gorm.Expr("version + 1"),
	}).Error; err != nil {
		t.Fatal(err)
	}
	policy, err := (&MeetingQuotaRepo{db: tx}).GetDefaultPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if policy.MonthlyAudioSeconds != 3600 || policy.MaxMeetingAudioSeconds != 1800 ||
		policy.MaxConcurrentMeetings != 2 || policy.CreateRateLimit != 7 ||
		policy.CreateRateWindow != 5*time.Minute || policy.UsageReportInterval != 15*time.Second ||
		policy.ReservationTTL != 1815*time.Second || policy.RedisFailurePolicy != biz.RedisQuotaFailurePolicyPostgresFallback {
		t.Fatalf("loaded meeting quota policy = %#v", policy)
	}
}

func (p staticMeetingQuotaPolicyProvider) GetDefaultPolicy(context.Context) (biz.MeetingQuotaPolicy, error) {
	return p.policy, nil
}

type alwaysAllowMeetingRateLimiter struct{}

func (alwaysAllowMeetingRateLimiter) Allow(context.Context, string, time.Time, int32, time.Duration) (biz.MeetingCreateRateDecision, error) {
	return biz.MeetingCreateRateDecision{Allowed: true}, nil
}

func TestFixedRateWindow(t *testing.T) {
	window := 10 * time.Minute
	now := time.Date(2026, 8, 27, 12, 7, 30, 0, time.UTC)
	start, ttl := fixedRateWindow(now, window)
	if !start.Equal(time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)) || ttl != 150*time.Second {
		t.Fatalf("fixedRateWindow() = (%s, %s)", start, ttl)
	}
}

func TestMeetingCreateRateLimiterRedisIntegration(t *testing.T) {
	addr := os.Getenv("TEST_CORE_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_CORE_REDIS_ADDR is not set")
	}
	cfg := &conf.Redis{
		Addr: addr, Username: os.Getenv("TEST_CORE_REDIS_USERNAME"), Password: os.Getenv("TEST_CORE_REDIS_PASSWORD"),
		DialTimeout: durationpb.New(time.Second), ReadTimeout: durationpb.New(time.Second), WriteTimeout: durationpb.New(time.Second),
		PoolSize: 5, MinIdleConns: 1, KeyPrefix: "core:",
	}
	client, err := platformredis.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})
	limiter, err := NewMeetingCreateRateLimiter(client)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	const window = 5 * time.Second
	now := time.Now().UTC().Truncate(window).Add(100 * time.Millisecond)
	for attempt := 1; attempt <= 3; attempt++ {
		decision, allowErr := limiter.Allow(context.Background(), userID, now, 2, window)
		if allowErr != nil {
			t.Fatal(allowErr)
		}
		if decision.Allowed != (attempt <= 2) {
			t.Fatalf("attempt %d decision = %#v", attempt, decision)
		}
		if decision.RetryAfter <= 0 || decision.RetryAfter > window {
			t.Fatalf("attempt %d retry after = %s", attempt, decision.RetryAfter)
		}
	}
	start, _ := fixedRateWindow(now, window)
	key, err := client.Key("meeting_create_rate:v1:" + userID + ":" + formatUnixMillis(start))
	if err != nil {
		t.Fatal(err)
	}
	if ttl := client.Commands().PTTL(context.Background(), key).Val(); ttl <= 0 || ttl > window {
		t.Fatalf("Redis key TTL = %s", ttl)
	}
}

func TestMeetingQuotaRepositoryPostgresLedger(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	repo := NewMeetingQuotaRepo(db)
	policy := quotaTestPolicy(t, 10, 6, 1)
	uc, err := biz.NewMeetingQuotaUsecase(staticMeetingQuotaPolicyProvider{policy: policy}, repo, alwaysAllowMeetingRateLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	meetingOne := uuid.NewString()
	first, err := uc.Reserve(context.Background(), userID, meetingOne, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reservation.GrantedSeconds != 6 || first.Quota.ReservedSeconds != 6 || first.Quota.RemainingSeconds != 4 {
		t.Fatalf("first reservation = %#v", first)
	}
	duplicate, err := uc.Reserve(context.Background(), userID, meetingOne, now)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Existing || duplicate.Reservation.ID != first.Reservation.ID || duplicate.Quota.ReservedSeconds != 6 {
		t.Fatalf("duplicate reservation = %#v", duplicate)
	}
	if _, err := uc.Reserve(context.Background(), userID, uuid.NewString(), now); !stderrors.Is(err, biz.ErrMeetingConcurrentLimit) && kratoserrors.Reason(err) != "MEETING_CONCURRENT_LIMIT_REACHED" {
		t.Fatalf("concurrent reserve error = %v", err)
	}

	reported, err := uc.ReportUsage(context.Background(), first.Reservation.ID, meetingOne, 4, now.Add(time.Minute))
	if err != nil || reported.ReportedSeconds != 4 {
		t.Fatalf("ReportUsage(4) = (%#v, %v)", reported, err)
	}
	reported, err = uc.ReportUsage(context.Background(), first.Reservation.ID, meetingOne, 2, now.Add(2*time.Minute))
	if err != nil || reported.ReportedSeconds != 4 {
		t.Fatalf("ReportUsage(backward) = (%#v, %v)", reported, err)
	}
	reported, err = uc.ReportUsage(context.Background(), first.Reservation.ID, meetingOne, 100, now.Add(3*time.Minute))
	if err != nil || reported.ReportedSeconds != 6 {
		t.Fatalf("ReportUsage(over grant) = (%#v, %v)", reported, err)
	}

	finalize := biz.MeetingUsageFinalizeCommand{
		ReservationID: first.Reservation.ID, MeetingID: meetingOne, TotalAcceptedSeconds: 5,
		ProviderUsageSeconds: 7, Reason: biz.MeetingUsageSettlementReasonCompleted, FinalizedAt: now.Add(4 * time.Minute),
	}
	record, err := uc.Finalize(context.Background(), finalize)
	if err != nil || record.ActualSeconds != 6 || record.ProviderUsageSeconds != 7 {
		t.Fatalf("Finalize() = (%#v, %v)", record, err)
	}
	finalize.ProviderUsageSeconds = 8
	repeated, err := uc.Finalize(context.Background(), finalize)
	if err != nil || repeated.ID != record.ID || repeated.ActualSeconds != 6 || repeated.ProviderUsageSeconds != 8 {
		t.Fatalf("Finalize(repeated) = (%#v, %v)", repeated, err)
	}
	snapshot, err := uc.GetQuota(context.Background(), userID, now.Add(5*time.Minute))
	if err != nil || snapshot.ConsumedSeconds != 6 || snapshot.ReservedSeconds != 0 || snapshot.RemainingSeconds != 4 {
		t.Fatalf("GetQuota() = (%#v, %v)", snapshot, err)
	}

	meetingTwo := uuid.NewString()
	second, err := uc.Reserve(context.Background(), userID, meetingTwo, now.Add(6*time.Minute))
	if err != nil || second.Reservation.GrantedSeconds != 4 {
		t.Fatalf("second reservation = (%#v, %v)", second, err)
	}
	released, err := uc.ReleasePreparationFailure(context.Background(), second.Reservation.ID, meetingTwo, now.Add(7*time.Minute))
	if err != nil || released.ActualSeconds != 0 || released.Reason != biz.MeetingUsageSettlementReasonPreparationFailed {
		t.Fatalf("ReleasePreparationFailure() = (%#v, %v)", released, err)
	}
	snapshot, err = uc.GetQuota(context.Background(), userID, now.Add(8*time.Minute))
	if err != nil || snapshot.ConsumedSeconds != 6 || snapshot.ReservedSeconds != 0 || snapshot.RemainingSeconds != 4 {
		t.Fatalf("quota after release = (%#v, %v)", snapshot, err)
	}
}

func TestMeetingQuotaRepositorySnapshotsMonthlyBaseAndAddsPurchasedQuota(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	repo := NewMeetingQuotaRepo(db)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	initialPolicy := quotaTestPolicy(t, 10, 10, 1)
	initial, err := repo.GetSnapshot(context.Background(), userID, initialPolicy.PeriodAt(now), initialPolicy, now)
	if err != nil {
		t.Fatal(err)
	}
	if initial.BaseLimitSeconds != 10 || initial.PurchasedLimitSeconds != 0 ||
		initial.TotalLimitSeconds != 10 || initial.RemainingSeconds != 10 {
		t.Fatalf("initial monthly quota = %#v", initial)
	}

	changedPolicy := quotaTestPolicy(t, 20, 20, 1)
	frozen, err := repo.GetSnapshot(context.Background(), userID, changedPolicy.PeriodAt(now), changedPolicy, now)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.BaseLimitSeconds != 10 || frozen.TotalLimitSeconds != 10 {
		t.Fatalf("monthly base quota was not frozen = %#v", frozen)
	}

	period := changedPolicy.PeriodAt(now)
	if err := db.WithContext(context.Background()).Model(&model.MeetingUsagePeriod{}).
		Where("user_id = ? AND period_start = ?", userID, period.Start).
		Update("purchased_quota_seconds", 5).Error; err != nil {
		t.Fatal(err)
	}
	purchased, err := repo.GetSnapshot(context.Background(), userID, period, changedPolicy, now)
	if err != nil {
		t.Fatal(err)
	}
	if purchased.BaseLimitSeconds != 10 || purchased.PurchasedLimitSeconds != 5 ||
		purchased.TotalLimitSeconds != 15 || purchased.RemainingSeconds != 15 {
		t.Fatalf("purchased monthly quota = %#v", purchased)
	}
}

func TestMeetingQuotaRepositoryExpiresAndSettlesLastReportedUsage(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	repo := NewMeetingQuotaRepo(db)
	policy := quotaTestPolicy(t, 10, 10, 1)
	uc, err := biz.NewMeetingQuotaUsecase(staticMeetingQuotaPolicyProvider{policy: policy}, repo, alwaysAllowMeetingRateLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	meetingID := uuid.NewString()
	reserved, err := repo.Reserve(context.Background(), biz.MeetingQuotaReserveInput{
		UserID: userID, MeetingID: meetingID, Period: policy.PeriodAt(now), Policy: policy,
		Now: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReportUsage(context.Background(), reserved.Reservation.ID, meetingID, 1, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	reconciled, err := uc.ReconcileExpired(context.Background(), now.AddDate(0, 1, 0), 100)
	if err != nil || reconciled != 1 {
		t.Fatalf("ReconcileExpired() = (%d, %v)", reconciled, err)
	}
	snapshot, err := repo.GetSnapshot(context.Background(), userID, policy.PeriodAt(now), policy, now.AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConsumedSeconds != 1 || snapshot.ReservedSeconds != 0 || snapshot.ActiveMeetings != 0 {
		t.Fatalf("expired quota = %#v", snapshot)
	}
	var record model.MeetingUsageRecord
	if err := db.WithContext(context.Background()).Where("meeting_id = ?", meetingID).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.SettlementReason != biz.MeetingUsageSettlementReasonExpired.String() || record.ActualSeconds != 1 {
		t.Fatalf("expired usage record = %#v", record)
	}
}

func TestMeetingQuotaRepositoryAppliesOneSecondOverrideAndExhaustsExactly(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	one := int64(1)
	if err := db.WithContext(context.Background()).Create(&model.UserMeetingQuotaOverride{
		UserID: userID, Status: biz.MeetingQuotaOverrideStatusActive.String(),
		MonthlyAudioSeconds: &one, MaxMeetingAudioSeconds: &one,
	}).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewMeetingQuotaRepo(db)
	policy := quotaTestPolicy(t, 10, 6, 1)
	uc, err := biz.NewMeetingQuotaUsecase(staticMeetingQuotaPolicyProvider{policy: policy}, repo, alwaysAllowMeetingRateLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	meetingID := uuid.NewString()
	reserved, err := uc.Reserve(context.Background(), userID, meetingID, now)
	if err != nil {
		t.Fatal(err)
	}
	if reserved.Reservation.GrantedSeconds != 1 || reserved.Quota.LimitSeconds != 1 || reserved.Quota.RemainingSeconds != 0 {
		t.Fatalf("one-second reservation = %#v", reserved)
	}
	if _, err := uc.Finalize(context.Background(), biz.MeetingUsageFinalizeCommand{
		ReservationID: reserved.Reservation.ID, MeetingID: meetingID, TotalAcceptedSeconds: 1,
		Reason: biz.MeetingUsageSettlementReasonQuotaExhausted, FinalizedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := uc.GetQuota(context.Background(), userID, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConsumedSeconds != 1 || snapshot.RemainingSeconds != 0 {
		t.Fatalf("exhausted snapshot = %#v", snapshot)
	}
	if _, err := uc.Reserve(context.Background(), userID, uuid.NewString(), now.Add(3*time.Second)); kratoserrors.Reason(err) != "MEETING_QUOTA_EXCEEDED" {
		t.Fatalf("Reserve() after exact exhaustion error = %v", err)
	}
}

func TestMeetingQuotaRepositoryFinalizesEveryReasonIdempotently(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	repo := NewMeetingQuotaRepo(db)
	policy := quotaTestPolicy(t, 100, 10, 1)
	uc, err := biz.NewMeetingQuotaUsecase(staticMeetingQuotaPolicyProvider{policy: policy}, repo, alwaysAllowMeetingRateLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	reasons := []biz.MeetingUsageSettlementReason{
		biz.MeetingUsageSettlementReasonCompleted,
		biz.MeetingUsageSettlementReasonQuotaExhausted,
		biz.MeetingUsageSettlementReasonCancelled,
		biz.MeetingUsageSettlementReasonFailed,
		biz.MeetingUsageSettlementReasonExpired,
	}
	for index, settlementReason := range reasons {
		meetingID := uuid.NewString()
		reserved, reserveErr := uc.Reserve(context.Background(), userID, meetingID, now.Add(time.Duration(index)*time.Minute))
		if reserveErr != nil {
			t.Fatalf("Reserve(%s) error = %v", settlementReason.String(), reserveErr)
		}
		command := biz.MeetingUsageFinalizeCommand{
			ReservationID: reserved.Reservation.ID, MeetingID: meetingID, TotalAcceptedSeconds: 2,
			Reason: settlementReason, FinalizedAt: now.Add(time.Duration(index)*time.Minute + time.Second),
		}
		first, finalizeErr := uc.Finalize(context.Background(), command)
		if finalizeErr != nil || first.ActualSeconds != 2 || first.Reason != settlementReason {
			t.Fatalf("Finalize(%s) = (%#v, %v)", settlementReason.String(), first, finalizeErr)
		}
		second, finalizeErr := uc.Finalize(context.Background(), command)
		if finalizeErr != nil || second.ID != first.ID || second.ActualSeconds != first.ActualSeconds {
			t.Fatalf("Finalize(%s repeated) = (%#v, %v)", settlementReason.String(), second, finalizeErr)
		}
	}
	snapshot, err := uc.GetQuota(context.Background(), userID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConsumedSeconds != int64(len(reasons))*2 || snapshot.ReservedSeconds != 0 {
		t.Fatalf("quota after all settlement reasons = %#v", snapshot)
	}
}

func TestMeetingQuotaRepositorySerializesConcurrentReservations(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	repo := NewMeetingQuotaRepo(db)
	policy := quotaTestPolicy(t, 100, 100, 1)
	period := policy.PeriodAt(time.Now())
	const workers = 8
	errorsChannel := make(chan error, workers)
	successes := make(chan *biz.MeetingQuotaReservationResult, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := repo.Reserve(context.Background(), biz.MeetingQuotaReserveInput{
				UserID: userID, MeetingID: uuid.NewString(), Period: period, Policy: policy,
				Now: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
			})
			if err != nil {
				errorsChannel <- err
				return
			}
			successes <- result
		}()
	}
	group.Wait()
	close(errorsChannel)
	close(successes)
	if len(successes) != 1 {
		t.Fatalf("successful concurrent reservations = %d, want 1", len(successes))
	}
	limited := 0
	for err := range errorsChannel {
		if stderrors.Is(err, biz.ErrMeetingConcurrentLimit) {
			limited++
			continue
		}
		t.Fatalf("unexpected concurrent error = %v", err)
	}
	if limited != workers-1 {
		t.Fatalf("concurrent limit errors = %d, want %d", limited, workers-1)
	}
	var periodRow model.MeetingUsagePeriod
	if err := db.WithContext(context.Background()).Where("user_id = ? AND period_start = ?", userID, period.Start).Take(&periodRow).Error; err != nil {
		t.Fatal(err)
	}
	if periodRow.ReservedSeconds != 100 || periodRow.ConsumedSeconds != 0 {
		t.Fatalf("period after concurrency = %#v", periodRow)
	}
}

func TestMeetingQuotaRepositoryCancellationAndRollback(t *testing.T) {
	db := openQuotaTestDatabase(t)
	repo := NewMeetingQuotaRepo(db)
	policy := quotaTestPolicy(t, 100, 100, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repo.GetSnapshot(ctx, uuid.NewString(), policy.PeriodAt(time.Now()), policy, time.Now()); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("GetSnapshot() error = %v", err)
	}
	nonexistentUser := uuid.NewString()
	period := policy.PeriodAt(time.Now())
	_, err := repo.Reserve(context.Background(), biz.MeetingQuotaReserveInput{
		UserID: nonexistentUser, MeetingID: uuid.NewString(), Period: period, Policy: policy,
		Now: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err == nil {
		t.Fatal("Reserve() expected foreign-key failure")
	}
	var count int64
	if err := db.WithContext(context.Background()).Model(&model.MeetingUsagePeriod{}).Where("user_id = ?", nonexistentUser).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled back period count = %d", count)
	}
}

func openQuotaTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_CORE_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CORE_DATABASE_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func createQuotaTestUser(t *testing.T, db *gorm.DB) string {
	t.Helper()
	user := model.User{Status: biz.UserStatusActive.String()}
	if err := db.WithContext(context.Background()).Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.WithContext(context.Background()).Unscoped().Delete(&user).Error; err != nil {
			t.Errorf("cleanup quota test user: %v", err)
		}
	})
	return user.ID
}

func quotaTestPolicy(t *testing.T, monthly, perMeeting int64, concurrent int32) biz.MeetingQuotaPolicy {
	t.Helper()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return biz.MeetingQuotaPolicy{
		MonthlyAudioSeconds: monthly, MaxMeetingAudioSeconds: perMeeting, MaxConcurrentMeetings: concurrent,
		CreateRateLimit: 5, CreateRateWindow: 10 * time.Minute, UsageReportInterval: 30 * time.Second,
		ReservationTTL: 4*time.Hour + 30*time.Second, PeriodLocation: location,
		RedisFailurePolicy: biz.RedisQuotaFailurePolicyDeny,
	}
}

func formatUnixMillis(value time.Time) string {
	return strconv.FormatInt(value.UnixMilli(), 10)
}
