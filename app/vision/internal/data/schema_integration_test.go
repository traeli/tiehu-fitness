package data

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAutoMigrateSchemaRejectsMissingDependencies(t *testing.T) {
	if err := AutoMigrateSchema(nil, nil); err == nil {
		t.Fatal("AutoMigrateSchema(nil, nil) expected error")
	}
	if err := AutoMigrateSchema(context.Background(), nil); err == nil {
		t.Fatal("AutoMigrateSchema(ctx, nil) expected error")
	}
}

func TestAutoMigrateSchemaCreatesVisionTablesAndSeeds(t *testing.T) {
	dsn := os.Getenv("VISION_SCHEMA_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("VISION_SCHEMA_TEST_DATABASE_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open test postgres: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	results := make(chan error, 2)
	for range 2 {
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					results <- fmt.Errorf("vision schema migration panic: %v", recovered)
				}
			}()
			results <- AutoMigrateSchema(ctx, db)
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("AutoMigrateSchema(concurrent): %v", err)
		}
	}
	if err := AutoMigrateSchema(ctx, db); err != nil {
		t.Fatalf("AutoMigrateSchema(repeated): %v", err)
	}
	for _, schemaModel := range visionSchemaModels() {
		if !db.Migrator().HasTable(schemaModel) {
			t.Fatalf("AutoMigrateSchema did not create table for %T", schemaModel)
		}
	}
	for _, column := range []string{"llm_request", "llm_response", "llm_http_status", "llm_duration_milliseconds"} {
		if !db.Migrator().HasColumn(&model.MeetingSummaryJob{}, column) {
			t.Fatalf("meeting_summary_jobs is missing LLM exchange column %q", column)
		}
	}
	if !db.Migrator().HasColumn(&model.ProviderCredential{}, "api_key") ||
		db.Migrator().HasColumn(&model.ProviderCredential{}, "api_key_ciphertext") {
		t.Fatal("provider_credentials does not use the plaintext API key schema")
	}
	var asrConfig model.ASRProviderConfig
	if err := db.Where("status = ?", "active").Take(&asrConfig).Error; err != nil {
		t.Fatalf("load default ASR provider config: %v", err)
	}
	if asrConfig.Version != 1 || asrConfig.RealtimeModel != "paraformer-realtime-v2" {
		t.Fatalf("unexpected default ASR provider config: %+v", asrConfig)
	}
	var summaryConfig model.MeetingSummaryProviderConfig
	if err := db.Where("status = ?", "active").Take(&summaryConfig).Error; err != nil {
		t.Fatalf("load default summary provider config: %v", err)
	}
	if summaryConfig.Version != 1 || summaryConfig.ModelName != "deepseek-v4-flash" {
		t.Fatalf("unexpected default summary provider config: %+v", summaryConfig)
	}
}
