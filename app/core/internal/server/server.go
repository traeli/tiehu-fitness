package server

import (
	contentv1 "github.com/tiehu-ai/tiehu-fitness/api/content/v1"
	meetingv1 "github.com/tiehu-ai/tiehu-fitness/api/meeting/v1"
	userv1 "github.com/tiehu-ai/tiehu-fitness/api/user/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/middleware/selector"
	"github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/go-kratos/kratos/v3/transport/http"
)

func NewHTTPServer(
	c *conf.Server,
	authMiddleware middleware.Middleware,
	userService *service.UserService,
	contentService *service.ContentService,
	meetingService *service.MeetingService,
) *http.Server {
	opts := []http.ServerOption{http.Middleware(recovery.Recovery(), protectedAccess(authMiddleware))}
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
	userv1.RegisterUserServiceHTTPServer(srv, userService)
	contentv1.RegisterContentServiceHTTPServer(srv, contentService)
	meetingv1.RegisterMeetingServiceHTTPServer(srv, meetingService)
	return srv
}

func NewGRPCServer(
	c *conf.Server,
	authMiddleware middleware.Middleware,
	userService *service.UserService,
	contentService *service.ContentService,
	meetingService *service.MeetingService,
	meetingIngestService *service.MeetingIngestInternalService,
) *grpc.Server {
	opts := []grpc.ServerOption{grpc.Middleware(recovery.Recovery(), protectedAccess(authMiddleware))}
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
	userv1.RegisterUserServiceServer(srv, userService)
	contentv1.RegisterContentServiceServer(srv, contentService)
	meetingv1.RegisterMeetingServiceServer(srv, meetingService)
	meetingv1.RegisterMeetingIngestInternalServiceServer(srv, meetingIngestService)
	return srv
}

func protectedAccess(authMiddleware middleware.Middleware) middleware.Middleware {
	return selector.Server(authMiddleware).
		Prefix("/meeting.v1.MeetingService/").
		Path(
			userv1.OperationUserServiceGetFitnessProfile,
			userv1.OperationUserServiceUpdateFitnessProfile,
			userv1.OperationUserServiceGenerateWorkoutPlan,
			userv1.OperationUserServiceGetWorkoutPlan,
			userv1.OperationUserServiceCheckIn,
		).
		Build()
}
