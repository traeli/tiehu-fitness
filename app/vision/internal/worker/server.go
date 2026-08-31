package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

// Server owns the bounded transcription outbox polling lifecycle.
type Server struct {
	uc           *biz.TranscriptionOutboxUsecase
	summaryUC    *biz.MeetingSummaryUsecase
	pollInterval time.Duration
	logger       *slog.Logger

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
	started     bool
	stopped     bool
}

func NewServer(uc *biz.TranscriptionOutboxUsecase, pollInterval time.Duration, logger *slog.Logger, summaryUC ...*biz.MeetingSummaryUsecase) (*Server, error) {
	if uc == nil || pollInterval <= 0 || pollInterval > time.Minute {
		return nil, fmt.Errorf("vision outbox worker configuration is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	server := &Server{uc: uc, pollInterval: pollInterval, logger: logger, done: make(chan struct{})}
	if len(summaryUC) > 0 {
		server.summaryUC = summaryUC[0]
	}
	return server, nil
}

func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("vision outbox worker start context is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.lifecycleMu.Lock()
	if s.started {
		s.lifecycleMu.Unlock()
		cancel()
		return fmt.Errorf("vision outbox worker has already started")
	}
	s.started = true
	s.cancel = cancel
	if s.stopped {
		cancel()
	}
	s.lifecycleMu.Unlock()
	defer func() {
		cancel()
		close(s.done)
	}()

	s.logger.Info("vision transcription outbox worker started", "poll_interval", s.pollInterval)
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return nil
		default:
		}
		if _, err := s.runBatch(runCtx); err != nil {
			if runCtx.Err() != nil {
				return nil
			}
			s.logger.Error("process transcription outbox batch", "error", err)
		}
		select {
		case <-runCtx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Server) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("vision outbox worker stop context is required")
	}
	s.lifecycleMu.Lock()
	s.stopped = true
	cancel := s.cancel
	started := s.started
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !started {
		return nil
	}
	select {
	case <-s.done:
		s.logger.Info("vision worker pool stopped")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop vision outbox worker: %w", ctx.Err())
	}
}

func (s *Server) runBatch(ctx context.Context) (delivered int, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("transcription outbox worker panic", "panic", recovered, "stack", string(debug.Stack()))
			err = fmt.Errorf("transcription outbox worker panic: %v", recovered)
		}
	}()
	now := time.Now().UTC()
	delivered, transcriptionErr := s.uc.ProcessBatch(ctx, now)
	if s.summaryUC == nil {
		return delivered, transcriptionErr
	}
	summarized, summaryErr := s.summaryUC.ProcessBatch(ctx, now)
	return delivered + summarized, errors.Join(transcriptionErr, summaryErr)
}
