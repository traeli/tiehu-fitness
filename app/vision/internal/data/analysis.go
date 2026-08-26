package data

import (
	"context"
	"sync"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"

	"github.com/go-kratos/kratos/v3/errors"
)

// MemoryAnalysisRepo is a development placeholder. Replace it with PostgreSQL
// and publish the job ID to the AI queue in the same use-case transaction.
type MemoryAnalysisRepo struct {
	mu   sync.RWMutex
	jobs map[string]*biz.AnalysisJob
}

func NewAnalysisRepo() biz.AnalysisRepo {
	return &MemoryAnalysisRepo{jobs: make(map[string]*biz.AnalysisJob)}
}

func (r *MemoryAnalysisRepo) Save(_ context.Context, job *biz.AnalysisJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyOfJob := *job
	r.jobs[job.ID] = &copyOfJob
	return nil
}

func (r *MemoryAnalysisRepo) Get(_ context.Context, id string) (*biz.AnalysisJob, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	job, ok := r.jobs[id]
	if !ok {
		return nil, errors.NotFound("ANALYSIS_JOB_NOT_FOUND", "analysis job not found")
	}
	copyOfJob := *job
	return &copyOfJob, nil
}
