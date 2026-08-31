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

func NewMeetingQuotaRepo(db *gorm.DB) *MeetingQuotaRepo {
	return &MeetingQuotaRepo{db: db}
}

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

func (r *MeetingQuotaRepo) GetOverride(ctx context.Context, userID string) (*biz.MeetingQuotaOverride, error) {
	var row model.UserMeetingQuotaOverride
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingQuotaOverrideNotFound
	}
	if err != nil {
		return nil, quotaDataError(err)
	}
	status, err := biz.ParseMeetingQuotaOverrideStatus(row.Status)
	if err != nil {
		return nil, quotaDataError(err)
	}
	return &biz.MeetingQuotaOverride{
		UserID: row.UserID, Status: status, MonthlyAudioSeconds: row.MonthlyAudioSeconds,
		MaxMeetingAudioSeconds: row.MaxMeetingAudioSeconds, MaxConcurrentMeetings: row.MaxConcurrentMeetings,
	}, nil
}

func (r *MeetingQuotaRepo) FindReservationByMeeting(ctx context.Context, userID, meetingID string) (*biz.MeetingUsageReservation, error) {
	var row model.MeetingUsageReservation
	err := r.db.WithContext(ctx).Where("user_id = ? AND meeting_id = ?", userID, meetingID).Take(&row).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingQuotaReservationNotFound
	}
	if err != nil {
		return nil, quotaDataError(err)
	}
	return toBizMeetingUsageReservation(&row)
}

func (r *MeetingQuotaRepo) Reserve(ctx context.Context, input biz.MeetingQuotaReserveInput) (*biz.MeetingQuotaReservationResult, error) {
	var result *biz.MeetingQuotaReservationResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reserveErr error
		result, reserveErr = r.reserveWithTx(ctx, tx, input)
		return reserveErr
	})
	if err != nil {
		if stderrors.Is(err, biz.ErrMeetingQuotaExceeded) || stderrors.Is(err, biz.ErrMeetingConcurrentLimit) || stderrors.Is(err, biz.ErrMeetingReservationConflict) {
			return nil, err
		}
		return nil, quotaDataError(err)
	}
	return result, nil
}

func (r *MeetingQuotaRepo) reserveWithTx(ctx context.Context, tx *gorm.DB, input biz.MeetingQuotaReserveInput) (*biz.MeetingQuotaReservationResult, error) {
	period, err := lockUsagePeriod(ctx, tx, input.UserID, input.Period, input.Policy.MonthlyAudioSeconds)
	if err != nil {
		return nil, err
	}
	if err := reconcileExpiredReservations(ctx, tx, period, input.Now); err != nil {
		return nil, err
	}

	var existing model.MeetingUsageReservation
	err = tx.WithContext(ctx).Where("meeting_id = ?", input.MeetingID).Take(&existing).Error
	if err == nil {
		if existing.UserID != input.UserID {
			return nil, biz.ErrMeetingReservationConflict
		}
		reservation, mapErr := toBizMeetingUsageReservation(&existing)
		if mapErr != nil {
			return nil, mapErr
		}
		snapshot, snapshotErr := quotaSnapshot(ctx, tx, period, input.Policy, input.Now)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		return &biz.MeetingQuotaReservationResult{Reservation: reservation, Quota: snapshot, Existing: true}, nil
	}
	if !stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	activeCount, err := activeReservationCount(ctx, tx, input.UserID, input.Period.Start, input.Now)
	if err != nil {
		return nil, err
	}
	if activeCount >= int64(input.Policy.MaxConcurrentMeetings) {
		return nil, biz.ErrMeetingConcurrentLimit
	}
	totalLimit, err := usagePeriodTotalLimit(period)
	if err != nil {
		return nil, err
	}
	remaining := totalLimit - period.ConsumedSeconds - period.ReservedSeconds
	if remaining <= 0 {
		return nil, biz.ErrMeetingQuotaExceeded
	}
	granted := minQuotaSeconds(input.Policy.MaxMeetingAudioSeconds, remaining)
	if granted <= 0 {
		return nil, biz.ErrMeetingQuotaExceeded
	}

	reservationID := input.ReservationID
	if reservationID == "" {
		reservationID = uuid.NewString()
	}
	row := model.MeetingUsageReservation{
		ID: reservationID, UserID: input.UserID, MeetingID: input.MeetingID,
		PeriodStart: input.Period.Start, PeriodEnd: input.Period.End,
		GrantedSeconds: granted, Status: biz.MeetingUsageReservationStatusActive.String(),
		ExpiresAt: input.ExpiresAt,
	}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		if stderrors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, biz.ErrMeetingReservationConflict
		}
		return nil, err
	}
	update := tx.WithContext(ctx).Model(period).Updates(map[string]any{
		"reserved_seconds": gorm.Expr("reserved_seconds + ?", granted),
		"updated_at":       input.Now,
	})
	if update.Error != nil {
		return nil, update.Error
	}
	if update.RowsAffected != 1 {
		return nil, fmt.Errorf("meeting usage period disappeared during reservation")
	}
	period.ReservedSeconds += granted

	reservation, err := toBizMeetingUsageReservation(&row)
	if err != nil {
		return nil, err
	}
	snapshot, err := quotaSnapshot(ctx, tx, period, input.Policy, input.Now)
	if err != nil {
		return nil, err
	}
	return &biz.MeetingQuotaReservationResult{Reservation: reservation, Quota: snapshot}, nil
}

