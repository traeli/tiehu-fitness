package data

import (
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxExpiredReservationsPerReconciliation = 1_000

type MeetingQuotaRepo struct{ db *gorm.DB }

var _ biz.MeetingQuotaRepo = (*MeetingQuotaRepo)(nil)
var _ biz.MeetingQuotaPolicyProvider = (*MeetingQuotaRepo)(nil)

func NewMeetingQuotaRepo(db *gorm.DB) *MeetingQuotaRepo { return &MeetingQuotaRepo{db: db} }

func (r *MeetingQuotaRepo) GetDefaultPolicy(ctx context.Context) (biz.MeetingQuotaPolicy, error) {
	var row model.MeetingQuotaPolicy
	if err := r.db.WithContext(ctx).Where("id = ?", int16(1)).Take(&row).Error; err != nil {
		return biz.MeetingQuotaPolicy{}, quotaDataError(err)
	}
	createWindow, err := checkedQuotaDuration(row.CreateRateWindowSeconds)
	if err != nil {
		return biz.MeetingQuotaPolicy{}, quotaDataError(fmt.Errorf("map meeting quota create rate window: %w", err))
	}
	usageInterval, err := checkedQuotaDuration(row.UsageReportIntervalSeconds)
	if err != nil {
		return biz.MeetingQuotaPolicy{}, quotaDataError(fmt.Errorf("map meeting quota usage report interval: %w", err))
	}
	reservationTTL, err := checkedQuotaDuration(row.ReservationTTLSeconds)
	if err != nil {
		return biz.MeetingQuotaPolicy{}, quotaDataError(fmt.Errorf("map meeting quota reservation TTL: %w", err))
	}
	failurePolicy, err := biz.ParseRedisQuotaFailurePolicy(row.RedisFailurePolicy)
	if err != nil {
		return biz.MeetingQuotaPolicy{}, quotaDataError(err)
	}
	policy, err := biz.NewMeetingQuotaPolicy(biz.MeetingQuotaPolicyInput{
		MonthlyAudioSeconds: row.MonthlyAudioSeconds, MaxMeetingAudioSeconds: row.MaxMeetingAudioSeconds,
		MaxConcurrentMeetings: row.MaxConcurrentMeetings, CreateRateLimit: row.CreateRateLimit,
		CreateRateWindow: createWindow, PeriodTimezone: row.PeriodTimezone,
		UsageReportInterval: usageInterval, ReservationTTL: reservationTTL, RedisFailurePolicy: failurePolicy,
	})
	if err != nil {
		return biz.MeetingQuotaPolicy{}, quotaDataError(fmt.Errorf("validate stored meeting quota policy: %w", err))
	}
	return policy, nil
}

func checkedQuotaDuration(seconds int64) (time.Duration, error) {
	if seconds <= 0 || seconds > int64((1<<63-1)/time.Second) {
		return 0, fmt.Errorf("duration seconds are out of range")
	}
	return time.Duration(seconds) * time.Second, nil
}

func (r *MeetingQuotaRepo) ReportUsage(ctx context.Context, input biz.MeetingQuotaReportInput) (*biz.MeetingUsageReservation, error) {
	var output *biz.MeetingUsageReservation
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockMeetingByReservation(ctx, tx, input.ReservationID, input.MeetingID)
		if err != nil {
			return err
		}
		status, err := biz.ParseMeetingUsageReservationStatus(row.QuotaStatus)
		if err != nil {
			return err
		}
		if status == biz.MeetingUsageReservationStatusActive {
			next := minQuotaSeconds(input.TotalSeconds, row.GrantedAudioSeconds)
			updates := make(map[string]any, 3)
			if next > row.ReportedAudioSeconds {
				updates["reported_audio_seconds"] = next
				row.ReportedAudioSeconds = next
			}
			if input.ExpiresAt.After(row.QuotaExpiresAt) {
				updates["quota_expires_at"] = input.ExpiresAt
				row.QuotaExpiresAt = input.ExpiresAt
			}
			if input.ObservedAt.After(row.UpdatedAt) {
				updates["updated_at"] = input.ObservedAt
				row.UpdatedAt = input.ObservedAt
			}
			if len(updates) > 0 {
				if err := tx.WithContext(ctx).Model(row).Updates(updates).Error; err != nil {
					return err
				}
			}
		}
		output, err = meetingToQuotaReservation(row)
		return err
	})
	if err != nil {
		if stderrors.Is(err, biz.ErrMeetingQuotaReservationNotFound) {
			return nil, err
		}
		return nil, quotaDataError(err)
	}
	return output, nil
}

