package biz_test

import (
	"context"
	"testing"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data"
)

func TestRecommendExercises(t *testing.T) {
	uc := biz.NewContentUsecase(data.NewContentRepo())
	items, err := uc.Recommend(context.Background(), "cable-machine")
	if err != nil {
		t.Fatalf("Recommend() error = %v", err)
	}
	if len(items) != 1 || items[0].Code != "cable-row" {
		t.Fatalf("Recommend() = %#v", items)
	}
}
