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

func NewUserService(uc *biz.UserUsecase) *UserService { return &UserService{uc: uc} }

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

func (s *UserService) RefreshToken(ctx context.Context, req *v1.RefreshTokenRequest) (*v1.RefreshTokenResponse, error) {
	session, err := s.uc.RefreshToken(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, err
	}
	return &v1.RefreshTokenResponse{AccessToken: session.AccessToken, RefreshToken: session.RefreshToken, ExpiresInSeconds: int64(time.Until(session.AccessExpiry).Seconds())}, nil
}

func (s *UserService) Logout(ctx context.Context, req *v1.LogoutRequest) (*v1.LogoutResponse, error) {
	if err := s.uc.Logout(ctx, req.GetRefreshToken()); err != nil {
		return nil, err
	}
	return &v1.LogoutResponse{}, nil
}

func (s *UserService) GetFitnessProfile(ctx context.Context, req *v1.GetFitnessProfileRequest) (*v1.GetFitnessProfileResponse, error) {
	profile, err := s.uc.GetProfile(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &v1.GetFitnessProfileResponse{Profile: toProfileProto(profile)}, nil
}

func (s *UserService) UpdateFitnessProfile(ctx context.Context, req *v1.UpdateFitnessProfileRequest) (*v1.UpdateFitnessProfileResponse, error) {
	profile := fromProfileProto(req.GetProfile())
	if err := s.uc.UpdateProfile(ctx, profile); err != nil {
		return nil, err
	}
	return &v1.UpdateFitnessProfileResponse{Profile: toProfileProto(profile)}, nil
}

func (s *UserService) GenerateWorkoutPlan(ctx context.Context, req *v1.GenerateWorkoutPlanRequest) (*v1.GenerateWorkoutPlanResponse, error) {
	plan, err := s.uc.GeneratePlan(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &v1.GenerateWorkoutPlanResponse{Plan: toPlanProto(plan)}, nil
}

func (s *UserService) GetWorkoutPlan(ctx context.Context, req *v1.GetWorkoutPlanRequest) (*v1.GetWorkoutPlanResponse, error) {
	plan, err := s.uc.GetPlan(ctx, req.GetUserId(), req.GetPlanId())
	if err != nil {
		return nil, err
	}
	return &v1.GetWorkoutPlanResponse{Plan: toPlanProto(plan)}, nil
}

func (s *UserService) CheckIn(ctx context.Context, req *v1.CheckInRequest) (*v1.CheckInResponse, error) {
	streak, err := s.uc.CheckIn(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &v1.CheckInResponse{CurrentStreakDays: streak}, nil
}

func toUserProto(user *biz.User) *v1.User {
	return &v1.User{UserId: user.ID, Nickname: user.Nickname, AvatarUri: user.AvatarURI, Status: user.Status}
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
