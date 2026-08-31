package service

import (
	"context"
	"time"

	v1 "github.com/tiehu-ai/tiehu-fitness/api/user/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
)

type UserService struct {
	v1.UnimplementedUserServiceServer
	uc *biz.UserUsecase
}

// NewUserService 创建用户接口服务。
func NewUserService(uc *biz.UserUsecase) *UserService { return &UserService{uc: uc} }

// Register 创建 Web 密码账号并返回首次登录凭证。
func (s *UserService) Register(ctx context.Context, req *v1.RegisterRequest) (*v1.RegisterResponse, error) {
	result, err := s.uc.Register(ctx, req.GetEmail(), req.GetPassword(), req.GetNickname(), req.GetDeviceId())
	if err != nil {
		return nil, err
	}
	return &v1.RegisterResponse{
		AccessToken: result.Session.AccessToken, RefreshToken: result.Session.RefreshToken,
		ExpiresInSeconds:   accessExpiresInSeconds(result),
		User:               toUserProto(result.User),
		OnboardingRequired: result.OnboardingRequired,
	}, nil
}

// Login 校验 Web 邮箱和密码并返回登录凭证。
func (s *UserService) Login(ctx context.Context, req *v1.LoginRequest) (*v1.LoginResponse, error) {
	result, err := s.uc.Login(ctx, req.GetEmail(), req.GetPassword(), req.GetDeviceId())
	if err != nil {
		return nil, err
	}
	return &v1.LoginResponse{
		AccessToken: result.Session.AccessToken, RefreshToken: result.Session.RefreshToken,
		ExpiresInSeconds:   accessExpiresInSeconds(result),
		User:               toUserProto(result.User),
		OnboardingRequired: result.OnboardingRequired,
	}, nil
}

// WechatLogin 将微信登录请求交给用户用例处理。
func (s *UserService) WechatLogin(ctx context.Context, req *v1.WechatLoginRequest) (*v1.WechatLoginResponse, error) {
	result, err := s.uc.WechatLogin(ctx, req.GetCode(), req.GetDeviceId())
	if err != nil {
		return nil, err
	}
	return &v1.WechatLoginResponse{
		AccessToken: result.Session.AccessToken, RefreshToken: result.Session.RefreshToken,
		ExpiresInSeconds: int64(time.Until(result.Session.AccessExpiry).Seconds()),
		User:             toUserProto(result.User), IsNewUser: result.IsNewUser,
		OnboardingRequired: result.OnboardingRequired,
	}, nil
}

// UToolsLogin verifies the plugin temporary token and returns project tokens.
func (s *UserService) UToolsLogin(ctx context.Context, req *v1.UToolsLoginRequest) (*v1.UToolsLoginResponse, error) {
	result, err := s.uc.UToolsLogin(ctx, req.GetTemporaryToken(), req.GetDeviceId())
	if err != nil {
		return nil, err
	}
	return &v1.UToolsLoginResponse{
		AccessToken: result.Session.AccessToken, RefreshToken: result.Session.RefreshToken,
		ExpiresInSeconds:   accessExpiresInSeconds(result),
		User:               toUserProto(result.User),
		IsNewUser:          result.IsNewUser,
		OnboardingRequired: result.OnboardingRequired,
	}, nil
}

// RefreshToken 轮换令牌并返回新的会话凭证。
func (s *UserService) RefreshToken(ctx context.Context, req *v1.RefreshTokenRequest) (*v1.RefreshTokenResponse, error) {
	session, err := s.uc.RefreshToken(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, err
	}
	return &v1.RefreshTokenResponse{AccessToken: session.AccessToken, RefreshToken: session.RefreshToken, ExpiresInSeconds: int64(time.Until(session.AccessExpiry).Seconds())}, nil
}

// Logout 撤销请求中的刷新令牌。
func (s *UserService) Logout(ctx context.Context, req *v1.LogoutRequest) (*v1.LogoutResponse, error) {
	if err := s.uc.Logout(ctx, req.GetRefreshToken()); err != nil {
		return nil, err
	}
	return &v1.LogoutResponse{}, nil
}