func (r *MeetingQuotaRepo) ReportUsage(ctx context.Context, reservationID, meetingID string, totalSeconds int64, observedAt time.Time) (*biz.MeetingUsageReservation, error) {
	var output *biz.MeetingUsageReservation
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.MeetingUsageReservation
		err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("reservation_id = ? AND meeting_id = ?", reservationID, meetingID).Take(&row).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrMeetingQuotaReservationNotFound
		}
		if err != nil {
			return err
		}
		status, err := biz.ParseMeetingUsageReservationStatus(row.Status)
		if err != nil {
			return err
		}
		if status == biz.MeetingUsageReservationStatusActive {
			next := minQuotaSeconds(totalSeconds, row.GrantedSeconds)
			if next > row.ReportedSeconds {
				update := tx.WithContext(ctx).Model(&row).Updates(map[string]any{
					"reported_seconds": next,
					"updated_at":       observedAt,
				})
				if update.Error != nil {
					return update.Error
				}
				row.ReportedSeconds = next
				row.UpdatedAt = observedAt
			}
		}
		output, err = toBizMeetingUsageReservation(&row)
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
	var initial model.MeetingUsageReservation
	err := r.db.WithContext(ctx).Where("reservation_id = ? AND meeting_id = ?", input.ReservationID, input.MeetingID).Take(&initial).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingQuotaReservationNotFound
	}
	if err != nil {
		return nil, quotaDataError(err)
	}

	var output *biz.MeetingUsageRecord
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var finalizeErr error
		output, finalizeErr = r.finalizeWithTx(ctx, tx, input, &initial)
		return finalizeErr
	})
	if err != nil {
		if stderrors.Is(err, biz.ErrMeetingQuotaReservationNotFound) {
			return nil, err
		}
		return nil, quotaDataError(err)
	}
	return output, nil
}

