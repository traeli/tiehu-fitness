package data

import (
	"context"
	"errors"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"gorm.io/gorm"
)

type ContentRepo struct{ db *gorm.DB }

var _ biz.ContentRepo = (*ContentRepo)(nil)

func NewContentRepo(db *gorm.DB) biz.ContentRepo { return &ContentRepo{db: db} }

func (r *ContentRepo) GetEquipment(ctx context.Context, code string) (*biz.Equipment, error) {
	var row model.Equipment
	err := r.db.WithContext(ctx).Where("code = ? AND status = ?", code, "published").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kratoserrors.NotFound("EQUIPMENT_NOT_FOUND", "equipment not found")
	}
	if err != nil {
		return nil, databaseError(err)
	}
	return toBizEquipment(&row)
}

func (r *ContentRepo) ListExercises(ctx context.Context, equipmentCode string) ([]*biz.Exercise, error) {
	var rows []model.Exercise
	if err := r.db.WithContext(ctx).
		Where("equipment_code = ? AND status = ?", equipmentCode, "published").
		Order("code ASC").Find(&rows).Error; err != nil {
		return nil, databaseError(err)
	}
	items := make([]*biz.Exercise, 0, len(rows))
	for i := range rows {
		item, err := toBizExercise(&rows[i])
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *ContentRepo) GetExercise(ctx context.Context, code string) (*biz.Exercise, error) {
	var row model.Exercise
	err := r.db.WithContext(ctx).Where("code = ? AND status = ?", code, "published").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kratoserrors.NotFound("EXERCISE_NOT_FOUND", "exercise not found")
	}
	if err != nil {
		return nil, databaseError(err)
	}
	return toBizExercise(&row)
}

func toBizEquipment(row *model.Equipment) (*biz.Equipment, error) {
	targetMuscles, err := decodeStrings(row.TargetMuscles)
	if err != nil {
		return nil, databaseError(err)
	}
	safetyTips, err := decodeStrings(row.SafetyTips)
	if err != nil {
		return nil, databaseError(err)
	}
	return &biz.Equipment{
		Code: row.Code, Name: row.Name, Description: row.Description,
		TargetMuscles: targetMuscles, SafetyTips: safetyTips,
	}, nil
}

func toBizExercise(row *model.Exercise) (*biz.Exercise, error) {
	targetMuscles, err := decodeStrings(row.TargetMuscles)
	if err != nil {
		return nil, databaseError(err)
	}
	keyPoints, err := decodeStrings(row.KeyPoints)
	if err != nil {
		return nil, databaseError(err)
	}
	commonMistakes, err := decodeStrings(row.CommonMistakes)
	if err != nil {
		return nil, databaseError(err)
	}
	return &biz.Exercise{
		Code: row.Code, Name: row.Name, EquipmentCode: row.EquipmentCode,
		InstructionVideoURI: row.InstructionVideoURI, TargetMuscles: targetMuscles,
		KeyPoints: keyPoints, CommonMistakes: commonMistakes,
	}, nil
}
