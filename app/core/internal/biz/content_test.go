package biz_test

import (
	"context"
	"testing"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
)

type fakeContentRepo struct{}

func (fakeContentRepo) GetEquipment(context.Context, string) (*biz.Equipment, error) {
	return &biz.Equipment{Code: "cable-machine", Name: "龙门架"}, nil
}

func (fakeContentRepo) ListExercises(context.Context, string) ([]*biz.Exercise, error) {
	return []*biz.Exercise{{Code: "cable-row", EquipmentCode: "cable-machine"}}, nil
}

func (fakeContentRepo) GetExercise(context.Context, string) (*biz.Exercise, error) {
	return &biz.Exercise{Code: "cable-row", EquipmentCode: "cable-machine"}, nil
}

func TestRecommendExercises(t *testing.T) {
	uc := biz.NewContentUsecase(fakeContentRepo{})
	items, err := uc.Recommend(context.Background(), "cable-machine")
	if err != nil {
		t.Fatalf("Recommend() error = %v", err)
	}
	if len(items) != 1 || items[0].Code != "cable-row" {
		t.Fatalf("Recommend() = %#v", items)
	}
}
