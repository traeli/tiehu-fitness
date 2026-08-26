package service

import (
	"context"

	v1 "github.com/tiehu-ai/tiehu-fitness/api/vision/v1"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

type VisionService struct {
	v1.UnimplementedVisionServiceServer
	uc *biz.AnalysisUsecase
}

func NewVisionService(uc *biz.AnalysisUsecase) *VisionService {
	return &VisionService{uc: uc}
}

func (s *VisionService) RecognizeEquipment(ctx context.Context, req *v1.RecognizeEquipmentRequest) (*v1.RecognizeEquipmentResponse, error) {
	job, err := s.uc.SubmitEquipment(ctx, req.GetImageUri())
	if err != nil {
		return nil, err
	}
	return &v1.RecognizeEquipmentResponse{Job: toProto(job)}, nil
}

func (s *VisionService) AnalyzePosture(ctx context.Context, req *v1.AnalyzePostureRequest) (*v1.AnalyzePostureResponse, error) {
	job, err := s.uc.SubmitPosture(ctx, req.GetVideoUri())
	if err != nil {
		return nil, err
	}
	return &v1.AnalyzePostureResponse{Job: toProto(job)}, nil
}

func (s *VisionService) GetAnalysisJob(ctx context.Context, req *v1.GetAnalysisJobRequest) (*v1.GetAnalysisJobResponse, error) {
	job, err := s.uc.Get(ctx, req.GetJobId())
	if err != nil {
		return nil, err
	}
	return &v1.GetAnalysisJobResponse{Job: toProto(job)}, nil
}

func toProto(job *biz.AnalysisJob) *v1.AnalysisJob {
	jobType := v1.AnalysisType_ANALYSIS_TYPE_EQUIPMENT
	if job.Type == biz.AnalysisTypePosture {
		jobType = v1.AnalysisType_ANALYSIS_TYPE_POSTURE
	}
	status := v1.AnalysisStatus_ANALYSIS_STATUS_PENDING
	switch job.Status {
	case "processing":
		status = v1.AnalysisStatus_ANALYSIS_STATUS_PROCESSING
	case "succeeded":
		status = v1.AnalysisStatus_ANALYSIS_STATUS_SUCCEEDED
	case "failed":
		status = v1.AnalysisStatus_ANALYSIS_STATUS_FAILED
	}
	return &v1.AnalysisJob{
		JobId:        job.ID,
		Type:         jobType,
		Status:       status,
		MediaUri:     job.MediaURI,
		ResultJson:   job.ResultJSON,
		ErrorMessage: job.ErrorMessage,
	}
}
