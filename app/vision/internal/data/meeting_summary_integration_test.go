package data

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestMeetingSummaryDeliveryClaimUsesLease(t *testing.T) {
	dsn := os.Getenv("VISION_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("VISION_TEST_DATABASE_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open test postgres: %v", err)
	}
	var provider model.MeetingSummaryProviderConfig
	if err := db.Where("status = ?", "active").Order("version DESC").Take(&provider).Error; err != nil {
		t.Fatalf("load active summary provider: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	resultJSON, err := json.Marshal(biz.MeetingSummary{
		Topic: "租约测试", Abstract: "验证同一交付任务不会被两个实例同时领取。",
		KeyDiscussions: []string{}, Decisions: []string{}, ActionItems: []biz.MeetingActionItem{}, Risks: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	row := model.MeetingSummaryJob{
		ID: uuid.NewString(), ProviderConfigID: provider.ID,
		MeetingID: uuid.NewString(), UserID: uuid.NewString(), Version: 1,
		SourceTranscriptRevision: 1, Language: string(biz.MeetingLanguageAuto),
		IdempotencyKey: uuid.NewString(), Status: string(biz.MeetingSummaryJobStatusDeliveryPending),
		Provider: provider.Provider, ModelName: provider.ModelName, PromptVersion: provider.PromptVersion,
		ResultJSON: resultJSON, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("insert summary delivery job: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Delete(&model.MeetingSummaryJob{}, "id = ?", row.ID).Error; err != nil {
			t.Fatalf("delete summary delivery job: %v", err)
		}
	})

	repo, err := NewMeetingSummaryRepo(db)
	if err != nil {
		t.Fatal(err)
	}
	const lease = time.Minute
	first, err := repo.ClaimJobs(context.Background(), now, lease, 1)
	if err != nil || len(first) != 1 || first[0].ID != row.ID {
		t.Fatalf("first ClaimJobs() = %#v, %v", first, err)
	}
	second, err := repo.ClaimJobs(context.Background(), now.Add(time.Second), lease, 1)
	if err != nil || len(second) != 0 {
		t.Fatalf("concurrent ClaimJobs() = %#v, %v; want no leased job", second, err)
	}
	afterLease, err := repo.ClaimJobs(context.Background(), now.Add(lease+time.Second), lease, 1)
	if err != nil || len(afterLease) != 1 || afterLease[0].ID != row.ID {
		t.Fatalf("ClaimJobs(after lease) = %#v, %v", afterLease, err)
	}
}
