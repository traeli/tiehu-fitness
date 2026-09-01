package data

import (
	"context"
	stderrors "errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	platformredis "github.com/tiehu-ai/tiehu-fitness/internal/platform/redis"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type staticMeetingQuotaPolicyProvider struct{ policy biz.MeetingQuotaPolicy }

func (p staticMeetingQuotaPolicyProvider) GetDefaultPolicy(context.Context) (biz.MeetingQuotaPolicy, error) {
	return p.policy, nil
}

type alwaysAllowMeetingRateLimiter struct{}

func (alwaysAllowMeetingRateLimiter) Allow(context.Context, string, time.Time, int32, time.Duration) (biz.MeetingCreateRateDecision, error) {
	return biz.MeetingCreateRateDecision{Allowed: true}, nil
}

func TestMeetingQuotaRepositoryLoadsCurrentDefaultPolicy(t *testing.T) {
	db := openQuotaTestDatabase(t)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })
	if err := tx.WithContext(context.Background()).Model(&model.MeetingQuotaPolicy{}).Where("id = ?", int16(1)).Updates(map[string]any{
		"monthly_audio_seconds": 3600, "max_meeting_audio_seconds": 1800, "max_concurrent_meetings": 2,
		"create_rate_limit": 7, "create_rate_window_seconds": 300, "period_timezone": "Asia/Shanghai",
		"usage_report_interval_seconds": 15, "reservation_ttl_seconds": 1815,
		"redis_failure_policy": "postgres_fallback", "version": gorm.Expr("version + 1"),
	}).Error; err != nil {
		t.Fatal(err)
	}
	policy, err := (&MeetingQuotaRepo{db: tx}).GetDefaultPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if policy.MonthlyAudioSeconds != 3600 || policy.MaxMeetingAudioSeconds != 1800 || policy.MaxConcurrentMeetings != 2 ||
		policy.CreateRateLimit != 7 || policy.CreateRateWindow != 5*time.Minute || policy.UsageReportInterval != 15*time.Second ||
		policy.ReservationTTL != 1815*time.Second || policy.RedisFailurePolicy != biz.RedisQuotaFailurePolicyPostgresFallback {
		t.Fatalf("loaded meeting quota policy = %#v", policy)
	}
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
	t.Cleanup(func() { _ = client.Close() })
	limiter, err := NewMeetingCreateRateLimiter(client)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	const window = 5 * time.Second
	now := time.Now().UTC().Truncate(window).Add(100 * time.Millisecond)
	for attempt := 1; attempt <= 3; attempt++ {
		decision, allowErr := limiter.Allow(context.Background(), userID, now, 2, window)
		if allowErr != nil || decision.Allowed != (attempt <= 2) {
			t.Fatalf("attempt %d decision = %#v, error = %v", attempt, decision, allowErr)
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

func TestCompactMeetingQuotaLedgerReservesAllRemainingAndSettlesInMeeting(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	repo := NewMeetingQuotaRepo(db)
	meetingRepo := NewMeetingRepo(db)
	policy := quotaTestPolicy(t, 10, 6, 1)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	meetingID, reservationID := uuid.NewString(), uuid.NewString()
	created, err := meetingRepo.CreateWithQuota(context.Background(), biz.MeetingCreatePersistenceInput{
		MeetingID: meetingID, UserID: userID, IdempotencyKey: uuid.NewString(),
		RequestFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Language:           biz.MeetingLanguageAuto, Now: now,
	}, biz.MeetingQuotaReserveInput{
		ReservationID: reservationID, UserID: userID, MeetingID: meetingID,
		Period: policy.PeriodAt(now), Policy: policy, Now: now, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Reservation.GrantedSeconds != 10 {
		t.Fatalf("granted seconds = %d, want all 10 remaining seconds", created.Reservation.GrantedSeconds)
	}
	if _, err := repo.ReportUsage(context.Background(), reservationID, meetingID, 4, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	record, err := repo.Finalize(context.Background(), biz.MeetingQuotaFinalizeInput{
		MeetingUsageFinalizeCommand: biz.MeetingUsageFinalizeCommand{
			ReservationID: reservationID, MeetingID: meetingID, TotalAcceptedSeconds: 4,
			ProviderUsageSeconds: 5, Reason: biz.MeetingUsageSettlementReasonCompleted, FinalizedAt: now.Add(2 * time.Minute),
		}, Kind: biz.MeetingUsageKindASRAudio,
	})
	if err != nil || record.ActualSeconds != 4 || record.ProviderUsageSeconds != 5 {
		t.Fatalf("Finalize() = (%#v, %v)", record, err)
	}
	var meeting model.Meeting
	if err := db.Where("id = ?", meetingID).Take(&meeting).Error; err != nil {
		t.Fatal(err)
	}
	if meeting.QuotaStatus != biz.MeetingUsageReservationStatusSettled.String() || meeting.ActualAudioSeconds != 4 || meeting.QuotaFinalizedAt == nil {
		t.Fatalf("compact meeting quota fields = %#v", meeting)
	}
	var monthly model.UserMeetingMonthlyQuota
	if err := db.Where("user_id = ? AND period_start = ?", userID, policy.PeriodAt(now).Start).Take(&monthly).Error; err != nil {
		t.Fatal(err)
	}
	if monthly.ReservedSeconds != 0 || monthly.ConsumedSeconds != 4 {
		t.Fatalf("monthly quota = %#v", monthly)
	}
}

func TestMeetingQuotaRepositorySnapshotsMonthlyBaseAndAddsPurchasedQuota(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	repo := NewMeetingQuotaRepo(db)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	policy := quotaTestPolicy(t, 10, 10, 1)
	initial, err := repo.GetSnapshot(context.Background(), userID, policy.PeriodAt(now), policy, now)
	if err != nil || initial.BaseLimitSeconds != 10 || initial.RemainingSeconds != 10 {
		t.Fatalf("initial monthly quota = (%#v, %v)", initial, err)
	}
	period := policy.PeriodAt(now)
	if err := db.Model(&model.UserMeetingMonthlyQuota{}).Where("user_id = ? AND period_start = ?", userID, period.Start).
		Update("purchased_quota_seconds", 5).Error; err != nil {
		t.Fatal(err)
	}
	purchased, err := repo.GetSnapshot(context.Background(), userID, period, policy, now)
	if err != nil || purchased.TotalLimitSeconds != 15 || purchased.RemainingSeconds != 15 {
		t.Fatalf("purchased monthly quota = (%#v, %v)", purchased, err)
	}
}

func TestMeetingQuotaRepositoryCancellationDoesNotCreateMonthlyRow(t *testing.T) {
	db := openQuotaTestDatabase(t)
	repo := NewMeetingQuotaRepo(db)
	policy := quotaTestPolicy(t, 100, 100, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	userID := uuid.NewString()
	if _, err := repo.GetSnapshot(ctx, userID, policy.PeriodAt(time.Now()), policy, time.Now()); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("GetSnapshot() error = %v", err)
	}
	var count int64
	if err := db.Model(&model.UserMeetingMonthlyQuota{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("monthly rows after cancellation = %d", count)
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
	t.Cleanup(func() { _ = db.WithContext(context.Background()).Unscoped().Delete(&user).Error })
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
		CreateRateLimit: 100, CreateRateWindow: time.Minute, UsageReportInterval: time.Second,
		ReservationTTL: time.Duration(maxInt64ForTest(perMeeting+1, 2)) * time.Second,
		PeriodLocation: location, RedisFailurePolicy: biz.RedisQuotaFailurePolicyPostgresFallback,
	}
}

func maxInt64ForTest(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func formatUnixMillis(value time.Time) string { return strconv.FormatInt(value.UnixMilli(), 10) }
