package service

import (
	"context"
	"testing"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
)

type fakeAccessAuthenticator struct {
	user *biz.User
	err  error
}

func (f fakeAccessAuthenticator) AuthenticateAccessToken(context.Context, string) (*biz.User, error) {
	return f.user, f.err
}

func TestAccessTokenMiddlewareInjectsCurrentUser(t *testing.T) {
	middlewareUnderTest := NewAccessTokenMiddleware(fakeAccessAuthenticator{
		user: &biz.User{ID: "user-1", Status: biz.UserStatusActive},
	})
	handler := middlewareUnderTest(func(ctx context.Context, _ any) (any, error) {
		userID, ok := biz.CurrentUserID(ctx)
		if !ok || userID != "user-1" {
			t.Fatalf("current user = (%q, %v)", userID, ok)
		}
		return "ok", nil
	})
	ctx := transport.NewServerContext(context.Background(), &authTestTransport{
		header: authTestHeader{"Authorization": "Bearer access-token"},
	})
	reply, err := handler(ctx, nil)
	if err != nil || reply != "ok" {
		t.Fatalf("handler() = (%v, %v)", reply, err)
	}
}

func TestAccessTokenMiddlewareRejectsMissingOrMalformedBearer(t *testing.T) {
	tests := []string{"", "Basic token", "Bearer", "Bearer one two"}
	for _, header := range tests {
		t.Run(header, func(t *testing.T) {
			middlewareUnderTest := NewAccessTokenMiddleware(fakeAccessAuthenticator{})
			handler := middlewareUnderTest(func(context.Context, any) (any, error) {
				t.Fatal("protected handler must not run")
				return nil, nil
			})
			ctx := transport.NewServerContext(context.Background(), &authTestTransport{
				header: authTestHeader{"Authorization": header},
			})
			_, err := handler(ctx, nil)
			if kratoserrors.Reason(err) != "UNAUTHENTICATED" {
				t.Fatalf("handler() error = %v", err)
			}
		})
	}
}

type authTestHeader map[string]string

func (h authTestHeader) Get(key string) string      { return h[key] }
func (h authTestHeader) Set(key, value string)      { h[key] = value }
func (h authTestHeader) Add(key, value string)      { h[key] = value }
func (h authTestHeader) Keys() []string             { return nil }
func (h authTestHeader) Values(key string) []string { return []string{h[key]} }

type authTestTransport struct{ header transport.Header }

func (*authTestTransport) Kind() transport.Kind              { return transport.KindHTTP }
func (*authTestTransport) Endpoint() string                  { return "http://127.0.0.1" }
func (*authTestTransport) Operation() string                 { return "/meeting.v1.MeetingService/GetMeeting" }
func (t *authTestTransport) RequestHeader() transport.Header { return t.header }
func (*authTestTransport) ReplyHeader() transport.Header     { return authTestHeader{} }
