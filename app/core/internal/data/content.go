package data

import (
	"context"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"

	"github.com/go-kratos/kratos/v3/errors"
)

type MemoryContentRepo struct {
	equipment map[string]*biz.Equipment
	exercises map[string]*biz.Exercise
}

func NewContentRepo() biz.ContentRepo {
	return &MemoryContentRepo{
		equipment: map[string]*biz.Equipment{
			"cable-machine": {
				Code: "cable-machine", Name: "龙门架", Description: "支持多方向绳索阻力训练",
				TargetMuscles: []string{"胸部", "背部", "肩部", "手臂"},
				SafetyTips:    []string{"训练前检查插销", "保持钢索路径无遮挡"},
			},
		},
		exercises: map[string]*biz.Exercise{
			"cable-row": {
				Code: "cable-row", Name: "坐姿绳索划船", EquipmentCode: "cable-machine",
				InstructionVideoURI: "https://example.invalid/videos/cable-row",
				TargetMuscles:       []string{"背阔肌", "菱形肌", "肱二头肌"},
				KeyPoints:           []string{"挺胸保持脊柱中立", "肘部向后拉"},
				CommonMistakes:      []string{"身体大幅后仰", "耸肩"},
			},
		},
	}
}

func (r *MemoryContentRepo) GetEquipment(_ context.Context, code string) (*biz.Equipment, error) {
	item, ok := r.equipment[code]
	if !ok {
		return nil, errors.NotFound("EQUIPMENT_NOT_FOUND", "equipment not found")
	}
	copyOfItem := *item
	copyOfItem.TargetMuscles = append([]string(nil), item.TargetMuscles...)
	copyOfItem.SafetyTips = append([]string(nil), item.SafetyTips...)
	return &copyOfItem, nil
}

func (r *MemoryContentRepo) ListExercises(_ context.Context, equipmentCode string) ([]*biz.Exercise, error) {
	items := make([]*biz.Exercise, 0)
	for _, exercise := range r.exercises {
		if exercise.EquipmentCode == equipmentCode {
			items = append(items, cloneExercise(exercise))
		}
	}
	return items, nil
}

func (r *MemoryContentRepo) GetExercise(_ context.Context, code string) (*biz.Exercise, error) {
	item, ok := r.exercises[code]
	if !ok {
		return nil, errors.NotFound("EXERCISE_NOT_FOUND", "exercise not found")
	}
	return cloneExercise(item), nil
}

func cloneExercise(item *biz.Exercise) *biz.Exercise {
	copyOfItem := *item
	copyOfItem.TargetMuscles = append([]string(nil), item.TargetMuscles...)
	copyOfItem.KeyPoints = append([]string(nil), item.KeyPoints...)
	copyOfItem.CommonMistakes = append([]string(nil), item.CommonMistakes...)
	return &copyOfItem
}