func (r *MeetingQuotaRepo) Finalize(ctx context.Context, input biz.MeetingQuotaFinalizeInput) (*biz.MeetingUsageRecord, error) {
	var output *biz.MeetingUsageRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		meeting, err := lockMeetingByReservation(ctx, tx, input.ReservationID, input.MeetingID)
		if err != nil {
			return err
		}
		output, err = r.finalizeWithTx(ctx, tx, input, meeting)
		return err
	})
	if err != nil {
		if stderrors.Is(err, biz.ErrMeetingQuotaReservationNotFound) {
			return nil, err
		}
		return nil, quotaDataError(err)
	}
	return output, nil
}

// reserveWithTx locks the user's monthly balance and reserves every currently
// available second. The caller persists the meeting in the same transaction;
// no independent reservation row exists.
func (r *MeetingQuotaRepo) reserveWithTx(ctx context.Context, tx *gorm.DB, input biz.MeetingQuotaReserveInput) (*biz.MeetingQuotaReservationResult, error) {
	period, err := lockMonthlyQuota(ctx, tx, input.UserID, input.Period, input.Policy.MonthlyAudioSeconds)
	if err != nil {
		return nil, err
	}
	if err := reconcileExpiredMeetings(ctx, tx, period, input.Now); err != nil {
		return nil, err
	}
	activeCount, err := activeMeetingCount(ctx, tx, period.UserID, period.PeriodStart, input.Now)
	if err != nil {
		return nil, err
	}
	if activeCount >= int64(input.Policy.MaxConcurrentMeetings) {
		return nil, biz.ErrMeetingConcurrentLimitReached
	}
	totalLimit, err := monthlyQuotaTotalLimit(period)
	if err != nil {
		return nil, err
	}
	granted := totalLimit - period.ConsumedSeconds - period.ReservedSeconds
	if granted <= 0 {
		return nil, biz.ErrMeetingQuotaExceeded
	}
	reservationID := input.ReservationID
	if reservationID == "" {
		reservationID = uuid.NewString()
	}
	update := tx.WithContext(ctx).Model(period).Updates(map[string]any{
		"reserved_seconds": gorm.Expr("reserved_seconds + ?", granted), "updated_at": input.Now,
	})
	if update.Error != nil {
		return nil, update.Error
	}
	if update.RowsAffected != 1 {
		return nil, fmt.Errorf("monthly meeting quota disappeared during reservation")
	}
	period.ReservedSeconds += granted
	reservation := &biz.MeetingUsageReservation{
		ID: reservationID, UserID: input.UserID, MeetingID: input.MeetingID,
		Period: input.Period, GrantedSeconds: granted,
		Status: biz.MeetingUsageReservationStatusActive, ExpiresAt: input.ExpiresAt,
	}
	snapshot, err := quotaSnapshot(ctx, tx, period, input.Policy, input.Now)
	if err != nil {
		return nil, err
	}
	return &biz.MeetingQuotaReservationResult{Reservation: reservation, Quota: snapshot}, nil
}

