package worker

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

// TranscriptionReaper owns cleanup of prepared sessions whose WebSocket ticket
// was never consumed, such as when the desktop process exits during startup.
type TranscriptionReaper struct {
	usecase      *biz.TranscriptionUsecase
	pollInterval time.Duration
	staleAfter   time.Duration
	batchSize    int
	logger       *slog.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
	stopped bool
}

func NewTranscriptionReaper(usecase *biz.TranscriptionUsecase, pollInterval, staleAfter time.Duration, batchSize int, logger *slog.Logger) (*TranscriptionReaper, error) {
	if usecase == nil || pollInterval <= 0 || pollInterval > time.Minute || staleAfter <= 0 || staleAfter > 30*time.Minute || batchSize <= 0 || batchSize > 1_000 {
		return nil, fmt.Errorf("transcription reaper configuration is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TranscriptionReaper{
		usecase: usecase, pollInterval: pollInterval, staleAfter: staleAfter,
		batchSize: batchSize, logger: logger, done: make(chan struct{}),
	}, nil
}

func (w *TranscriptionReaper) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("transcription reaper start context is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		cancel()
		return fmt.Errorf("transcription reaper has already started")
	}
	w.started = true
	w.cancel = cancel
	if w.stopped {
		cancel()
	}
	w.mu.Unlock()
	defer func() {
		cancel()
		close(w.done)
	}()

	w.logger.Info("transcription pending-session reaper started", "poll_interval", w.pollInterval, "stale_after", w.staleAfter)
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.runBatch(runCtx); err != nil && runCtx.Err() == nil {
			w.logger.Error("expire stale pending transcription sessions", "error", err)
		}
		select {
		case <-runCtx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *TranscriptionReaper) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("transcription reaper stop context is required")
	}
	w.mu.Lock()
	w.stopped = true
	cancel := w.cancel
	started := w.started
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !started {
		return nil
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop transcription reaper: %w", ctx.Err())
	}
}

func (w *TranscriptionReaper) runBatch(ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			w.logger.Error("transcription reaper panic", "panic", recovered, "stack", string(debug.Stack()))
			err = fmt.Errorf("transcription reaper panic: %v", recovered)
		}
	}()
	batchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	expired, err := w.usecase.ExpireStalePending(batchCtx, time.Now().UTC().Add(-w.staleAfter), w.batchSize)
	if err == nil && expired > 0 {
		w.logger.Info("stale pending transcription sessions expired", "sessions", expired)
	}
	return err
}
