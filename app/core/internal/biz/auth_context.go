package biz

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v3/errors"
)

type currentUserContextKey struct{}

// ContextWithCurrentUser stores the authenticated user ID for downstream
// service and biz calls. Only authentication middleware should call it.
func ContextWithCurrentUser(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, currentUserContextKey{}, userID)
}

// CurrentUserID returns the authenticated user ID from context.
func CurrentUserID(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	userID, ok := ctx.Value(currentUserContextKey{}).(string)
	return userID, ok && strings.TrimSpace(userID) != ""
}

// RequireCurrentUserID authorizes an optional path owner and returns the
// authenticated identity as the only trusted user ID.
func RequireCurrentUserID(ctx context.Context, requestedUserID string) (string, error) {
	userID, ok := CurrentUserID(ctx)
	if !ok {
		return "", errors.Unauthorized("UNAUTHENTICATED", "authentication is required")
	}
	requestedUserID = strings.TrimSpace(requestedUserID)
	if requestedUserID != "" && requestedUserID != userID {
		return "", errors.Forbidden("USER_ACCESS_DENIED", "resource does not belong to the current user")
	}
	return userID, nil
}