func (r *MeetingQuotaRepo) finalizeWithTx(ctx context.Context, tx *gorm.DB, input biz.MeetingQuotaFinalizeInput, meeting *model.Meeting) (*biz.MeetingUsageRecord, error) {
	if meeting == nil || meeting.ReservationID != input.ReservationID || meeting.ID != input.MeetingID {
		return nil, biz.ErrMeetingQuotaReservationNotFound
	}
	status, err := biz.ParseMeetingUsageReservationStatus(meeting.QuotaStatus)
	if err != nil {
		return nil, err
	}
	if status.IsTerminal() {
		if input.ProviderUsageSeconds > meeting.ProviderUsageSeconds {
			if err := tx.WithContext(ctx).Model(meeting).Updates(map[string]any{
				"provider_usage_seconds": input.ProviderUsageSeconds, "updated_at": input.FinalizedAt,
			}).Error; err != nil {
				return nil, err
			}
			meeting.ProviderUsageSeconds = input.ProviderUsageSeconds
		}
		return meetingToUsageRecord(meeting)
	}

	var monthly model.UserMeetingMonthlyQuota
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND period_start = ?", meeting.UserID, meeting.QuotaPeriodStart).Take(&monthly).Error; err != nil {
		return nil, err
	}
	reported := meeting.ReportedAudioSeconds
	if input.TotalAcceptedSeconds > reported {
		reported = minQuotaSeconds(input.TotalAcceptedSeconds, meeting.GrantedAudioSeconds)
	}
	reported, err = biz.RoundMeetingAudioSeconds(reported)
	if err != nil {
		return nil, err
	}
	reported = minQuotaSeconds(reported, meeting.GrantedAudioSeconds)
	actual := minQuotaSeconds(reported, meeting.GrantedAudioSeconds)
	if input.Reason == biz.MeetingUsageSettlementReasonPreparationFailed {
		actual = 0
	}
	updateMonthly := tx.WithContext(ctx).Model(&monthly).
		Where("reserved_seconds >= ?", meeting.GrantedAudioSeconds).
		Updates(map[string]any{
			"reserved_seconds": gorm.Expr("reserved_seconds - ?", meeting.GrantedAudioSeconds),
			"consumed_seconds": gorm.Expr("consumed_seconds + ?", actual), "updated_at": input.FinalizedAt,
		})
	if updateMonthly.Error != nil {
		return nil, updateMonthly.Error
	}
	if updateMonthly.RowsAffected != 1 {
		return nil, fmt.Errorf("monthly meeting quota reserved balance is inconsistent")
	}
	terminalStatus := reservationStatusForReason(input.Reason)
	meetingUpdates := map[string]any{
		"reported_audio_seconds": reported, "actual_audio_seconds": actual,
		"provider_usage_seconds": input.ProviderUsageSeconds,
		"quota_status":           terminalStatus.String(), "quota_finalized_at": input.FinalizedAt,
		"quota_settlement_reason": input.Reason.String(), "updated_at": input.FinalizedAt,
	}
	if input.Reason == biz.MeetingUsageSettlementReasonExpired {
		expiryUpdates, err := expiredMeetingStateUpdates(meeting, input.FinalizedAt)
		if err != nil {
			return nil, err
		}
		for key, value := range expiryUpdates {
			meetingUpdates[key] = value
		}
	}
	if err := tx.WithContext(ctx).Model(meeting).Updates(meetingUpdates).Error; err != nil {
		return nil, err
	}
	meeting.ReportedAudioSeconds = reported
	meeting.ActualAudioSeconds = actual
	meeting.ProviderUsageSeconds = input.ProviderUsageSeconds
	meeting.QuotaStatus = terminalStatus.String()
	meeting.QuotaFinalizedAt = &input.FinalizedAt
	meeting.QuotaSettlementReason = input.Reason.String()
	meeting.UpdatedAt = input.FinalizedAt
	return meetingToUsageRecord(meeting)
}

func (r *MeetingQuotaRepo) GetSnapshot(ctx context.Context, userID string, periodInput biz.MeetingBillingPeriod, policy biz.MeetingQuotaPolicy, now time.Time) (*biz.MeetingQuotaSnapshot, error) {
	var output *biz.MeetingQuotaSnapshot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		period, err := lockMonthlyQuota(ctx, tx, userID, periodInput, policy.MonthlyAudioSeconds)
		if err != nil {
			return err
		}
		if err := reconcileExpiredMeetings(ctx, tx, period, now); err != nil {
			return err
		}
		output, err = quotaSnapshot(ctx, tx, period, policy, now)
		return err
	})
	if err != nil {
		return nil, quotaDataError(err)
	}
	return output, nil
}

