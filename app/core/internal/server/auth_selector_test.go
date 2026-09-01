package server

import (
	"context"
	"testing"

	userv1 "github.com/tiehu-ai/tiehu-fitness/api/user/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

type selectorAuthenticator struct{}

func (selectorAuthenticator) AuthenticateAccessToken(context.Context, string) (*biz.User, error) {
	return &biz.User{ID: "user-1", Status: biz.UserStatusActive}, nil
}

func TestProtectedAccessSelector(t *testing.T) {
	auth := service.NewAccessTokenMiddleware(selectorAuthenticator{})
	middlewareUnderTest := protectedAccess(auth)
	tests := []struct {
		name, operation, authorization, wantReason string
		wantUser                                   bool
	}{
		{name: "utools login remains public", operation: userv1.OperationUserServiceUToolsLogin},
		{name: "meeting requires token", operation: "/meeting.v1.MeetingService/GetMeeting", wantReason: "UNAUTHENTICATED"},
		{name: "profile requires token", operation: userv1.OperationUserServiceGetFitnessProfile, wantReason: "UNAUTHENTICATED"},
		{name: "meeting accepts token", operation: "/meeting.v1.MeetingService/GetMeeting", authorization: "Bearer token", wantUser: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := middlewareUnderTest(func(ctx context.Context, _ any) (any, error) {
				called = true
				_, hasUser := biz.CurrentUserID(ctx)
				if hasUser != tt.wantUser {
					t.Fatalf("current user present = %v, want %v", hasUser, tt.wantUser)
				}
				return nil, nil
			})
			ctx := transport.NewServerContext(context.Background(), &selectorTransport{
				operation: tt.operation, header: selectorHeader{"Authorization": tt.authorization},
			})
			_, err := handler(ctx, nil)
			if kratoserrors.Reason(err) != tt.wantReason {
				t.Fatalf("handler() error = %v", err)
			}
			if tt.wantReason != "" && called {
				t.Fatal("protected handler unexpectedly ran")
			}
			if tt.wantReason == "" && !called {
				t.Fatal("handler did not run")
			}
		})
	}
}

func TestMeetingServiceIsRegisteredOnHTTPAndGRPCServers(t *testing.T) {
	auth := service.NewAccessTokenMiddleware(selectorAuthenticator{})
	userService := service.NewUserService(nil)
	contentService := service.NewContentService(nil)
	meetingService := service.NewMeetingService(nil)
	httpServer, err := NewHTTPServer(&conf.Server{}, testCORSConfig(), auth, userService, contentService, meetingService)
	if err != nil {
		t.Fatal(err)
	}
	foundCreateRoute := false
	if err := httpServer.WalkRoute(func(info kratoshttp.RouteInfo) error {
		if info.Method == "POST" && info.Path == "/v1/meetings" {
			foundCreateRoute = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !foundCreateRoute {
		t.Fatal("meeting create HTTP route is not registered")
	}

	grpcServer := NewGRPCServer(&conf.Server{}, auth, userService, contentService, meetingService, service.NewMeetingIngestInternalService(nil))
	if _, ok := grpcServer.GetServiceInfo()["meeting.v1.MeetingService"]; !ok {
		t.Fatal("meeting gRPC service is not registered")
	}
	if _, ok := grpcServer.GetServiceInfo()["meeting.v1.MeetingIngestInternalService"]; !ok {
		t.Fatal("meeting ingest internal gRPC service is not registered")
	}
}

func testCORSConfig() *conf.HTTPCORS {
	return &conf.HTTPCORS{AllowedOrigins: []string{"http://127.0.0.1:5173"}}
}

type selectorHeader map[string]string

func (h selectorHeader) Get(key string) string      { return h[key] }
func (h selectorHeader) Set(key, value string)      { h[key] = value }
func (h selectorHeader) Add(key, value string)      { h[key] = value }
func (selectorHeader) Keys() []string               { return nil }
func (h selectorHeader) Values(key string) []string { return []string{h[key]} }

type selectorTransport struct {
	operation string
	header    transport.Header
}

func (*selectorTransport) Kind() transport.Kind              { return transport.KindHTTP }
func (*selectorTransport) Endpoint() string                  { return "http://127.0.0.1" }
func (t *selectorTransport) Operation() string               { return t.operation }
func (t *selectorTransport) RequestHeader() transport.Header { return t.header }
func (*selectorTransport) ReplyHeader() transport.Header     { return selectorHeader{} }
