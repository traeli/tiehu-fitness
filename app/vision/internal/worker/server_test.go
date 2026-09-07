package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

type blockingOutboxRepo struct {
	started chan struct{}
}

func (r *blockingOutboxRepo) ClaimTranscriptionDeliveries(ctx context.Context, _ time.Time, _ time.Duration, _ int, _ int32) ([]*biz.TranscriptionOutboxDelivery, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*blockingOutboxRepo) MarkTranscriptionDeliveryDelivered(context.Context, string, time.Time) error {
	return nil
}

func (*blockingOutboxRepo) RetryTranscriptionDelivery(context.Context, string, time.Time, bool) error {
	return nil
}

type unusedCoreMeetingSink struct{}

type batchProcessorFunc func(context.Context, time.Time) (int, error)

func (f batchProcessorFunc) ProcessBatch(ctx context.Context, now time.Time) (int, error) {
	return f(ctx, now)
}

func (unusedCoreMeetingSink) AppendFinalTranscriptSegments(context.Context, string, *biz.TranscriptionSession, []biz.TranscriptSegment) error {
	return nil
}

func (unusedCoreMeetingSink) ReportTranscriptionUsage(context.Context, *biz.TranscriptionSession, time.Duration, time.Time) error {
	return nil
}

func (unusedCoreMeetingSink) CompleteTranscription(context.Context, *biz.TranscriptionSession, time.Duration, time.Duration, time.Time) error {
	return nil
}

func (unusedCoreMeetingSink) FailTranscription(context.Context, *biz.TranscriptionSession, time.Duration, biz.TranscriptionFailureReason, time.Time) error {
	return nil
}

func TestServerStopCancelsRunningBatchAndWaitsForExit(t *testing.T) {
	repo := &blockingOutboxRepo{started: make(chan struct{})}
	uc, err := biz.NewTranscriptionOutboxUsecase(repo, unusedCoreMeetingSink{}, biz.TranscriptionOutboxPolicy{
		LeaseTimeout:   time.Minute,
		BatchSize:      10,
		MaxAttempts:    3,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Minute,
		Audio: biz.AudioSpec{
			Format: biz.AudioFormatPCMS16LE, MIMEType: "audio/pcm", SampleRate: 16_000,
			Channels: 1, ChunkDuration: 200 * time.Millisecond, MaxChunkBytes: 6_400,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(uc, time.Second, nil, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	startResult := make(chan error, 1)
	go func() {
		startResult <- server.Start(context.Background())
	}()

	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start its first batch")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-startResult:
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

func TestServerSlowSummaryDoesNotBlockTranscriptionDelivery(t *testing.T) {
	transcriptionCalls := make(chan struct{}, 4)
	summaryStarted := make(chan struct{})
	transcription := batchProcessorFunc(func(context.Context, time.Time) (int, error) {
		select {
		case transcriptionCalls <- struct{}{}:
		default:
		}
		return 1, nil
	})
	summary := batchProcessorFunc(func(ctx context.Context, _ time.Time) (int, error) {
		select {
		case <-summaryStarted:
		default:
			close(summaryStarted)
		}
		<-ctx.Done()
		return 0, ctx.Err()
	})
	server, err := NewServer(
		transcription,
		10*time.Millisecond,
		summary,
		10*time.Millisecond,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	startResult := make(chan error, 1)
	go func() {
		startResult <- server.Start(context.Background())
	}()

	select {
	case <-summaryStarted:
	case <-time.After(time.Second):
		t.Fatal("summary worker did not start")
	}
	for call := 0; call < 2; call++ {
		select {
		case <-transcriptionCalls:
		case <-time.After(time.Second):
			t.Fatal("slow summary blocked transcription delivery")
		}
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-startResult:
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return after Stop")
	}
}