func (r *MeetingQuotaRepo) ListExpiredReservations(ctx context.Context, before time.Time, limit int) ([]*biz.MeetingUsageReservation, error) {
	var rows []model.Meeting
	err := r.db.WithContext(ctx).
		Where("quota_status = ? AND quota_expires_at <= ? AND deleted_at IS NULL", biz.MeetingUsageReservationStatusActive.String(), before).
		Order("quota_expires_at ASC, reservation_id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, quotaDataError(err)
	}
	reservations := make([]*biz.MeetingUsageReservation, 0, len(rows))
	for index := range rows {
		reservation, mapErr := meetingToQuotaReservation(&rows[index])
		if mapErr != nil {
			return nil, quotaDataError(mapErr)
		}
		reservations = append(reservations, reservation)
	}
	return reservations, nil
}

func lockMonthlyQuota(ctx context.Context, tx *gorm.DB, userID string, period biz.MeetingBillingPeriod, baseQuotaSeconds int64) (*model.UserMeetingMonthlyQuota, error) {
	if baseQuotaSeconds <= 0 {
		return nil, fmt.Errorf("monthly meeting quota base limit must be positive")
	}
	candidate := model.UserMeetingMonthlyQuota{
		UserID: userID, PeriodStart: period.Start, PeriodEnd: period.End, BaseQuotaSeconds: baseQuotaSeconds,
	}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "period_start"}}, DoNothing: true,
	}).Create(&candidate).Error; err != nil {
		return nil, err
	}
	var row model.UserMeetingMonthlyQuota
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND period_start = ?", userID, period.Start).Take(&row).Error; err != nil {
		return nil, err
	}
	if !row.PeriodEnd.Equal(period.End) {
		return nil, fmt.Errorf("monthly meeting quota period end is inconsistent")
	}
	if row.BaseQuotaSeconds == 0 {
		update := tx.WithContext(ctx).Model(&row).Where("base_quota_seconds = 0").
			Updates(map[string]any{"base_quota_seconds": baseQuotaSeconds, "updated_at": time.Now().UTC()})
		if update.Error != nil {
			return nil, update.Error
		}
		if update.RowsAffected != 1 {
			return nil, fmt.Errorf("monthly meeting quota base limit initialization conflicted")
		}
		row.BaseQuotaSeconds = baseQuotaSeconds
	}
	if _, err := monthlyQuotaTotalLimit(&row); err != nil {
		return nil, err
	}
	return &row, nil
}

