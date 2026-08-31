package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/model"
	"gorm.io/gorm"
)

type ProviderCredentialRepo struct {
	db *gorm.DB
}

var _ biz.ProviderCredentialRepo = (*ProviderCredentialRepo)(nil)

func NewProviderCredentialRepo(db *gorm.DB) (*ProviderCredentialRepo, error) {
	if db == nil {
		return nil, fmt.Errorf("provider credential database is required")
	}
	return &ProviderCredentialRepo{db: db}, nil
}

func (r *ProviderCredentialRepo) GetProviderCredential(ctx context.Context, provider biz.ProviderCredentialName) (*biz.ProviderCredential, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if _, err := biz.ParseProviderCredentialName(string(provider)); err != nil {
		return nil, err
	}
	var row model.ProviderCredential
	if err := r.db.WithContext(ctx).Where("provider = ?", string(provider)).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("provider credential %q is not configured", provider)
		}
		return nil, fmt.Errorf("load provider credential %q: %w", provider, err)
	}
	credential := &biz.ProviderCredential{
		Provider: provider, APIKey: row.APIKey, Version: row.Version,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if err := credential.Validate(); err != nil {
		return nil, fmt.Errorf("stored provider credential is invalid: %w", err)
	}
	return credential, nil
}

func (r *ProviderCredentialRepo) SetProviderCredential(ctx context.Context, provider biz.ProviderCredentialName, apiKey string) (*biz.ProviderCredential, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	now := time.Now().UTC()
	prospective := &biz.ProviderCredential{Provider: provider, APIKey: apiKey, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := prospective.Validate(); err != nil {
		return nil, err
	}
	var stored model.ProviderCredential
	err := r.db.WithContext(ctx).Raw(`
		INSERT INTO provider_credentials (
			provider, api_key, version, created_at, updated_at
		) VALUES (?, ?, 1, ?, ?)
		ON CONFLICT (provider) DO UPDATE SET
			api_key = EXCLUDED.api_key,
			version = provider_credentials.version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING provider, api_key, version, created_at, updated_at
	`, string(provider), apiKey, now, now).Scan(&stored).Error
	if err != nil {
		return nil, fmt.Errorf("store provider credential %q: %w", provider, err)
	}
	return &biz.ProviderCredential{
		Provider: provider, APIKey: stored.APIKey, Version: stored.Version,
		CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt,
	}, nil
}