// GetFitnessProfile 返回用户健身档案。
func (s *UserService) GetFitnessProfile(ctx context.Context, req *v1.GetFitnessProfileRequest) (*v1.GetFitnessProfileResponse, error) {
	userID, err := biz.RequireCurrentUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	profile, err := s.uc.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &v1.GetFitnessProfileResponse{Profile: toProfileProto(profile)}, nil
}

// UpdateFitnessProfile 保存并返回用户健身档案。
func (s *UserService) UpdateFitnessProfile(ctx context.Context, req *v1.UpdateFitnessProfileRequest) (*v1.UpdateFitnessProfileResponse, error) {
	userID, err := biz.RequireCurrentUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	profile := fromProfileProto(req.GetProfile())
	if profile != nil {
		profile.UserID = userID
	}
	if err := s.uc.UpdateProfile(ctx, profile); err != nil {
		return nil, err
	}
	return &v1.UpdateFitnessProfileResponse{Profile: toProfileProto(profile)}, nil
}

// GenerateWorkoutPlan 生成并返回用户训练计划。
func (s *UserService) GenerateWorkoutPlan(ctx context.Context, req *v1.GenerateWorkoutPlanRequest) (*v1.GenerateWorkoutPlanResponse, error) {
	userID, err := biz.RequireCurrentUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	plan, err := s.uc.GeneratePlan(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &v1.GenerateWorkoutPlanResponse{Plan: toPlanProto(plan)}, nil
}

// GetWorkoutPlan 返回指定用户的训练计划。
func (s *UserService) GetWorkoutPlan(ctx context.Context, req *v1.GetWorkoutPlanRequest) (*v1.GetWorkoutPlanResponse, error) {
	userID, err := biz.RequireCurrentUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	plan, err := s.uc.GetPlan(ctx, userID, req.GetPlanId())
	if err != nil {
		return nil, err
	}
	return &v1.GetWorkoutPlanResponse{Plan: toPlanProto(plan)}, nil
}

// CheckIn 记录用户当天打卡并返回连续天数。
func (s *UserService) CheckIn(ctx context.Context, req *v1.CheckInRequest) (*v1.CheckInResponse, error) {
	userID, err := biz.RequireCurrentUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	streak, err := s.uc.CheckIn(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &v1.CheckInResponse{CurrentStreakDays: streak}, nil
}

func toUserProto(user *biz.User) *v1.User {
	return &v1.User{UserId: user.ID, Nickname: user.Nickname, AvatarUri: user.AvatarURI, Status: user.Status.String()}
}

func accessExpiresInSeconds(result *biz.LoginResult) int64 {
	return int64(time.Until(result.Session.AccessExpiry).Seconds())
}

func toProfileProto(profile *biz.FitnessProfile) *v1.FitnessProfile {
	return &v1.FitnessProfile{
		UserId: profile.UserID, Goal: profile.Goal, ExperienceLevel: profile.ExperienceLevel,
		DaysPerWeek: profile.DaysPerWeek, DurationMinutes: profile.DurationMinutes,
		AvailableEquipmentCodes: profile.AvailableEquipmentCodes, InjuryNotes: profile.InjuryNotes,
		OnboardingCompleted: profile.OnboardingCompleted,
	}
}

func fromProfileProto(profile *v1.FitnessProfile) *biz.FitnessProfile {
	if profile == nil {
		return nil
	}
	return &biz.FitnessProfile{
		UserID: profile.GetUserId(), Goal: profile.GetGoal(), ExperienceLevel: profile.GetExperienceLevel(),
		DaysPerWeek: profile.GetDaysPerWeek(), DurationMinutes: profile.GetDurationMinutes(),
		AvailableEquipmentCodes: profile.GetAvailableEquipmentCodes(), InjuryNotes: profile.GetInjuryNotes(),
		OnboardingCompleted: profile.GetOnboardingCompleted(),
	}
}

func toPlanProto(plan *biz.WorkoutPlan) *v1.WorkoutPlan {
	return &v1.WorkoutPlan{PlanId: plan.ID, UserId: plan.UserID, Goal: plan.Goal, Status: plan.Status}
}
