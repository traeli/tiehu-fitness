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
		if err := tx.AutoMigrate(coreSchemaModels()...); err != nil {
			return fmt.Errorf("auto migrate core schema: %w", err)
		}
		if err := compactLegacyMeetingSummaries(tx); err != nil {
			return fmt.Errorf("compact legacy meeting summaries: %w", err)
		}
		if err := seedCoreBootstrapData(tx); err != nil {
			return fmt.Errorf("seed core bootstrap data: %w", err)
		}
		if err := backfillLegacyMeetingQuotaPeriods(tx); err != nil {
			return fmt.Errorf("backfill legacy meeting quota periods: %w", err)
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
		&model.UserMeetingQuotaOverride{},
		&model.MeetingUsagePeriod{},
		&model.Order{},
		&model.MeetingUsageReservation{},
		&model.MeetingUsageRecord{},
		&model.Meeting{},
		&model.MeetingTranscriptSegment{},
		&model.MeetingTranscriptBatch{},
	}
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

// backfillLegacyMeetingQuotaPeriods bridges development databases that were
// auto-migrated before monthly policy snapshots existed. Deployed databases
// use migration 000011, which performs the same deterministic backfill.
func backfillLegacyMeetingQuotaPeriods(tx *gorm.DB) error {
	return tx.Exec(`
		UPDATE meeting_usage_periods AS usage_period
		SET base_quota_seconds = COALESCE(
			(
				SELECT quota_override.monthly_audio_seconds
				FROM user_meeting_quota_overrides AS quota_override
				WHERE quota_override.user_id = usage_period.user_id
				  AND quota_override.status = 'active'
				  AND quota_override.monthly_audio_seconds IS NOT NULL
			),
			(
				SELECT quota_policy.monthly_audio_seconds
				FROM meeting_quota_policies AS quota_policy
				WHERE quota_policy.id = 1
			)
		)
		WHERE usage_period.base_quota_seconds = 0
	`).Error
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