func (r *MeetingQuotaRepo) finalizeWithTx(ctx context.Context, tx *gorm.DB, input biz.MeetingQuotaFinalizeInput, initial *model.MeetingUsageReservation) (*biz.MeetingUsageRecord, error) {
	if initial == nil {
		return nil, fmt.Errorf("initial meeting quota reservation is required")
	}
	var period model.MeetingUsagePeriod
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND period_start = ?", initial.UserID, initial.PeriodStart).Take(&period).Error; err != nil {
		return nil, err
	}
	var reservation model.MeetingUsageReservation
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("reservation_id = ? AND meeting_id = ?", input.ReservationID, input.MeetingID).Take(&reservation).Error
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrMeetingQuotaReservationNotFound
	}
	if err != nil {
		return nil, err
	}
	status, err := biz.ParseMeetingUsageReservationStatus(reservation.Status)
	if err != nil {
		return nil, err
	}
	if status.IsTerminal() {
		output, loadErr := loadMeetingUsageRecord(ctx, tx, reservation.ID, input.Kind)
		if loadErr != nil {
			return nil, loadErr
		}
		if input.ProviderUsageSeconds > output.ProviderUsageSeconds {
			if err := tx.WithContext(ctx).Model(&model.MeetingUsageRecord{}).
				Where("id = ?", output.ID).
				Updates(map[string]any{"provider_usage_seconds": input.ProviderUsageSeconds, "updated_at": input.FinalizedAt}).Error; err != nil {
				return nil, err
			}
			output.ProviderUsageSeconds = input.ProviderUsageSeconds
		}
		return output, nil
	}

	reported := reservation.ReportedSeconds
	if input.TotalAcceptedSeconds > reported {
		reported = minQuotaSeconds(input.TotalAcceptedSeconds, reservation.GrantedSeconds)
	}
	actual := minQuotaSeconds(reported, reservation.GrantedSeconds)
	if input.Reason == biz.MeetingUsageSettlementReasonPreparationFailed {
		actual = 0
	}
	updatePeriod := tx.WithContext(ctx).Model(&period).
		Where("reserved_seconds >= ?", reservation.GrantedSeconds).
		Updates(map[string]any{
			"reserved_seconds": gorm.Expr("reserved_seconds - ?", reservation.GrantedSeconds),
			"consumed_seconds": gorm.Expr("consumed_seconds + ?", actual),
			"updated_at":       input.FinalizedAt,
		})
	if updatePeriod.Error != nil {
		return nil, updatePeriod.Error
	}
	if updatePeriod.RowsAffected != 1 {
		return nil, fmt.Errorf("meeting usage period reserved balance is inconsistent")
	}

	recordRow := model.MeetingUsageRecord{
		ReservationID: reservation.ID, UserID: reservation.UserID, MeetingID: reservation.MeetingID,
		PeriodStart: reservation.PeriodStart, PeriodEnd: reservation.PeriodEnd, UsageKind: input.Kind.String(),
		ActualSeconds: actual, ProviderUsageSeconds: input.ProviderUsageSeconds,
		SettlementReason: input.Reason.String(), SettledAt: input.FinalizedAt,
	}
	if err := tx.WithContext(ctx).Create(&recordRow).Error; err != nil {
		return nil, err
	}
	terminalStatus := reservationStatusForReason(input.Reason)
	updateReservation := tx.WithContext(ctx).Model(&reservation).Updates(map[string]any{
		"reported_seconds": reported, "status": terminalStatus.String(),
		"finalized_at": input.FinalizedAt, "updated_at": input.FinalizedAt,
	})
	if updateReservation.Error != nil {
		return nil, updateReservation.Error
	}
	return toBizMeetingUsageRecord(&recordRow)
}

func (r *MeetingQuotaRepo) GetSnapshot(ctx context.Context, userID string, periodInput biz.MeetingBillingPeriod, policy biz.MeetingQuotaPolicy, now time.Time) (*biz.MeetingQuotaSnapshot, error) {
	var output *biz.MeetingQuotaSnapshot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		period, err := lockUsagePeriod(ctx, tx, userID, periodInput, policy.MonthlyAudioSeconds)
		if err != nil {
			return err
		}
		if err := reconcileExpiredReservations(ctx, tx, period, now); err != nil {
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
	var rows []model.MeetingUsageReservation
	err := r.db.WithContext(ctx).
		Where("status = ? AND expires_at <= ?", biz.MeetingUsageReservationStatusActive.String(), before).
		Order("expires_at ASC, reservation_id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, quotaDataError(err)
	}
	reservations := make([]*biz.MeetingUsageReservation, 0, len(rows))
	for index := range rows {
		reservation, mapErr := toBizMeetingUsageReservation(&rows[index])
		if mapErr != nil {
			return nil, quotaDataError(mapErr)
		}
		reservations = append(reservations, reservation)
	}
	return reservations, nil
}

func lockUsagePeriod(ctx context.Context, tx *gorm.DB, userID string, period biz.MeetingBillingPeriod, baseQuotaSeconds int64) (*model.MeetingUsagePeriod, error) {
	if baseQuotaSeconds <= 0 {
		return nil, fmt.Errorf("meeting usage period base quota must be positive")
	}
	candidate := model.MeetingUsagePeriod{
		UserID: userID, PeriodStart: period.Start, PeriodEnd: period.End,
		BaseQuotaSeconds: baseQuotaSeconds,
	}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "period_start"}}, DoNothing: true,
	}).Create(&candidate).Error; err != nil {
		return nil, err
	}
	var row model.MeetingUsagePeriod
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND period_start = ?", userID, period.Start).Take(&row).Error; err != nil {
		return nil, err
	}
	if !row.PeriodEnd.Equal(period.End) {
		return nil, fmt.Errorf("meeting usage period end is inconsistent")
	}
	// Rows created before monthly quota snapshots were introduced have zero in
	// this column. The first locked access freezes the then-effective policy.
	if row.BaseQuotaSeconds == 0 {
		update := tx.WithContext(ctx).Model(&row).
			Where("base_quota_seconds = 0").
			Updates(map[string]any{"base_quota_seconds": baseQuotaSeconds, "updated_at": time.Now().UTC()})
		if update.Error != nil {
			return nil, update.Error
		}
		if update.RowsAffected != 1 {
			return nil, fmt.Errorf("meeting usage period base quota initialization conflicted")
		}
		row.BaseQuotaSeconds = baseQuotaSeconds
	}
	if _, err := usagePeriodTotalLimit(&row); err != nil {
		return nil, err
	}
	return &row, nil
}

