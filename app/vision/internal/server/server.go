package server

import (
	v1 "github.com/tiehu-ai/tiehu-fitness/api/vision/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/go-kratos/kratos/v3/transport/http"
)

func NewHTTPServer(c *conf.Server, svc *service.VisionService) *http.Server {
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
	return srv
}

func NewGRPCServer(c *conf.Server, svc *service.VisionService) *grpc.Server {
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
	return srv
}
