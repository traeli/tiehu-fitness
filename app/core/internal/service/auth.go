package service

import (
	"context"
	"strings"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

type accessTokenAuthenticator interface {
	AuthenticateAccessToken(context.Context, string) (*biz.User, error)
}

// NewAccessTokenMiddleware authenticates a Bearer token and injects only the
// verified current user ID into request context.
func NewAccessTokenMiddleware(authenticator accessTokenAuthenticator) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if authenticator == nil {
				return nil, errors.ServiceUnavailable("AUTH_NOT_CONFIGURED", "access authentication is not configured")
			}
			tr, ok := transport.FromServerContext(ctx)
			if !ok || tr.RequestHeader() == nil {
				return nil, errors.Unauthorized("UNAUTHENTICATED", "authentication is required")
			}
			token, err := parseBearerToken(tr.RequestHeader().Get("Authorization"))
			if err != nil {
				return nil, err
			}
			user, err := authenticator.AuthenticateAccessToken(ctx, token)
			if err != nil {
				return nil, err
			}
			if user == nil || strings.TrimSpace(user.ID) == "" {
				return nil, errors.InternalServer("AUTH_USER_INVALID", "authenticated user data is invalid")
			}
			return handler(biz.ContextWithCurrentUser(ctx, user.ID), req)
		}
	}
}

func parseBearerToken(header string) (string, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.Unauthorized("UNAUTHENTICATED", "a Bearer access token is required")
	}
	return parts[1], nil
}
