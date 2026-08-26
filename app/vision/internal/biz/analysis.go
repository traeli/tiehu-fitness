package biz

import (
	"context"
	"strings"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

type AnalysisType string

const (
	AnalysisTypeEquipment AnalysisType = "equipment"
	AnalysisTypePosture   AnalysisType = "posture"
)

type AnalysisJob struct {
	ID           string
	Type         AnalysisType
	Status       string
	MediaURI     string
	ResultJSON   string
	ErrorMessage string
}

type AnalysisRepo interface {
	Save(context.Context, *AnalysisJob) error
	Get(context.Context, string) (*AnalysisJob, error)
}

type AnalysisUsecase struct {
	repo AnalysisRepo
}

func NewAnalysisUsecase(repo AnalysisRepo) *AnalysisUsecase {
	return &AnalysisUsecase{repo: repo}
}

func (uc *AnalysisUsecase) SubmitEquipment(ctx context.Context, imageURI string) (*AnalysisJob, error) {
	if strings.TrimSpace(imageURI) == "" {
		return nil, errors.BadRequest("IMAGE_URI_REQUIRED", "image_uri is required")
	}
	return uc.submit(ctx, AnalysisTypeEquipment, imageURI)
}

func (uc *AnalysisUsecase) SubmitPosture(ctx context.Context, videoURI string) (*AnalysisJob, error) {
	if strings.TrimSpace(videoURI) == "" {
		return nil, errors.BadRequest("VIDEO_URI_REQUIRED", "video_uri is required")
	}
	return uc.submit(ctx, AnalysisTypePosture, videoURI)
}

func (uc *AnalysisUsecase) submit(ctx context.Context, jobType AnalysisType, mediaURI string) (*AnalysisJob, error) {
	job := &AnalysisJob{
		ID:       uuid.NewString(),
		Type:     jobType,
		Status:   "pending",
		MediaURI: mediaURI,
	}
	if err := uc.repo.Save(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

func (uc *AnalysisUsecase) Get(ctx context.Context, id string) (*AnalysisJob, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.BadRequest("JOB_ID_REQUIRED", "job_id is required")
	}
	return uc.repo.Get(ctx, id)
}
