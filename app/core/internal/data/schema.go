package data

import (
	"context"
	"fmt"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const coreSchemaAdvisoryLockKey int64 = 0x7469656875636f72

// AutoMigrateSchema synchronizes all Core-owned GORM models and inserts only
// the non-secret baseline rows required for an empty database to start. The
// PostgreSQL transaction-scoped advisory lock serializes concurrent instances.
func AutoMigrateSchema(ctx context.Context, db *gorm.DB) error {
	if ctx == nil {
		return fmt.Errorf("core schema migration context is required")
	}
	if db == nil {
		return fmt.Errorf("core schema migration database is required")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", coreSchemaAdvisoryLockKey).Error; err != nil {
			return fmt.Errorf("lock core schema migration: %w", err)
		}
		if err := compactLegacyMeetingStorage(tx); err != nil {
			return fmt.Errorf("compact legacy meeting storage: %w", err)
		}
		if err := tx.AutoMigrate(coreSchemaModels()...); err != nil {
			return fmt.Errorf("auto migrate core schema: %w", err)
		}
		if err := compactLegacyMeetingSummaries(tx); err != nil {
			return fmt.Errorf("compact legacy meeting summaries: %w", err)
		}
		if err := seedCoreBootstrapData(tx); err != nil {
			return fmt.Errorf("seed core bootstrap data: %w", err)
		}
		return nil
	})
}

func coreSchemaModels() []any {
	return []any{
		&model.User{},
		&model.WechatIdentity{},
		&model.UToolsIdentity{},
		&model.PasswordCredential{},
		&model.UserSession{},
		&model.FitnessProfile{},
		&model.Equipment{},
		&model.Exercise{},
		&model.TrainingPlan{},
		&model.TrainingPlanItem{},
		&model.WorkoutSession{},
		&model.WorkoutSet{},
		&model.CheckIn{},
		&model.MeetingQuotaPolicy{},
		&model.UserMeetingMonthlyQuota{},
		&model.Order{},
		&model.Meeting{},
	}
}

// compactLegacyMeetingStorage migrates the former normalized meeting quota
// and transcript tables into meetings plus one monthly balance row per user.
// It is an idempotent startup bridge for environments that use AutoMigrate;
// migration 000012 is the deployable schema contract.
func compactLegacyMeetingStorage(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("meeting_usage_periods") {
		return nil
	}
	return tx.Exec(`
		ALTER TABLE meeting_usage_periods
			ADD COLUMN IF NOT EXISTS base_quota_seconds BIGINT NOT NULL DEFAULT 0,
			ADD COLUMN IF NOT EXISTS purchased_quota_seconds BIGINT NOT NULL DEFAULT 0;

		UPDATE meeting_usage_periods AS monthly
		SET base_quota_seconds = COALESCE(
			(SELECT monthly_audio_seconds FROM user_meeting_quota_overrides
			 WHERE user_id = monthly.user_id AND status = 'active' AND monthly_audio_seconds IS NOT NULL),
			(SELECT monthly_audio_seconds FROM meeting_quota_policies WHERE id = 1)
		)
		WHERE monthly.base_quota_seconds = 0;

		ALTER TABLE meetings
			ADD COLUMN IF NOT EXISTS quota_period_start TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS quota_period_end TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS reported_audio_seconds BIGINT NOT NULL DEFAULT 0,
			ADD COLUMN IF NOT EXISTS actual_audio_seconds BIGINT NOT NULL DEFAULT 0,
			ADD COLUMN IF NOT EXISTS provider_usage_seconds BIGINT NOT NULL DEFAULT 0,
			ADD COLUMN IF NOT EXISTS quota_status VARCHAR(16),
			ADD COLUMN IF NOT EXISTS quota_expires_at TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS quota_finalized_at TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS quota_settlement_reason VARCHAR(32) NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS transcript_segments JSONB NOT NULL DEFAULT '[]'::jsonb;

		UPDATE meetings AS meeting
		SET quota_period_start = reservation.period_start,
			quota_period_end = reservation.period_end,
			reported_audio_seconds = reservation.reported_seconds,
			actual_audio_seconds = COALESCE(usage.actual_seconds, 0),
			provider_usage_seconds = COALESCE(usage.provider_usage_seconds, 0),
			quota_status = reservation.status,
			quota_expires_at = reservation.expires_at,
			quota_finalized_at = reservation.finalized_at,
			quota_settlement_reason = COALESCE(usage.settlement_reason, '')
		FROM meeting_usage_reservations AS reservation
		LEFT JOIN meeting_usage_records AS usage ON usage.reservation_id = reservation.reservation_id
		WHERE meeting.reservation_id = reservation.reservation_id;

		UPDATE meetings AS meeting
		SET transcript_segments = transcript.payload
		FROM (
			SELECT meeting_id, jsonb_agg(jsonb_build_object(
				'id', segment_id,
				'sequence_no', sequence_no,
				'start_offset_ms', start_offset_ms,
				'end_offset_ms', end_offset_ms,
				'speaker_label', speaker_label,
				'content', content,
				'language', language,
				'confidence', confidence,
				'created_at', created_at
			) ORDER BY sequence_no) AS payload
			FROM meeting_transcript_segments
			GROUP BY meeting_id
		) AS transcript
		WHERE meeting.id = transcript.meeting_id;

		SET CONSTRAINTS ALL IMMEDIATE;

		ALTER TABLE meetings
			ALTER COLUMN quota_period_start SET NOT NULL,
			ALTER COLUMN quota_period_end SET NOT NULL,
			ALTER COLUMN quota_status SET NOT NULL,
			ALTER COLUMN quota_status SET DEFAULT 'active',
			ALTER COLUMN quota_expires_at SET NOT NULL,
			DROP CONSTRAINT IF EXISTS fk_meeting_usage_reservation;

		ALTER TABLE meeting_usage_periods RENAME TO user_meeting_monthly_quotas;

		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'orders' AND column_name = 'usage_period_id'
			) THEN
				ALTER TABLE orders RENAME COLUMN usage_period_id TO monthly_quota_id;
			END IF;
		END $$;

		DROP TABLE IF EXISTS meeting_transcript_batches;
		DROP TABLE IF EXISTS meeting_transcript_segments;
		DROP TABLE IF EXISTS meeting_usage_records;
		DROP TABLE IF EXISTS meeting_usage_reservations;
		DROP TABLE IF EXISTS user_meeting_quota_overrides;
	`).Error
}

