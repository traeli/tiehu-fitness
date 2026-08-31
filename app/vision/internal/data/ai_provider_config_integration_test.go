package data

import (
	"context"
	"os"
	"testing"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAIProviderConfigRepoLoadsSeededActiveVersions(t *testing.T) {
	dsn := os.Getenv("VISION_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("VISION_TEST_DATABASE_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open test postgres: %v", err)
	}
	repo, err := NewAIProviderConfigRepo(db)
	if err != nil {
		t.Fatal(err)
	}
	asr, err := repo.GetActiveASRProviderConfig(context.Background())
	if err != nil || asr.Version <= 0 || asr.RealtimeModel == "" {
		t.Fatalf("GetActiveASRProviderConfig() = %+v, %v", asr, err)
	}
	summary, err := repo.GetActiveMeetingSummaryProviderConfig(context.Background())
	if err != nil || summary.Version <= 0 || summary.ModelName == "" {
		t.Fatalf("GetActiveMeetingSummaryProviderConfig() = %+v, %v", summary, err)
	}
}

func TestProviderCredentialRepoStoresAndRotatesAPIKey(t *testing.T) {
	dsn := os.Getenv("VISION_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("VISION_TEST_DATABASE_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open test postgres: %v", err)
	}
	cleanup := func() {
		if err := db.Exec("DELETE FROM provider_credentials").Error; err != nil {
			t.Fatalf("clean provider credentials: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	repo, err := NewProviderCredentialRepo(db)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.SetProviderCredential(context.Background(), biz.ProviderCredentialNameDeepSeek, "first-secret-key")
	if err != nil || first.Version != 1 {
		t.Fatalf("SetProviderCredential(first) = %+v, %v", first, err)
	}
	second, err := repo.SetProviderCredential(context.Background(), biz.ProviderCredentialNameDeepSeek, "rotated-secret-key")
	if err != nil || second.Version != 2 {
		t.Fatalf("SetProviderCredential(second) = %+v, %v", second, err)
	}
	loaded, err := repo.GetProviderCredential(context.Background(), biz.ProviderCredentialNameDeepSeek)
	if err != nil || loaded.APIKey != "rotated-secret-key" || loaded.Version != 2 {
		t.Fatalf("GetProviderCredential() = %+v, %v", loaded, err)
	}
	var stored struct {
		APIKey string `gorm:"column:api_key"`
	}
	if err := db.Raw("SELECT api_key FROM provider_credentials WHERE provider = ?", string(biz.ProviderCredentialNameDeepSeek)).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.APIKey != loaded.APIKey {
		t.Fatalf("stored API key = %q, want %q", stored.APIKey, loaded.APIKey)
	}
}
