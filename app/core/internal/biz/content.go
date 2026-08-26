package biz

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v3/errors"
)

type Equipment struct {
	Code          string
	Name          string
	Description   string
	TargetMuscles []string
	SafetyTips    []string
}

type Exercise struct {
	Code                string
	Name                string
	EquipmentCode       string
	InstructionVideoURI string
	TargetMuscles       []string
	KeyPoints           []string
	CommonMistakes      []string
}

type ContentRepo interface {
	GetEquipment(context.Context, string) (*Equipment, error)
	ListExercises(context.Context, string) ([]*Exercise, error)
	GetExercise(context.Context, string) (*Exercise, error)
}

type ContentUsecase struct{ repo ContentRepo }

func NewContentUsecase(repo ContentRepo) *ContentUsecase { return &ContentUsecase{repo: repo} }

func (uc *ContentUsecase) GetEquipment(ctx context.Context, code string) (*Equipment, error) {
	if strings.TrimSpace(code) == "" {
		return nil, errors.BadRequest("EQUIPMENT_CODE_REQUIRED", "equipment_code is required")
	}
	return uc.repo.GetEquipment(ctx, code)
}

func (uc *ContentUsecase) Recommend(ctx context.Context, equipmentCode string) ([]*Exercise, error) {
	if strings.TrimSpace(equipmentCode) == "" {
		return nil, errors.BadRequest("EQUIPMENT_CODE_REQUIRED", "equipment_code is required")
	}
	return uc.repo.ListExercises(ctx, equipmentCode)
}

func (uc *ContentUsecase) GetExercise(ctx context.Context, code string) (*Exercise, error) {
	if strings.TrimSpace(code) == "" {
		return nil, errors.BadRequest("EXERCISE_CODE_REQUIRED", "exercise_code is required")
	}
	return uc.repo.GetExercise(ctx, code)
}