func reconcileExpiredReservations(ctx context.Context, tx *gorm.DB, period *model.MeetingUsagePeriod, now time.Time) error {
	var rows []model.MeetingUsageReservation
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND period_start = ? AND status = ? AND expires_at <= ?",
			period.UserID, period.PeriodStart, biz.MeetingUsageReservationStatusActive.String(), now).
		Order("expires_at ASC").Limit(maxExpiredReservationsPerReconciliation).Find(&rows).Error; err != nil {
		return err
	}
	for index := range rows {
		row := &rows[index]
		actual := minQuotaSeconds(row.ReportedSeconds, row.GrantedSeconds)
		record := model.MeetingUsageRecord{
			ReservationID: row.ID, UserID: row.UserID, MeetingID: row.MeetingID,
			PeriodStart: row.PeriodStart, PeriodEnd: row.PeriodEnd,
			UsageKind: biz.MeetingUsageKindASRAudio.String(), ActualSeconds: actual,
			SettlementReason: biz.MeetingUsageSettlementReasonExpired.String(), SettledAt: now,
		}
		if err := tx.WithContext(ctx).Create(&record).Error; err != nil {
			return err
		}
		updatePeriod := tx.WithContext(ctx).Model(period).
			Where("reserved_seconds >= ?", row.GrantedSeconds).
			Updates(map[string]any{
				"reserved_seconds": gorm.Expr("reserved_seconds - ?", row.GrantedSeconds),
				"consumed_seconds": gorm.Expr("consumed_seconds + ?", actual),
				"updated_at":       now,
			})
		if updatePeriod.Error != nil {
			return updatePeriod.Error
		}
		if updatePeriod.RowsAffected != 1 {
			return fmt.Errorf("meeting usage period reserved balance is inconsistent")
		}
		period.ReservedSeconds -= row.GrantedSeconds
		period.ConsumedSeconds += actual
		if err := tx.WithContext(ctx).Model(row).Updates(map[string]any{
			"status":       biz.MeetingUsageReservationStatusExpired.String(),
			"finalized_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func quotaSnapshot(ctx context.Context, tx *gorm.DB, period *model.MeetingUsagePeriod, policy biz.MeetingQuotaPolicy, now time.Time) (*biz.MeetingQuotaSnapshot, error) {
	activeCount, err := activeReservationCount(ctx, tx, period.UserID, period.PeriodStart, now)
	if err != nil {
		return nil, err
	}
	if activeCount > math.MaxInt32 {
		return nil, fmt.Errorf("active meeting count exceeds int32")
	}
	totalLimit, err := usagePeriodTotalLimit(period)
	if err != nil {
		return nil, err
	}
	remaining := totalLimit - period.ConsumedSeconds - period.ReservedSeconds
	if remaining < 0 {
		remaining = 0
	}
	return &biz.MeetingQuotaSnapshot{
		Period:           biz.MeetingBillingPeriod{Start: period.PeriodStart, End: period.PeriodEnd},
		BaseLimitSeconds: period.BaseQuotaSeconds, PurchasedLimitSeconds: period.PurchasedQuotaSeconds,
		TotalLimitSeconds: totalLimit, LimitSeconds: totalLimit, ConsumedSeconds: period.ConsumedSeconds,
		ReservedSeconds: period.ReservedSeconds, RemainingSeconds: remaining,
		MaxMeetingSeconds: policy.MaxMeetingAudioSeconds, MaxConcurrentMeetings: policy.MaxConcurrentMeetings,
		ActiveMeetings: int32(activeCount),
	}, nil
}

func usagePeriodTotalLimit(period *model.MeetingUsagePeriod) (int64, error) {
	if period == nil {
		return 0, fmt.Errorf("meeting usage period is nil")
	}
	if period.BaseQuotaSeconds <= 0 || period.PurchasedQuotaSeconds < 0 {
		return 0, fmt.Errorf("meeting usage period quota balance is invalid")
	}
	if period.BaseQuotaSeconds > math.MaxInt64-period.PurchasedQuotaSeconds {
		return 0, fmt.Errorf("meeting usage period total quota overflows int64")
	}
	return period.BaseQuotaSeconds + period.PurchasedQuotaSeconds, nil
}

func activeReservationCount(ctx context.Context, tx *gorm.DB, userID string, periodStart, now time.Time) (int64, error) {
	var count int64
	err := tx.WithContext(ctx).Model(&model.MeetingUsageReservation{}).
		Where("user_id = ? AND period_start = ? AND status = ? AND expires_at > ?",
			userID, periodStart, biz.MeetingUsageReservationStatusActive.String(), now).
		Count(&count).Error
	return count, err
}

func loadMeetingUsageRecord(ctx context.Context, tx *gorm.DB, reservationID string, kind biz.MeetingUsageKind) (*biz.MeetingUsageRecord, error) {
	var row model.MeetingUsageRecord
	if err := tx.WithContext(ctx).Where("reservation_id = ? AND usage_kind = ?", reservationID, kind.String()).Take(&row).Error; err != nil {
		return nil, err
	}
	return toBizMeetingUsageRecord(&row)
}

func toBizMeetingUsageReservation(row *model.MeetingUsageReservation) (*biz.MeetingUsageReservation, error) {
	if row == nil {
		return nil, fmt.Errorf("meeting usage reservation is nil")
	}
	status, err := biz.ParseMeetingUsageReservationStatus(row.Status)
	if err != nil {
		return nil, err
	}
	return &biz.MeetingUsageReservation{
		ID: row.ID, UserID: row.UserID, MeetingID: row.MeetingID,
		Period:         biz.MeetingBillingPeriod{Start: row.PeriodStart, End: row.PeriodEnd},
		GrantedSeconds: row.GrantedSeconds, ReportedSeconds: row.ReportedSeconds,
		Status: status, ExpiresAt: row.ExpiresAt, FinalizedAt: row.FinalizedAt,
	}, nil
}

func toBizMeetingUsageRecord(row *model.MeetingUsageRecord) (*biz.MeetingUsageRecord, error) {
	if row == nil {
		return nil, fmt.Errorf("meeting usage record is nil")
	}
	kind, err := biz.ParseMeetingUsageKind(row.UsageKind)
	if err != nil {
		return nil, err
	}
	reason, err := biz.ParseMeetingUsageSettlementReason(row.SettlementReason)
	if err != nil {
		return nil, err
	}
	return &biz.MeetingUsageRecord{
		ID: row.ID, ReservationID: row.ReservationID, UserID: row.UserID, MeetingID: row.MeetingID,
		Period: biz.MeetingBillingPeriod{Start: row.PeriodStart, End: row.PeriodEnd}, Kind: kind,
		ActualSeconds: row.ActualSeconds, ProviderUsageSeconds: row.ProviderUsageSeconds,
		Reason: reason, SettledAt: row.SettledAt,
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
