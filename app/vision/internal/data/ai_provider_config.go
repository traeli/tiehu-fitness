package data

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/model"
	"gorm.io/gorm"
)

type AIProviderConfigRepo struct {
	db *gorm.DB
}

var _ biz.ASRProviderConfigRepo = (*AIProviderConfigRepo)(nil)
var _ biz.MeetingSummaryProviderConfigRepo = (*AIProviderConfigRepo)(nil)

func NewAIProviderConfigRepo(db *gorm.DB) (*AIProviderConfigRepo, error) {
	if db == nil {
		return nil, fmt.Errorf("AI provider config database is required")
	}
	return &AIProviderConfigRepo{db: db}, nil
}

func (r *AIProviderConfigRepo) GetActiveASRProviderConfig(ctx context.Context) (*biz.ASRProviderConfig, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	var row model.ASRProviderConfig
	if err := r.db.WithContext(ctx).Where("status = ?", string(biz.AIProviderConfigStatusActive)).Take(&row).Error; err != nil {
		return nil, translateAIProviderConfigDBError("load active ASR provider config", err)
	}
	return asrProviderConfigModelToBiz(&row)
}

func (r *AIProviderConfigRepo) GetASRProviderConfig(ctx context.Context, configID string) (*biz.ASRProviderConfig, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if _, err := uuid.Parse(configID); err != nil {
		return nil, fmt.Errorf("ASR provider config ID is invalid: %w", err)
	}
	var row model.ASRProviderConfig
	if err := r.db.WithContext(ctx).Where("id = ?", configID).Take(&row).Error; err != nil {
		return nil, translateAIProviderConfigDBError("load ASR provider config", err)
	}
	return asrProviderConfigModelToBiz(&row)
}

func (r *AIProviderConfigRepo) GetActiveMeetingSummaryProviderConfig(ctx context.Context) (*biz.MeetingSummaryProviderConfig, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	var row model.MeetingSummaryProviderConfig
	if err := r.db.WithContext(ctx).Where("status = ?", string(biz.AIProviderConfigStatusActive)).Take(&row).Error; err != nil {
		return nil, translateAIProviderConfigDBError("load active meeting summary provider config", err)
	}
	return meetingSummaryProviderConfigModelToBiz(&row)
}

func (r *AIProviderConfigRepo) GetMeetingSummaryProviderConfig(ctx context.Context, configID string) (*biz.MeetingSummaryProviderConfig, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if _, err := uuid.Parse(configID); err != nil {
		return nil, fmt.Errorf("meeting summary provider config ID is invalid: %w", err)
	}
	var row model.MeetingSummaryProviderConfig
	if err := r.db.WithContext(ctx).Where("id = ?", configID).Take(&row).Error; err != nil {
		return nil, translateAIProviderConfigDBError("load meeting summary provider config", err)
	}
	return meetingSummaryProviderConfigModelToBiz(&row)
}

func asrProviderConfigModelToBiz(row *model.ASRProviderConfig) (*biz.ASRProviderConfig, error) {
	if row == nil {
		return nil, fmt.Errorf("ASR provider config row is required")
	}
	status, err := biz.ParseAIProviderConfigStatus(row.Status)
	if err != nil {
		return nil, err
	}
	provider, err := biz.ParseASRProviderName(row.Provider)
	if err != nil {
		return nil, err
	}
	config := &biz.ASRProviderConfig{
		ID: row.ID, Version: row.Version, Status: status, Provider: provider,
		WorkspaceID: row.WorkspaceID, RealtimeModel: row.RealtimeModel, FileModel: row.FileModel,
		VocabularyID: row.VocabularyID, ActivatedAt: row.ActivatedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("stored ASR provider config is invalid: %w", err)
	}
	return config, nil
}

func meetingSummaryProviderConfigModelToBiz(row *model.MeetingSummaryProviderConfig) (*biz.MeetingSummaryProviderConfig, error) {
	if row == nil {
		return nil, fmt.Errorf("meeting summary provider config row is required")
	}
	status, err := biz.ParseAIProviderConfigStatus(row.Status)
	if err != nil {
		return nil, err
	}
	provider, err := biz.ParseMeetingSummaryProviderName(row.Provider)
	if err != nil {
		return nil, err
	}
	config := &biz.MeetingSummaryProviderConfig{
		ID: row.ID, Version: row.Version, Status: status, Provider: provider,
		ModelName: row.ModelName, PromptVersion: row.PromptVersion,
		MaxInputCharsPerChunk: row.MaxInputCharsPerChunk, MaxChunks: row.MaxChunks,
		MaxOutputTokens: row.MaxOutputTokens, ActivatedAt: row.ActivatedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("stored meeting summary provider config is invalid: %w", err)
	}
	return config, nil
}

func translateAIProviderConfigDBError(operation string, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%s: no matching provider config", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
