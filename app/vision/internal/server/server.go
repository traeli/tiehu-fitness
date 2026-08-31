package server

import (
	"log/slog"

	v1 "github.com/tiehu-ai/tiehu-fitness/api/vision/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/go-kratos/kratos/v3/transport/http"
)

func NewHTTPServer(c *conf.Server, realtimeCfg *conf.RealtimeTranscription, svc *service.VisionService, realtimeSvc *service.RealtimeTranscriptionService, logger *slog.Logger) (*http.Server, *RealtimeWebSocketHandler, error) {
	opts := []http.ServerOption{http.Middleware(recovery.Recovery())}
	if hc := c.GetHttp(); hc != nil {
		if hc.Network != "" {
			opts = append(opts, http.Network(hc.Network))
		}
		if hc.Addr != "" {
			opts = append(opts, http.Address(hc.Addr))
		}
		if hc.Timeout != nil {
			opts = append(opts, http.Timeout(hc.Timeout.AsDuration()))
		}
	}
	srv := http.NewServer(opts...)
	v1.RegisterVisionServiceHTTPServer(srv, svc)
	realtimeHandler, err := NewRealtimeWebSocketHandler(realtimeCfg, realtimeSvc, logger)
	if err != nil {
		return nil, nil, err
	}
	srv.Route("/v1/realtime").GET("/transcriptions", realtimeHandler.Handle)
	return srv, realtimeHandler, nil
}

func NewGRPCServer(c *conf.Server, svc *service.VisionService, transcriptionSvc *service.MeetingTranscriptionInternalService) *grpc.Server {
	opts := []grpc.ServerOption{grpc.Middleware(recovery.Recovery())}
	if gc := c.GetGrpc(); gc != nil {
		if gc.Network != "" {
			opts = append(opts, grpc.Network(gc.Network))
		}
		if gc.Addr != "" {
			opts = append(opts, grpc.Address(gc.Addr))
		}
		if gc.Timeout != nil {
			opts = append(opts, grpc.Timeout(gc.Timeout.AsDuration()))
		}
	}
	srv := grpc.NewServer(opts...)
	v1.RegisterVisionServiceServer(srv, svc)
	v1.RegisterMeetingTranscriptionInternalServiceServer(srv, transcriptionSvc)
	return srv
}
