package biz

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type outboxFakeRepo struct {
	deliveries []*TranscriptionOutboxDelivery
	delivered  []string
	retried    []string
	terminal   bool
}

func (r *outboxFakeRepo) ClaimTranscriptionDeliveries(context.Context, time.Time, time.Duration, int, int32) ([]*TranscriptionOutboxDelivery, error) {
	return r.deliveries, nil
}

func (r *outboxFakeRepo) MarkTranscriptionDeliveryDelivered(_ context.Context, id string, _ time.Time) error {
	r.delivered = append(r.delivered, id)
	return nil
}

func (r *outboxFakeRepo) RetryTranscriptionDelivery(_ context.Context, id string, _ time.Time, terminal bool) error {
	r.retried = append(r.retried, id)
	r.terminal = terminal
	return nil
}

type outboxFakeSink struct {
	appends   int
	reports   int
	completes int
	fails     int
	err       error
}

func (s *outboxFakeSink) AppendFinalTranscriptSegments(context.Context, string, *TranscriptionSession, []TranscriptSegment) error {
	s.appends++
	return s.err
}

func (s *outboxFakeSink) ReportTranscriptionUsage(context.Context, *TranscriptionSession, time.Duration, time.Time) error {
	s.reports++
	return s.err
}

func (s *outboxFakeSink) CompleteTranscription(context.Context, *TranscriptionSession, time.Duration, time.Duration, time.Time) error {
	s.completes++
	return s.err
}

func (s *outboxFakeSink) FailTranscription(context.Context, *TranscriptionSession, time.Duration, TranscriptionFailureReason, time.Time) error {
	s.fails++
	return s.err
}

func TestTranscriptionOutboxDeliversFinalSegmentsThenCompletion(t *testing.T) {
	now := time.Now().UTC()
	session := &TranscriptionSession{
		ID: uuid.NewString(), MeetingID: uuid.NewString(), ReservationID: uuid.NewString(),
		Status: TranscriptionSessionStatusSucceeded, AcceptedAudioBytes: 32_000, UpdatedAt: now,
	}
	repo := &outboxFakeRepo{deliveries: []*TranscriptionOutboxDelivery{
		{ID: uuid.NewString(), Type: TranscriptionOutboxEventTypeFinalTranscriptReady, AttemptCount: 0, Session: session, Segments: []TranscriptSegment{{
			ID: uuid.NewString(), SessionID: session.ID, Sequence: 1, EndOffset: time.Second,
			Content: "final", Language: MeetingLanguageEnUS, Confidence: 0.9, CreatedAt: now,
		}}},
		{ID: uuid.NewString(), Type: TranscriptionOutboxEventTypeUsageReady, AttemptCount: 0, Session: session},
	}}
	sink := &outboxFakeSink{}
	uc := mustOutboxUsecase(t, repo, sink, 3)
	delivered, err := uc.ProcessBatch(context.Background(), now)
	if err != nil || delivered != 2 || sink.appends != 1 || sink.reports != 1 || sink.completes != 1 || sink.fails != 0 || len(repo.delivered) != 2 {
		t.Fatalf("ProcessBatch() = delivered %d, error %v, sink %#v, repo %#v", delivered, err, sink, repo)
	}
}

func TestTranscriptionOutboxRetriesAndStopsAtConfiguredAttempt(t *testing.T) {
	now := time.Now().UTC()
	session := &TranscriptionSession{ID: uuid.NewString(), MeetingID: uuid.NewString(), ReservationID: uuid.NewString(), Status: TranscriptionSessionStatusSucceeded, UpdatedAt: now}
	repo := &outboxFakeRepo{deliveries: []*TranscriptionOutboxDelivery{{
		ID: uuid.NewString(), Type: TranscriptionOutboxEventTypeUsageReady, AttemptCount: 2, Session: session,
	}}}
	sink := &outboxFakeSink{err: stderrors.New("core unavailable")}
	uc := mustOutboxUsecase(t, repo, sink, 3)
	delivered, err := uc.ProcessBatch(context.Background(), now)
	if err == nil || delivered != 0 || len(repo.retried) != 1 || !repo.terminal || len(repo.delivered) != 0 {
		t.Fatalf("ProcessBatch() = delivered %d, error %v, sink %#v, repo %#v", delivered, err, sink, repo)
	}
}

func TestTranscriptionOutboxFailureUsesStableFailureDelivery(t *testing.T) {
	now := time.Now().UTC()
	session := &TranscriptionSession{
		ID: uuid.NewString(), MeetingID: uuid.NewString(), ReservationID: uuid.NewString(),
		Status: TranscriptionSessionStatusFailed, FailureCode: "ASR_FINISH_FAILED", UpdatedAt: now,
	}
	repo := &outboxFakeRepo{deliveries: []*TranscriptionOutboxDelivery{{
		ID: uuid.NewString(), Type: TranscriptionOutboxEventTypeUsageReady, AttemptCount: 0, Session: session,
	}}}
	sink := &outboxFakeSink{}
	uc := mustOutboxUsecase(t, repo, sink, 3)
	if delivered, err := uc.ProcessBatch(context.Background(), now); err != nil || delivered != 1 || sink.reports != 1 || sink.fails != 1 || sink.completes != 0 {
		t.Fatalf("ProcessBatch(failure) = %d, %v, sink %#v", delivered, err, sink)
	}
}

func mustOutboxUsecase(t *testing.T, repo TranscriptionOutboxRepo, sink CoreMeetingIngestSink, maxAttempts int32) *TranscriptionOutboxUsecase {
	t.Helper()
	uc, err := NewTranscriptionOutboxUsecase(repo, sink, TranscriptionOutboxPolicy{
		LeaseTimeout: time.Minute, BatchSize: 10, MaxAttempts: maxAttempts,
		InitialBackoff: time.Second, MaxBackoff: time.Minute, Audio: validTranscriptionPolicy().Audio,
	})
	if err != nil {
		t.Fatal(err)
	}
	return uc
}