func reconcileExpiredMeetings(ctx context.Context, tx *gorm.DB, monthly *model.UserMeetingMonthlyQuota, now time.Time) error {
	var rows []model.Meeting
	// Finalization locks a meeting before its monthly quota row. Reconciliation
	// owns the monthly row first, so SKIP LOCKED avoids the inverse lock order;
	// a row currently being finalized no longer needs expiry reconciliation.
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("user_id = ? AND quota_period_start = ? AND quota_status = ? AND quota_expires_at <= ? AND deleted_at IS NULL",
			monthly.UserID, monthly.PeriodStart, biz.MeetingUsageReservationStatusActive.String(), now).
		Order("quota_expires_at ASC").Limit(maxExpiredReservationsPerReconciliation).Find(&rows).Error; err != nil {
		return err
	}
	for index := range rows {
		row := &rows[index]
		billable, err := biz.RoundMeetingAudioSeconds(row.ReportedAudioSeconds)
		if err != nil {
			return err
		}
		billable = minQuotaSeconds(billable, row.GrantedAudioSeconds)
		actual := minQuotaSeconds(billable, row.GrantedAudioSeconds)
		update := tx.WithContext(ctx).Model(monthly).Where("reserved_seconds >= ?", row.GrantedAudioSeconds).
			Updates(map[string]any{
				"reserved_seconds": gorm.Expr("reserved_seconds - ?", row.GrantedAudioSeconds),
				"consumed_seconds": gorm.Expr("consumed_seconds + ?", actual), "updated_at": now,
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return fmt.Errorf("monthly meeting quota reserved balance is inconsistent")
		}
		monthly.ReservedSeconds -= row.GrantedAudioSeconds
		monthly.ConsumedSeconds += actual
		meetingUpdates := map[string]any{
			"reported_audio_seconds": billable, "actual_audio_seconds": actual, "quota_status": biz.MeetingUsageReservationStatusExpired.String(),
			"quota_finalized_at": now, "quota_settlement_reason": biz.MeetingUsageSettlementReasonExpired.String(),
			"updated_at": now,
		}
		expiryUpdates, err := expiredMeetingStateUpdates(row, now)
		if err != nil {
			return err
		}
		for key, value := range expiryUpdates {
			meetingUpdates[key] = value
		}
		if err := tx.WithContext(ctx).Model(row).Updates(meetingUpdates).Error; err != nil {
			return err
		}
	}
	return nil
}

func expiredMeetingStateUpdates(meeting *model.Meeting, expiredAt time.Time) (map[string]any, error) {
	meetingStatus, err := biz.ParseMeetingStatus(meeting.Status)
	if err != nil {
		return nil, err
	}
	transcriptionStatus, err := biz.ParseMeetingTranscriptionStatus(meeting.TranscriptionStatus)
	if err != nil {
		return nil, err
	}
	updates := make(map[string]any, 3)
	if !meetingStatus.IsTerminal() {
		if !meetingStatus.CanTransitionTo(biz.MeetingStatusFailed) {
			return nil, fmt.Errorf("meeting status cannot transition to failed after quota lease expiry")
		}
		updates["status"] = biz.MeetingStatusFailed.String()
		if meeting.StoppedAt == nil {
			updates["stopped_at"] = expiredAt
		}
	}
	if !transcriptionStatus.IsTerminal() {
		if !transcriptionStatus.CanTransitionTo(biz.MeetingTranscriptionStatusExpired) {
			return nil, fmt.Errorf("meeting transcription status cannot transition to expired after quota lease expiry")
		}
		updates["transcription_status"] = biz.MeetingTranscriptionStatusExpired.String()
	}
	return updates, nil
}

func quotaSnapshot(ctx context.Context, tx *gorm.DB, monthly *model.UserMeetingMonthlyQuota, policy biz.MeetingQuotaPolicy, now time.Time) (*biz.MeetingQuotaSnapshot, error) {
	activeCount, err := activeMeetingCount(ctx, tx, monthly.UserID, monthly.PeriodStart, now)
	if err != nil {
		return nil, err
	}
	if activeCount > math.MaxInt32 {
		return nil, fmt.Errorf("active meeting count exceeds int32")
	}
	totalLimit, err := monthlyQuotaTotalLimit(monthly)
	if err != nil {
		return nil, err
	}
	remaining := totalLimit - monthly.ConsumedSeconds - monthly.ReservedSeconds
	if remaining < 0 {
		remaining = 0
	}
	return &biz.MeetingQuotaSnapshot{
		Period:           biz.MeetingBillingPeriod{Start: monthly.PeriodStart, End: monthly.PeriodEnd},
		BaseLimitSeconds: monthly.BaseQuotaSeconds, PurchasedLimitSeconds: monthly.PurchasedQuotaSeconds,
		TotalLimitSeconds: totalLimit, LimitSeconds: totalLimit, ConsumedSeconds: monthly.ConsumedSeconds,
		ReservedSeconds: monthly.ReservedSeconds, RemainingSeconds: remaining,
		MaxMeetingSeconds: totalLimit, MaxConcurrentMeetings: policy.MaxConcurrentMeetings,
		ActiveMeetings: int32(activeCount),
	}, nil
}

func monthlyQuotaTotalLimit(monthly *model.UserMeetingMonthlyQuota) (int64, error) {
	if monthly == nil {
		return 0, fmt.Errorf("monthly meeting quota is nil")
	}
	if monthly.BaseQuotaSeconds <= 0 || monthly.PurchasedQuotaSeconds < 0 {
		return 0, fmt.Errorf("monthly meeting quota balance is invalid")
	}
	if monthly.BaseQuotaSeconds > math.MaxInt64-monthly.PurchasedQuotaSeconds {
		return 0, fmt.Errorf("monthly meeting quota total limit overflows int64")
	}
	return monthly.BaseQuotaSeconds + monthly.PurchasedQuotaSeconds, nil
}

func activeMeetingCount(ctx context.Context, tx *gorm.DB, userID string, periodStart, now time.Time) (int64, error) {
	var count int64
	err := tx.WithContext(ctx).Model(&model.Meeting{}).
		Where("user_id = ? AND quota_period_start = ? AND quota_status = ? AND quota_expires_at > ? AND deleted_at IS NULL",
			userID, periodStart, biz.MeetingUsageReservationStatusActive.String(), now).Count(&count).Error
	return count, err
}

func lockMeetingByReservation(ctx context.Context, tx *gorm.DB, reservationID, meetingID string) (*model.Meeting, error) {
	var row model.Meeting
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("reservation_id = ? AND id = ? AND deleted_at IS NULL", reservationID, meetingID).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingQuotaReservationNotFound
	}
	return &row, err
}

