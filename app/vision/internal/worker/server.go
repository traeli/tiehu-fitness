package worker

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

const (
	transcriptionBatchTimeout = 30 * time.Second
	summaryBatchTimeout       = 5 * time.Minute
)

type batchProcessor interface {
	ProcessBatch(context.Context, time.Time) (int, error)
}

// Server owns independent bounded loops for reliable transcription delivery
// and slower LLM summary work. Keeping the loops separate prevents a model
// request from delaying a meeting's terminal notification to Core.
type Server struct {
	transcription             batchProcessor
	transcriptionPollInterval time.Duration
	summary                   batchProcessor
	summaryPollInterval       time.Duration
	logger                    *slog.Logger

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
	started     bool
	stopped     bool
}

func NewServer(
	transcription batchProcessor,
	transcriptionPollInterval time.Duration,
	summary batchProcessor,
	summaryPollInterval time.Duration,
	logger *slog.Logger,
) (*Server, error) {
	if transcription == nil || transcriptionPollInterval <= 0 || transcriptionPollInterval > time.Minute {
		return nil, fmt.Errorf("vision outbox worker configuration is invalid")
	}
	if summary != nil && (summaryPollInterval <= 0 || summaryPollInterval > time.Minute) {
		return nil, fmt.Errorf("vision summary worker configuration is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		transcription: transcription, transcriptionPollInterval: transcriptionPollInterval,
		summary: summary, summaryPollInterval: summaryPollInterval,
		logger: logger, done: make(chan struct{}),
	}, nil
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

	var loops sync.WaitGroup
	loops.Add(1)
	go func() {
		defer loops.Done()
		s.runLoop(runCtx, "transcription outbox", s.transcription, s.transcriptionPollInterval, transcriptionBatchTimeout)
	}()
	if s.summary != nil {
		loops.Add(1)
		go func() {
			defer loops.Done()
			s.runLoop(runCtx, "meeting summary", s.summary, s.summaryPollInterval, summaryBatchTimeout)
		}()
	}
	<-runCtx.Done()
	loops.Wait()
	return nil
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

func (s *Server) runLoop(ctx context.Context, name string, processor batchProcessor, pollInterval, batchTimeout time.Duration) {
	s.logger.Info("vision worker loop started", "worker", name, "poll_interval", pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		processed, err := s.runBatch(ctx, name, processor, batchTimeout)
		if processed > 0 {
			s.logger.Info("vision worker batch completed", "worker", name, "processed", processed)
		}
		if err != nil && ctx.Err() == nil {
			s.logger.Error("process vision worker batch", "worker", name, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) runBatch(ctx context.Context, name string, processor batchProcessor, timeout time.Duration) (processed int, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("vision worker panic", "worker", name, "panic", recovered, "stack", string(debug.Stack()))
			err = fmt.Errorf("%s worker panic: %v", name, recovered)
		}
	}()
	batchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return processor.ProcessBatch(batchCtx, time.Now().UTC())
}