// compactLegacyMeetingSummaries is an idempotent bridge for development
// databases created before the current-summary fields moved into meetings.
// The SQL migration remains the deployable schema contract; this bridge keeps
// the repository's explicitly requested startup AutoMigrate workflow safe.
func compactLegacyMeetingSummaries(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("meeting_summaries") {
		return nil
	}
	if err := tx.Exec(`
		WITH latest AS (
			SELECT DISTINCT ON (meeting_id)
				meeting_id, version, source_transcript_revision, idempotency_key,
				status, topic, abstract, key_discussions, decisions, action_items,
				risks, provider, model_name, prompt_version, input_tokens,
				output_tokens, failure_reason, generated_at
			FROM meeting_summaries
			ORDER BY meeting_id, version DESC
		)
		UPDATE meetings AS m
		SET summary_status = latest.status,
			summary_version = latest.version,
			summary_source_transcript_revision = latest.source_transcript_revision,
			summary_idempotency_key = latest.idempotency_key,
			summary_content = jsonb_build_object(
				'topic', latest.topic,
				'abstract', latest.abstract,
				'key_discussions', latest.key_discussions,
				'decisions', latest.decisions,
				'action_items', latest.action_items,
				'risks', latest.risks
			),
			summary_provider = latest.provider,
			summary_model_name = latest.model_name,
			summary_prompt_version = latest.prompt_version,
			summary_input_tokens = latest.input_tokens,
			summary_output_tokens = latest.output_tokens,
			summary_failure_reason = latest.failure_reason,
			summary_generated_at = latest.generated_at
		FROM latest
		WHERE m.id = latest.meeting_id
	`).Error; err != nil {
		return err
	}
	return tx.Migrator().DropTable("meeting_summaries")
}

func seedCoreBootstrapData(tx *gorm.DB) error {
	now := time.Now().UTC()
	policy := model.MeetingQuotaPolicy{
		ID:                         1,
		MonthlyAudioSeconds:        7_200,
		MaxMeetingAudioSeconds:     14_400,
		MaxConcurrentMeetings:      1,
		CreateRateLimit:            5,
		CreateRateWindowSeconds:    600,
		PeriodTimezone:             "Asia/Shanghai",
		UsageReportIntervalSeconds: 30,
		ReservationTTLSeconds:      14_430,
		RedisFailurePolicy:         biz.RedisQuotaFailurePolicyDeny.String(),
		Version:                    1,
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&policy).Error; err != nil {
		return err
	}
	return nil
}
