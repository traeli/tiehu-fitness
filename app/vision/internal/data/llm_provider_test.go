package data

import (
	"context"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestMeetingSummarizerResolverLoadsActiveAndHistoricalConfigs(t *testing.T) {
	oldConfig := validMeetingSummaryProviderConfig("20000000-0000-4000-8000-000000000001", 1, "deepseek-old")
	newConfig := validMeetingSummaryProviderConfig("20000000-0000-4000-8000-000000000002", 2, "deepseek-new")
	repo := &meetingSummaryProviderConfigFakeRepo{
		active: newConfig,
		byID:   map[string]*biz.MeetingSummaryProviderConfig{oldConfig.ID: oldConfig, newConfig.ID: newConfig},
	}
	credentials := newProviderCredentialFakeRepo()
	resolver, err := NewMeetingSummarizerResolver(repo, credentials, &llmExchangeRecorderFake{}, validLLMRuntimeConfig(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	active, err := resolver.ResolveActive(context.Background())
	if err != nil || active.ConfigID != newConfig.ID || active.ModelName != "deepseek-new" {
		t.Fatalf("ResolveActive() = %+v, %v", active, err)
	}
	historical, err := resolver.Resolve(context.Background(), oldConfig.ID)
	if err != nil || historical.ConfigID != oldConfig.ID || historical.ModelName != "deepseek-old" {
		t.Fatalf("Resolve(old) = %+v, %v", historical, err)
	}
}

func TestMeetingSummarizerResolverLocalFakeDoesNotRequireAPIKey(t *testing.T) {
	config := validMeetingSummaryProviderConfig("20000000-0000-4000-8000-000000000001", 1, "deepseek-test")
	runtime := validLLMRuntimeConfig()
	repo := &meetingSummaryProviderConfigFakeRepo{active: config, byID: map[string]*biz.MeetingSummaryProviderConfig{config.ID: config}}
	resolver, err := NewMeetingSummarizerResolver(repo, nil, nil, runtime, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := resolver.ResolveActive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if binding.Provider != biz.MeetingSummaryProviderNameLocalFake {
		t.Fatalf("provider = %q", binding.Provider)
	}
}

type meetingSummaryProviderConfigFakeRepo struct {
	active *biz.MeetingSummaryProviderConfig
	byID   map[string]*biz.MeetingSummaryProviderConfig
}

type llmExchangeRecorderFake struct{}

func (*llmExchangeRecorderFake) RecordLLMRequest(context.Context, string, string, time.Time) error {
	return nil
}

func (*llmExchangeRecorderFake) RecordLLMResponse(context.Context, string, string, int32, time.Duration, int64, int64, string, time.Time) error {
	return nil
}

func (r *meetingSummaryProviderConfigFakeRepo) GetActiveMeetingSummaryProviderConfig(context.Context) (*biz.MeetingSummaryProviderConfig, error) {
	return r.active, nil
}

func (r *meetingSummaryProviderConfigFakeRepo) GetMeetingSummaryProviderConfig(_ context.Context, configID string) (*biz.MeetingSummaryProviderConfig, error) {
	return r.byID[configID], nil
}

func validLLMRuntimeConfig() *conf.LLM {
	return &conf.LLM{
		RequestTimeout: durationpb.New(2 * time.Minute),
		Deepseek: &conf.DeepSeek{
			Endpoint: "https://api.deepseek.com/chat/completions",
		},
	}
}

func validMeetingSummaryProviderConfig(id string, version int64, model string) *biz.MeetingSummaryProviderConfig {
	now := time.Now().UTC()
	return &biz.MeetingSummaryProviderConfig{
		ID: id, Version: version, Status: biz.AIProviderConfigStatusActive,
		Provider: biz.MeetingSummaryProviderNameDeepSeek, ModelName: model,
		PromptVersion: "meeting-summary-v1", MaxInputCharsPerChunk: 60_000,
		MaxChunks: 64, MaxOutputTokens: 8_192, ActivatedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
}
