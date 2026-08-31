package biz_test

import (
	"context"
	"testing"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

func TestRequireCurrentUserID(t *testing.T) {
	ctx := biz.ContextWithCurrentUser(context.Background(), "user-1")
	if got, err := biz.RequireCurrentUserID(ctx, "user-1"); err != nil || got != "user-1" {
		t.Fatalf("RequireCurrentUserID() = (%q, %v)", got, err)
	}
	if _, err := biz.RequireCurrentUserID(ctx, "user-2"); kratoserrors.Reason(err) != "USER_ACCESS_DENIED" {
		t.Fatalf("mismatched owner error = %v", err)
	}
	if _, err := biz.RequireCurrentUserID(context.Background(), ""); kratoserrors.Reason(err) != "UNAUTHENTICATED" {
		t.Fatalf("missing identity error = %v", err)
	}
}
