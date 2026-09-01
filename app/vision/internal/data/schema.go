package data

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const visionSchemaAdvisoryLockKey int64 = 0x7469656875766973

// AutoMigrateSchema synchronizes all Vision-owned GORM models and inserts only
// the non-secret provider defaults required for an empty database to start.
// Provider API keys are never generated or overwritten here.
func AutoMigrateSchema(ctx context.Context, db *gorm.DB) error {
	if ctx == nil {
		return fmt.Errorf("vision schema migration context is required")
	}
	if db == nil {
		return fmt.Errorf("vision schema migration database is required")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", visionSchemaAdvisoryLockKey).Error; err != nil {
			return fmt.Errorf("lock vision schema migration: %w", err)
		}
		if err := resetLegacyEncryptedProviderCredentials(tx); err != nil {
			return fmt.Errorf("reset legacy encrypted provider credentials: %w", err)
		}
		if err := tx.AutoMigrate(visionSchemaModels()...); err != nil {
			return fmt.Errorf("auto migrate vision schema: %w", err)
		}
		if err := seedVisionBootstrapData(tx); err != nil {
			return fmt.Errorf("seed vision bootstrap data: %w", err)
		}
		return nil
	})
}

// resetLegacyEncryptedProviderCredentials removes the obsolete ciphertext
// schema. SQL cannot recover plaintext without the local encryption key, so
// operators must configure both provider keys again after this one-time reset.
func resetLegacyEncryptedProviderCredentials(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("provider_credentials") ||
		!tx.Migrator().HasColumn("provider_credentials", "api_key_ciphertext") {
		return nil
	}
	return tx.Migrator().DropTable("provider_credentials")
}

func visionSchemaModels() []any {
	return []any{
		&model.MediaAsset{},
		&model.ModelVersion{},
		&model.AnalysisJob{},
		&model.EquipmentRecognitionResult{},
		&model.PostureAnalysisResult{},
		&model.ASRProviderConfig{},
		&model.MeetingSummaryProviderConfig{},
		&model.TranscriptionSession{},
		&model.TranscriptionAudioChunk{},
		&model.ASRJob{},
		&model.AIJobAttempt{},
		&model.TranscriptionFinalSegment{},
		&model.TranscriptionOutbox{},
		&model.MeetingSummaryJob{},
		&model.ProviderCredential{},
	}
}

func seedVisionBootstrapData(tx *gorm.DB) error {
	now := time.Now().UTC()
	var asrCount int64
	if err := tx.Model(&model.ASRProviderConfig{}).Count(&asrCount).Error; err != nil {
		return err
	}
	if asrCount == 0 {
		config := model.ASRProviderConfig{
			ID: "10000000-0000-4000-8000-000000000001", Version: 1,
			Status: string(biz.AIProviderConfigStatusActive), Provider: string(biz.ASRProviderNameBailianParaformer), WorkspaceID: "ws-ow92dz82zf0fph67",
			RealtimeModel: "paraformer-realtime-v2", FileModel: "paraformer-v2",
			ActivatedAt: &now, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&config).Error; err != nil {
			return err
		}
	}
	var summaryCount int64
	if err := tx.Model(&model.MeetingSummaryProviderConfig{}).Count(&summaryCount).Error; err != nil {
		return err
	}
	if summaryCount == 0 {
		config := model.MeetingSummaryProviderConfig{
			ID: "20000000-0000-4000-8000-000000000001", Version: 1,
			Status: string(biz.AIProviderConfigStatusActive), Provider: string(biz.MeetingSummaryProviderNameDeepSeek), ModelName: "deepseek-v4-flash",
			PromptVersion: "meeting-summary-v2", MaxInputCharsPerChunk: 60_000,
			MaxChunks: 64, MaxOutputTokens: 8_192,
			ActivatedAt: &now, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&config).Error; err != nil {
			return err
		}
	}
	return activateMeetingSummaryPromptV2(tx, now)
}

// activateMeetingSummaryPromptV2 clones the immutable active v1 row instead
// of mutating model settings referenced by historical summary jobs. The schema
// advisory lock held by AutoMigrateSchema serializes this one-time transition.
func activateMeetingSummaryPromptV2(tx *gorm.DB, now time.Time) error {
	var active model.MeetingSummaryProviderConfig
	if err := tx.Where("status = ?", string(biz.AIProviderConfigStatusActive)).Take(&active).Error; err != nil {
		return fmt.Errorf("load active meeting summary provider config: %w", err)
	}
	if active.PromptVersion == "meeting-summary-v2" {
		return nil
	}
	if active.PromptVersion != "meeting-summary-v1" {
		return nil
	}
	var maximumVersion int64
	if err := tx.Model(&model.MeetingSummaryProviderConfig{}).
		Select("COALESCE(MAX(version), 0)").Scan(&maximumVersion).Error; err != nil {
		return fmt.Errorf("load maximum meeting summary provider version: %w", err)
	}
	result := tx.Model(&model.MeetingSummaryProviderConfig{}).
		Where("id = ? AND status = ?", active.ID, string(biz.AIProviderConfigStatusActive)).
		Updates(map[string]any{
			"status":     string(biz.AIProviderConfigStatusRetired),
			"updated_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("retire meeting summary prompt v1 config: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("retire meeting summary prompt v1 config: active row changed concurrently")
	}
	next := model.MeetingSummaryProviderConfig{
		ID: uuid.NewString(), Version: maximumVersion + 1,
		Status: string(biz.AIProviderConfigStatusActive), Provider: active.Provider,
		ModelName: active.ModelName, PromptVersion: "meeting-summary-v2",
		MaxInputCharsPerChunk: active.MaxInputCharsPerChunk, MaxChunks: active.MaxChunks,
		MaxOutputTokens: active.MaxOutputTokens, ActivatedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(&next).Error; err != nil {
		return fmt.Errorf("activate meeting summary prompt v2 config: %w", err)
	}
	return nil
}
