package data

import (
	"context"
	stderrors "errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTranscriptionRepoPostgresLifecycle(t *testing.T) {
	dsn := os.Getenv("VISION_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("VISION_TEST_DATABASE_DSN is not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open test postgres: %v", err)
	}
	cleanup := func() {
		if err := db.Exec("TRUNCATE transcription_outbox, transcription_final_segments, ai_job_attempts, asr_jobs, transcription_audio_chunks, transcription_sessions CASCADE").Error; err != nil {
			t.Fatalf("clean transcription tables: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	repo, err := NewTranscriptionRepo(db)
	if err != nil {
		t.Fatalf("NewTranscriptionRepo() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	session := &biz.TranscriptionSession{
		ID: uuid.NewString(), ProviderConfigID: "10000000-0000-4000-8000-000000000001",
		MeetingID: uuid.NewString(), UserID: uuid.NewString(), ReservationID: uuid.NewString(),
		Language: biz.MeetingLanguageZhCN, Status: biz.TranscriptionSessionStatusPending,
		Provider: biz.ASRProviderNameBailianParaformer, IdempotencyKey: uuid.NewString(),
		GrantedAudioDuration: biz.GrantedAudioDuration(time.Second), CreatedAt: now, UpdatedAt: now,
	}
	created, wasCreated, err := repo.CreateOrGet(context.Background(), session)
	if err != nil || !wasCreated {
		t.Fatalf("CreateOrGet() = %v, %v, %v", created, wasCreated, err)
	}
	repeated, wasCreated, err := repo.CreateOrGet(context.Background(), session)
	if err != nil || wasCreated || repeated.ID != created.ID {
		t.Fatalf("CreateOrGet() repeat = %v, %v, %v", repeated, wasCreated, err)
	}
	if _, err := repo.Transition(context.Background(), session.ID, []biz.TranscriptionSessionStatus{biz.TranscriptionSessionStatusPending}, biz.TranscriptionSessionStatusConnecting, ""); err != nil {
		t.Fatalf("Transition(connecting) error = %v", err)
	}
	attempt, err := repo.StartAttempt(context.Background(), session.ID, biz.ASRProviderNameBailianParaformer)
	if err != nil || attempt.AttemptNumber != 1 || attempt.Status != biz.ASRAttemptStatusProcessing {
		t.Fatalf("StartAttempt() = %+v, %v", attempt, err)
	}
	if _, err := repo.Transition(context.Background(), session.ID, []biz.TranscriptionSessionStatus{biz.TranscriptionSessionStatusConnecting}, biz.TranscriptionSessionStatusStreaming, ""); err != nil {
		t.Fatalf("Transition(streaming) error = %v", err)
	}
	if _, err := repo.AcceptAudio(context.Background(), session.ID, 2, 3_200, 32_000); !stderrors.Is(err, biz.ErrTranscriptionSequence) {
		t.Fatalf("AcceptAudio(out of order) error = %v", err)
	}
	accepted, err := repo.AcceptAudio(context.Background(), session.ID, 1, 3_200, 32_000)
	if err != nil || accepted.Duplicate || accepted.Session.AcceptedAudioBytes != 3_200 {
		t.Fatalf("AcceptAudio() = %+v, %v", accepted, err)
	}
	duplicate, err := repo.AcceptAudio(context.Background(), session.ID, 1, 3_200, 32_000)
	if err != nil || !duplicate.Duplicate || duplicate.Session.AcceptedAudioBytes != 3_200 {
		t.Fatalf("AcceptAudio(duplicate) = %+v, %v", duplicate, err)
	}
	if _, err := repo.Transition(context.Background(), session.ID, []biz.TranscriptionSessionStatus{biz.TranscriptionSessionStatusStreaming}, biz.TranscriptionSessionStatusFinishing, ""); err != nil {
		t.Fatalf("Transition(finishing) error = %v", err)
	}
	segments := []biz.TranscriptSegment{{
		ID: uuid.NewString(), SessionID: session.ID, Sequence: 1, StartOffset: 0, EndOffset: time.Second,
		Content: "测试转写", Language: biz.MeetingLanguageZhCN, Confidence: 0.95, CreatedAt: now,
	}}
	completed, err := repo.Complete(context.Background(), session.ID, segments)
	if err != nil || completed.Status != biz.TranscriptionSessionStatusSucceeded {
		t.Fatalf("Complete() = %+v, %v", completed, err)
	}
	if _, err := repo.Complete(context.Background(), session.ID, segments); err != nil {
		t.Fatalf("Complete() repeat error = %v", err)
	}
	if err := repo.FinishAttempt(context.Background(), attempt.ID, biz.ASRAttemptStatusSucceeded, ""); err != nil {
		t.Fatalf("FinishAttempt() error = %v", err)
	}
	var outboxCount int64
	if err := db.Model(&model.TranscriptionOutbox{}).Where("session_id = ?", session.ID).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if outboxCount != 2 {
		t.Fatalf("outbox count = %d, want 2", outboxCount)
	}
	claimedAt := now.Add(time.Minute)
	firstDelivery, err := repo.ClaimTranscriptionDeliveries(context.Background(), claimedAt, 30*time.Second, 10, 5)
	if err != nil || len(firstDelivery) != 1 || firstDelivery[0].Type != biz.TranscriptionOutboxEventTypeFinalTranscriptReady || len(firstDelivery[0].Segments) != 1 {
		t.Fatalf("first outbox claim = (%#v, %v)", firstDelivery, err)
	}
	if err := repo.MarkTranscriptionDeliveryDelivered(context.Background(), firstDelivery[0].ID, claimedAt); err != nil {
		t.Fatalf("mark final transcript delivered: %v", err)
	}
	secondDelivery, err := repo.ClaimTranscriptionDeliveries(context.Background(), claimedAt.Add(time.Second), 30*time.Second, 10, 5)
	if err != nil || len(secondDelivery) != 1 || secondDelivery[0].Type != biz.TranscriptionOutboxEventTypeUsageReady || secondDelivery[0].Session.Status != biz.TranscriptionSessionStatusSucceeded {
		t.Fatalf("second outbox claim = (%#v, %v)", secondDelivery, err)
	}
	if err := repo.RetryTranscriptionDelivery(context.Background(), secondDelivery[0].ID, claimedAt.Add(2*time.Second), false); err != nil {
		t.Fatalf("retry usage delivery: %v", err)
	}
	retried, err := repo.ClaimTranscriptionDeliveries(context.Background(), claimedAt.Add(3*time.Second), 30*time.Second, 10, 5)
	if err != nil || len(retried) != 1 || retried[0].ID != secondDelivery[0].ID || retried[0].AttemptCount != 1 {
		t.Fatalf("retried outbox claim = (%#v, %v)", retried, err)
	}
}