func meetingToQuotaReservation(row *model.Meeting) (*biz.MeetingUsageReservation, error) {
	if row == nil {
		return nil, fmt.Errorf("meeting quota data is nil")
	}
	status, err := biz.ParseMeetingUsageReservationStatus(row.QuotaStatus)
	if err != nil {
		return nil, err
	}
	return &biz.MeetingUsageReservation{
		ID: row.ReservationID, UserID: row.UserID, MeetingID: row.ID,
		Period:         biz.MeetingBillingPeriod{Start: row.QuotaPeriodStart, End: row.QuotaPeriodEnd},
		GrantedSeconds: row.GrantedAudioSeconds, ReportedSeconds: row.ReportedAudioSeconds,
		Status: status, ExpiresAt: row.QuotaExpiresAt, FinalizedAt: row.QuotaFinalizedAt,
	}, nil
}

func meetingToUsageRecord(row *model.Meeting) (*biz.MeetingUsageRecord, error) {
	if row == nil || row.QuotaFinalizedAt == nil {
		return nil, fmt.Errorf("settled meeting quota data is incomplete")
	}
	reason, err := biz.ParseMeetingUsageSettlementReason(row.QuotaSettlementReason)
	if err != nil {
		return nil, err
	}
	return &biz.MeetingUsageRecord{
		ID: row.ID, ReservationID: row.ReservationID, UserID: row.UserID, MeetingID: row.ID,
		Period: biz.MeetingBillingPeriod{Start: row.QuotaPeriodStart, End: row.QuotaPeriodEnd},
		Kind:   biz.MeetingUsageKindASRAudio, ActualSeconds: row.ActualAudioSeconds,
		ProviderUsageSeconds: row.ProviderUsageSeconds, Reason: reason, SettledAt: *row.QuotaFinalizedAt,
	}, nil
}

func reservationStatusForReason(reason biz.MeetingUsageSettlementReason) biz.MeetingUsageReservationStatus {
	switch reason {
	case biz.MeetingUsageSettlementReasonPreparationFailed:
		return biz.MeetingUsageReservationStatusReleased
	case biz.MeetingUsageSettlementReasonExpired:
		return biz.MeetingUsageReservationStatusExpired
	default:
		return biz.MeetingUsageReservationStatusSettled
	}
}

func minQuotaSeconds(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func quotaDataError(err error) error {
	if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return databaseError(err)
}
