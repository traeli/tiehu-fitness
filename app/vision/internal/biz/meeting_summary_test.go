package biz

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMeetingSummaryPrepareSnapshotsActiveProviderConfig(t *testing.T) {
	now := time.Now().UTC()
	active := testMeetingSummarizerBinding("20000000-0000-4000-8000-000000000002", "deepseek-next")
	repo := &meetingSummaryFakeRepo{}
	resolver := &meetingSummarizerFakeResolver{active: active, byID: map[string]*MeetingSummarizerBinding{active.ConfigID: active}}
	uc := newTestMeetingSummaryUsecase(t, repo, &meetingSummaryFakeSink{}, resolver)
	input := PrepareMeetingSummaryInput{
		MeetingID: uuid.NewString(), UserID: uuid.NewString(), Version: 1, SourceTranscriptRevision: 1,
		Language: MeetingLanguageZhCN, IdempotencyKey: uuid.NewString(), Now: now,
	}
	job, err := uc.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if job.ProviderConfigID != active.ConfigID || job.ModelName != active.ModelName || repo.selection.ConfigID != active.ConfigID {
		t.Fatalf("Prepare() did not snapshot active provider: job=%+v selection=%+v", job, repo.selection)
	}
}

func TestMeetingSummaryWorkerUsesJobProviderConfigSnapshot(t *testing.T) {
	now := time.Now().UTC()
	oldProvider := &meetingSummaryFakeSummarizer{}
	oldBinding := testMeetingSummarizerBinding("20000000-0000-4000-8000-000000000001", "deepseek-old")
	oldBinding.Summarizer = oldProvider
	newBinding := testMeetingSummarizerBinding("20000000-0000-4000-8000-000000000002", "deepseek-new")
	job := &MeetingSummaryJob{
		ID: uuid.NewString(), ProviderConfigID: oldBinding.ConfigID,
		MeetingID: uuid.NewString(), UserID: uuid.NewString(), Version: 1, SourceTranscriptRevision: 1,
		Language: MeetingLanguageZhCN, IdempotencyKey: uuid.NewString(), Status: MeetingSummaryJobStatusProcessing,
		Provider: oldBinding.Provider, ModelName: oldBinding.ModelName, PromptVersion: oldBinding.PromptVersion,
		AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	repo := &meetingSummaryFakeRepo{claimed: []*MeetingSummaryJob{job}}
	sink := &meetingSummaryFakeSink{snapshot: &MeetingTranscriptSnapshot{
		MeetingID: job.MeetingID, Language: job.Language, TranscriptRevision: job.SourceTranscriptRevision,
		Segments: []TranscriptSegment{{ID: uuid.NewString(), SessionID: uuid.NewString(), Sequence: 1, Content: "测试会议内容", Language: job.Language}},
	}}
	resolver := &meetingSummarizerFakeResolver{
		active: newBinding, byID: map[string]*MeetingSummarizerBinding{oldBinding.ConfigID: oldBinding, newBinding.ConfigID: newBinding},
	}
	uc := newTestMeetingSummaryUsecase(t, repo, sink, resolver)
	processed, err := uc.ProcessBatch(context.Background(), now)
	if err != nil || processed != 1 {
		t.Fatalf("ProcessBatch() = %d, %v", processed, err)
	}
	if oldProvider.calls != 1 || resolver.resolveIDs[0] != oldBinding.ConfigID || resolver.activeCalls != 0 {
		t.Fatalf("worker did not use job snapshot: calls=%d resolver=%+v", oldProvider.calls, resolver)
	}
}

func newTestMeetingSummaryUsecase(t *testing.T, repo MeetingSummaryRepo, sink CoreMeetingSummarySink, resolver MeetingSummarizerResolver) *MeetingSummaryUsecase {
	t.Helper()
	uc, err := NewMeetingSummaryUsecase(repo, sink, resolver, MeetingSummaryPolicy{
		LeaseTimeout: time.Minute, BatchSize: 2, MaxAttempts: 3,
		InitialBackoff: time.Second, MaxBackoff: time.Minute,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return uc
}

func testMeetingSummarizerBinding(configID, model string) *MeetingSummarizerBinding {
	return &MeetingSummarizerBinding{
		ConfigID: configID, Provider: MeetingSummaryProviderNameDeepSeek,
		ModelName: model, PromptVersion: "meeting-summary-v1", Summarizer: &meetingSummaryFakeSummarizer{},
	}
}

type meetingSummarizerFakeResolver struct {
	active      *MeetingSummarizerBinding
	byID        map[string]*MeetingSummarizerBinding
	activeCalls int
	resolveIDs  []string
}

func (r *meetingSummarizerFakeResolver) ResolveActive(context.Context) (*MeetingSummarizerBinding, error) {
	r.activeCalls++
	return r.active, nil
}

func (r *meetingSummarizerFakeResolver) Resolve(_ context.Context, configID string) (*MeetingSummarizerBinding, error) {
	r.resolveIDs = append(r.resolveIDs, configID)
	return r.byID[configID], nil
}

type meetingSummaryFakeSummarizer struct{ calls int }

func (s *meetingSummaryFakeSummarizer) Summarize(context.Context, *MeetingSummaryGenerationRequest) (*MeetingSummary, error) {
	s.calls++
	return &MeetingSummary{Topic: "测试", Abstract: "摘要"}, nil
}

type meetingSummaryFakeRepo struct {
	selection MeetingSummaryProviderSnapshot
	claimed   []*MeetingSummaryJob
}

func (*meetingSummaryFakeRepo) RecordLLMRequest(context.Context, string, string, time.Time) error {
	return nil
}

func (*meetingSummaryFakeRepo) RecordLLMResponse(context.Context, string, string, int32, time.Duration, int64, int64, string, time.Time) error {
	return nil
}

func (r *meetingSummaryFakeRepo) EnsureJob(_ context.Context, input PrepareMeetingSummaryInput, selection MeetingSummaryProviderSnapshot) (*MeetingSummaryJob, error) {
	r.selection = selection
	return &MeetingSummaryJob{
		ID: uuid.NewString(), ProviderConfigID: selection.ConfigID,
		MeetingID: input.MeetingID, UserID: input.UserID, Version: input.Version,
		SourceTranscriptRevision: input.SourceTranscriptRevision, Language: input.Language,
		IdempotencyKey: input.IdempotencyKey, Status: MeetingSummaryJobStatusPending,
		Provider: selection.Provider, ModelName: selection.ModelName, PromptVersion: selection.PromptVersion,
		AvailableAt: input.Now, CreatedAt: input.Now, UpdatedAt: input.Now,
	}, nil
}

func (r *meetingSummaryFakeRepo) ClaimJobs(context.Context, time.Time, time.Duration, int) ([]*MeetingSummaryJob, error) {
	return r.claimed, nil
}
func (*meetingSummaryFakeRepo) SaveGenerated(context.Context, string, *MeetingSummary, time.Time) error {
	return nil
}
func (*meetingSummaryFakeRepo) SaveFailureForDelivery(context.Context, string, MeetingSummaryFailureReason, time.Time) error {
	return nil
}
func (*meetingSummaryFakeRepo) RetryJob(context.Context, string, time.Time) error { return nil }
func (*meetingSummaryFakeRepo) MarkSucceeded(context.Context, string, time.Time) error {
	return nil
}
func (*meetingSummaryFakeRepo) MarkFailed(context.Context, string, time.Time) error { return nil }

type meetingSummaryFakeSink struct {
	snapshot *MeetingTranscriptSnapshot
}

func (s *meetingSummaryFakeSink) GetTranscriptSnapshot(context.Context, string, int64) (*MeetingTranscriptSnapshot, error) {
	return s.snapshot, nil
}
func (*meetingSummaryFakeSink) CompleteMeetingSummary(context.Context, *MeetingSummaryJob) error {
	return nil
}
func (*meetingSummaryFakeSink) FailMeetingSummary(context.Context, *MeetingSummaryJob, time.Time) error {
	return nil
}
