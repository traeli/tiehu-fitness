package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
)

type quotaWorkerRepo struct{ listed chan struct{} }

func (*quotaWorkerRepo) ReportUsage(context.Context, string, string, int64, time.Time) (*biz.MeetingUsageReservation, error) {
	return nil, nil
}
func (*quotaWorkerRepo) Finalize(context.Context, biz.MeetingQuotaFinalizeInput) (*biz.MeetingUsageRecord, error) {
	return nil, nil
}
func (*quotaWorkerRepo) GetSnapshot(context.Context, string, biz.MeetingBillingPeriod, biz.MeetingQuotaPolicy, time.Time) (*biz.MeetingQuotaSnapshot, error) {
	return nil, nil
}
func (r *quotaWorkerRepo) ListExpiredReservations(context.Context, time.Time, int) ([]*biz.MeetingUsageReservation, error) {
	select {
	case <-r.listed:
	default:
		close(r.listed)
	}
	return nil, nil
}
func (*quotaWorkerRepo) GetDefaultPolicy(context.Context) (biz.MeetingQuotaPolicy, error) {
	return biz.MeetingQuotaPolicy{}, nil
}

type allowQuotaWorkerLimiter struct{}

func (allowQuotaWorkerLimiter) Allow(context.Context, string, time.Time, int32, time.Duration) (biz.MeetingCreateRateDecision, error) {
	return biz.MeetingCreateRateDecision{Allowed: true}, nil
}

func TestMeetingQuotaReconcilerStopsOwnedLoop(t *testing.T) {
	repo := &quotaWorkerRepo{listed: make(chan struct{})}
	usecase, err := biz.NewMeetingQuotaUsecase(repo, repo, allowQuotaWorkerLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewMeetingQuotaReconciler(usecase, time.Second, 100, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- worker.Start(context.Background()) }()
	select {
	case <-repo.listed:
	case <-time.After(time.Second):
		t.Fatal("reconciliation worker did not run its initial batch")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
