package worker

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
)

// MeetingQuotaReconciler owns periodic release and settlement of expired
// meeting reservations so recovery does not depend on a later user request.
type MeetingQuotaReconciler struct {
	usecase      *biz.MeetingQuotaUsecase
	pollInterval time.Duration
	batchSize    int
	logger       *slog.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
	stopped bool
}

func NewMeetingQuotaReconciler(usecase *biz.MeetingQuotaUsecase, pollInterval time.Duration, batchSize int, logger *slog.Logger) (*MeetingQuotaReconciler, error) {
	if usecase == nil || pollInterval <= 0 || pollInterval > time.Minute || batchSize <= 0 || batchSize > 1_000 {
		return nil, fmt.Errorf("meeting quota reconciliation worker configuration is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &MeetingQuotaReconciler{
		usecase: usecase, pollInterval: pollInterval, batchSize: batchSize,
		logger: logger, done: make(chan struct{}),
	}, nil
}

func (w *MeetingQuotaReconciler) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("meeting quota reconciliation start context is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		cancel()
		return fmt.Errorf("meeting quota reconciliation worker has already started")
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

	w.logger.Info("meeting quota reconciliation worker started", "poll_interval", w.pollInterval, "batch_size", w.batchSize)
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.runBatch(runCtx); err != nil && runCtx.Err() == nil {
			w.logger.Error("reconcile expired meeting quota reservations", "error", err)
		}
		select {
		case <-runCtx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *MeetingQuotaReconciler) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("meeting quota reconciliation stop context is required")
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
		return fmt.Errorf("stop meeting quota reconciliation worker: %w", ctx.Err())
	}
}

func (w *MeetingQuotaReconciler) runBatch(ctx context.Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			w.logger.Error("meeting quota reconciliation worker panic", "panic", recovered, "stack", string(debug.Stack()))
			err = fmt.Errorf("meeting quota reconciliation worker panic: %v", recovered)
		}
	}()
	batchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	completed, err := w.usecase.ReconcileExpired(batchCtx, time.Now().UTC(), w.batchSize)
	if err == nil && completed > 0 {
		w.logger.Info("expired meeting quota reservations reconciled", "reservations", completed)
	}
	return err
}
