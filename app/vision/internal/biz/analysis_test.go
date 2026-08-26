package biz_test

import (
	"context"
	"testing"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data"
)

func TestSubmitAndGetEquipmentJob(t *testing.T) {
	uc := biz.NewAnalysisUsecase(data.NewAnalysisRepo())
	job, err := uc.SubmitEquipment(context.Background(), "s3://bucket/equipment.jpg")
	if err != nil {
		t.Fatalf("SubmitEquipment() error = %v", err)
	}
	got, err := uc.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Type != biz.AnalysisTypeEquipment || got.Status != "pending" {
		t.Fatalf("Get() = %#v", got)
	}
}
