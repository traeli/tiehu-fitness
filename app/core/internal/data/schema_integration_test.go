package data

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"
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

func TestAutoMigrateSchemaCreatesCoreTablesAndSeed(t *testing.T) {
	dsn := os.Getenv("CORE_SCHEMA_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("CORE_SCHEMA_TEST_DATABASE_DSN is not set")
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
					results <- fmt.Errorf("core schema migration panic: %v", recovered)
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
	for _, schemaModel := range coreSchemaModels() {
		if !db.Migrator().HasTable(schemaModel) {
			t.Fatalf("AutoMigrateSchema did not create table for %T", schemaModel)
		}
	}
	if db.Migrator().HasTable("meeting_summaries") {
		t.Fatal("legacy meeting_summaries table was not compacted into meetings")
	}
	for _, column := range []string{"summary_content", "summary_provider", "summary_failure_reason"} {
		if !db.Migrator().HasColumn(&model.Meeting{}, column) {
			t.Fatalf("meetings is missing compact summary column %q", column)
		}
	}
	var policy model.MeetingQuotaPolicy
	if err := db.Where("id = ?", 1).Take(&policy).Error; err != nil {
		t.Fatalf("load default meeting quota policy: %v", err)
	}
	if policy.Version != 1 || policy.MaxMeetingAudioSeconds != 14_400 {
		t.Fatalf("unexpected default meeting quota policy: %+v", policy)
	}
}
