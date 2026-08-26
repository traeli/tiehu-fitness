package biz

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

type WechatIdentity struct {
	AppID      string
	OpenID     string
	UnionID    string
	SessionKey string
}

type User struct {
	ID        string
	Nickname  string
	AvatarURI string
	Status    string
}

type FitnessProfile struct {
	UserID                  string
	Goal                    string
	ExperienceLevel         string
	DaysPerWeek             int32
	DurationMinutes         int32
	AvailableEquipmentCodes []string
	InjuryNotes             []string
	OnboardingCompleted     bool
}

type WorkoutPlan struct {
	ID     string
	UserID string
	Goal   string
	Status string
}

type Session struct {
	UserID        string
	DeviceID      string
	AccessToken   string
	RefreshToken  string
	AccessExpiry  time.Time
	RefreshExpiry time.Time
}

type LoginResult struct {
	User               *User
	Session            *Session
	IsNewUser          bool
	OnboardingRequired bool
}

type WechatProvider interface {
	ExchangeCode(context.Context, string) (*WechatIdentity, error)
}

type UserRepo interface {
	UpsertWechatUser(context.Context, *WechatIdentity) (*User, bool, error)
	SaveSession(context.Context, *Session) error
	GetSessionByRefreshToken(context.Context, string) (*Session, error)
	DeleteSession(context.Context, string) error
	GetProfile(context.Context, string) (*FitnessProfile, error)
	SaveProfile(context.Context, *FitnessProfile) error
	SavePlan(context.Context, *WorkoutPlan) error
	GetPlan(context.Context, string, string) (*WorkoutPlan, error)
	CheckIn(context.Context, string, time.Time) (int32, error)
}

type UserUsecase struct {
	wechat     WechatProvider
	repo       UserRepo
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewUserUsecase(wechat WechatProvider, repo UserRepo, accessTTL, refreshTTL time.Duration) *UserUsecase {
	if accessTTL <= 0 {
		accessTTL = 2 * time.Hour
	}
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}
	return &UserUsecase{wechat: wechat, repo: repo, accessTTL: accessTTL, refreshTTL: refreshTTL}
}

func (uc *UserUsecase) WechatLogin(ctx context.Context, code, deviceID string) (*LoginResult, error) {
	if strings.TrimSpace(code) == "" {
		return nil, errors.BadRequest("WECHAT_CODE_REQUIRED", "wechat login code is required")
	}
	identity, err := uc.wechat.ExchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}
	user, isNew, err := uc.repo.UpsertWechatUser(ctx, identity)
	if err != nil {
		return nil, err
	}
	session, err := uc.newSession(user.ID, deviceID)
	if err != nil {
		return nil, err
	}
	if err := uc.repo.SaveSession(ctx, session); err != nil {
		return nil, err
	}
	profile, err := uc.repo.GetProfile(ctx, user.ID)
	onboardingRequired := err != nil || !profile.OnboardingCompleted
	return &LoginResult{User: user, Session: session, IsNewUser: isNew, OnboardingRequired: onboardingRequired}, nil
}

func (uc *UserUsecase) RefreshToken(ctx context.Context, refreshToken string) (*Session, error) {
	old, err := uc.repo.GetSessionByRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	if time.Now().After(old.RefreshExpiry) {
		_ = uc.repo.DeleteSession(ctx, refreshToken)
		return nil, errors.Unauthorized("REFRESH_TOKEN_EXPIRED", "refresh token expired")
	}
	next, err := uc.newSession(old.UserID, old.DeviceID)
	if err != nil {
		return nil, err
	}
	if err := uc.repo.DeleteSession(ctx, refreshToken); err != nil {
		return nil, err
	}
	if err := uc.repo.SaveSession(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}

func (uc *UserUsecase) Logout(ctx context.Context, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return errors.BadRequest("REFRESH_TOKEN_REQUIRED", "refresh_token is required")
	}
	return uc.repo.DeleteSession(ctx, refreshToken)
}

func (uc *UserUsecase) GetProfile(ctx context.Context, userID string) (*FitnessProfile, error) {
	return uc.repo.GetProfile(ctx, userID)
}

func (uc *UserUsecase) UpdateProfile(ctx context.Context, profile *FitnessProfile) error {
	if profile == nil || strings.TrimSpace(profile.UserID) == "" {
		return errors.BadRequest("USER_ID_REQUIRED", "user_id is required")
	}
	return uc.repo.SaveProfile(ctx, profile)
}

func (uc *UserUsecase) GeneratePlan(ctx context.Context, userID string) (*WorkoutPlan, error) {
	profile, err := uc.repo.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !profile.OnboardingCompleted || profile.Goal == "" {
		return nil, errors.BadRequest("FITNESS_PROFILE_INCOMPLETE", "complete fitness profile before generating a plan")
	}
	plan := &WorkoutPlan{ID: uuid.NewString(), UserID: userID, Goal: profile.Goal, Status: "draft"}
	if err := uc.repo.SavePlan(ctx, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (uc *UserUsecase) GetPlan(ctx context.Context, userID, planID string) (*WorkoutPlan, error) {
	return uc.repo.GetPlan(ctx, userID, planID)
}

func (uc *UserUsecase) CheckIn(ctx context.Context, userID string) (int32, error) {
	if strings.TrimSpace(userID) == "" {
		return 0, errors.BadRequest("USER_ID_REQUIRED", "user_id is required")
	}
	return uc.repo.CheckIn(ctx, userID, time.Now())
}

func (uc *UserUsecase) newSession(userID, deviceID string) (*Session, error) {
	accessToken, err := randomToken()
	if err != nil {
		return nil, errors.InternalServer("TOKEN_GENERATION_FAILED", err.Error())
	}
	refreshToken, err := randomToken()
	if err != nil {
		return nil, errors.InternalServer("TOKEN_GENERATION_FAILED", err.Error())
	}
	now := time.Now()
	return &Session{
		UserID: userID, DeviceID: deviceID,
		AccessToken: accessToken, RefreshToken: refreshToken,
		AccessExpiry: now.Add(uc.accessTTL), RefreshExpiry: now.Add(uc.refreshTTL),
	}, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
