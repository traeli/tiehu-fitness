package service

import (
	"context"

	v1 "github.com/tiehu-ai/tiehu-fitness/api/content/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
)

type ContentService struct {
	v1.UnimplementedContentServiceServer
	uc *biz.ContentUsecase
}

// NewContentService 创建健身内容接口服务。
func NewContentService(uc *biz.ContentUsecase) *ContentService { return &ContentService{uc: uc} }

// GetEquipment 返回指定器材详情。
func (s *ContentService) GetEquipment(ctx context.Context, req *v1.GetEquipmentRequest) (*v1.GetEquipmentResponse, error) {
	item, err := s.uc.GetEquipment(ctx, req.GetEquipmentCode())
	if err != nil {
		return nil, err
	}
	return &v1.GetEquipmentResponse{Equipment: toEquipmentProto(item)}, nil
}

// RecommendExercises 返回指定器材适用的动作列表。
func (s *ContentService) RecommendExercises(ctx context.Context, req *v1.RecommendExercisesRequest) (*v1.RecommendExercisesResponse, error) {
	items, err := s.uc.Recommend(ctx, req.GetEquipmentCode())
	if err != nil {
		return nil, err
	}
	reply := &v1.RecommendExercisesResponse{Exercises: make([]*v1.Exercise, 0, len(items))}
	for _, item := range items {
		reply.Exercises = append(reply.Exercises, toExerciseProto(item))
	}
	return reply, nil
}

// GetExercise 返回指定动作的教学详情。
func (s *ContentService) GetExercise(ctx context.Context, req *v1.GetExerciseRequest) (*v1.GetExerciseResponse, error) {
	item, err := s.uc.GetExercise(ctx, req.GetExerciseCode())
	if err != nil {
		return nil, err
	}
	return &v1.GetExerciseResponse{Exercise: toExerciseProto(item)}, nil
}

func toEquipmentProto(item *biz.Equipment) *v1.Equipment {
	return &v1.Equipment{EquipmentCode: item.Code, Name: item.Name, Description: item.Description, TargetMuscles: item.TargetMuscles, SafetyTips: item.SafetyTips}
}

func toExerciseProto(item *biz.Exercise) *v1.Exercise {
	return &v1.Exercise{
		ExerciseCode: item.Code, Name: item.Name, EquipmentCode: item.EquipmentCode,
		InstructionVideoUri: item.InstructionVideoURI, TargetMuscles: item.TargetMuscles,
		KeyPoints: item.KeyPoints, CommonMistakes: item.CommonMistakes,
	}
}
