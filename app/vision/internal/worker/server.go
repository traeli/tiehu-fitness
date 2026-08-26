package worker

import (
	"context"
	"log/slog"
)

// Server runs the bounded AI task consumer in the vision-service process.
// Replace the wait with a durable queue subscription and graceful drain.
type Server struct {
	logger *slog.Logger
}

func NewServer(logger *slog.Logger) *Server {
	return &Server{logger: logger}
}

func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("vision worker pool started; queue consumer is not connected yet")
	<-ctx.Done()
	return nil
}

func (s *Server) Stop(context.Context) error {
	s.logger.Info("vision worker pool stopped")
	return nil
}
