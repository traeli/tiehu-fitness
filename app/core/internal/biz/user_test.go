package biz_test

import (
	"context"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data"
)

type fakeWechatProvider struct{}

func (fakeWechatProvider) ExchangeCode(context.Context, string) (*biz.WechatIdentity, error) {
	return &biz.WechatIdentity{AppID: "miniapp-1", OpenID: "openid-1"}, nil
}

func TestWechatLoginAutomaticallyRegistersOnce(t *testing.T) {
	uc := biz.NewUserUsecase(fakeWechatProvider{}, data.NewUserRepo(), time.Hour, 24*time.Hour)
	first, err := uc.WechatLogin(context.Background(), "code-1", "device-1")
	if err != nil {
		t.Fatalf("WechatLogin() error = %v", err)
	}
	if !first.IsNewUser || !first.OnboardingRequired {
		t.Fatalf("first login = %#v", first)
	}
	second, err := uc.WechatLogin(context.Background(), "code-2", "device-1")
	if err != nil {
		t.Fatalf("WechatLogin() second error = %v", err)
	}
	if second.IsNewUser || second.User.ID != first.User.ID {
		t.Fatalf("second login = %#v", second)
	}
}

func TestProfileAndWorkoutPlanStayInUserDomain(t *testing.T) {
	uc := biz.NewUserUsecase(fakeWechatProvider{}, data.NewUserRepo(), time.Hour, 24*time.Hour)
	login, err := uc.WechatLogin(context.Background(), "code", "device")
	if err != nil {
		t.Fatal(err)
	}
	profile := &biz.FitnessProfile{UserID: login.User.ID, Goal: "增肌", DaysPerWeek: 3, OnboardingCompleted: true}
	if err := uc.UpdateProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	plan, err := uc.GeneratePlan(context.Background(), login.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Goal != "增肌" || plan.UserID != login.User.ID {
		t.Fatalf("plan = %#v", plan)
	}
}
