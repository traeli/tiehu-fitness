package biz

import (
	"testing"
	"time"
)

func TestAIProviderConfigStatusTransitions(t *testing.T) {
	tests := []struct {
		from AIProviderConfigStatus
		to   AIProviderConfigStatus
		want bool
	}{
		{AIProviderConfigStatusDraft, AIProviderConfigStatusActive, true},
		{AIProviderConfigStatusDraft, AIProviderConfigStatusRetired, true},
		{AIProviderConfigStatusActive, AIProviderConfigStatusRetired, true},
		{AIProviderConfigStatusActive, AIProviderConfigStatusDraft, false},
		{AIProviderConfigStatusRetired, AIProviderConfigStatusActive, false},
	}
	for _, tt := range tests {
		if got := tt.from.CanTransitionTo(tt.to); got != tt.want {
			t.Fatalf("%q.CanTransitionTo(%q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestVersionedProviderConfigsValidateBounds(t *testing.T) {
	now := time.Now().UTC()
	asr := &ASRProviderConfig{
		ID: "10000000-0000-4000-8000-000000000001", Version: 1,
		Status: AIProviderConfigStatusActive, Provider: ASRProviderNameBailianParaformer,
		WorkspaceID: "workspace-1", RealtimeModel: "paraformer-realtime-v3", FileModel: "paraformer-v2",
		ActivatedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := asr.Validate(); err != nil {
		t.Fatalf("ASR config validation error = %v", err)
	}
	asr.RealtimeModel = "bad model"
	if err := asr.Validate(); err == nil {
		t.Fatal("ASR config allowed invalid model identifier")
	}

	summary := &MeetingSummaryProviderConfig{
		ID: "20000000-0000-4000-8000-000000000001", Version: 1,
		Status: AIProviderConfigStatusActive, Provider: MeetingSummaryProviderNameDeepSeek,
		ModelName: "deepseek-next", PromptVersion: "meeting-summary-v2",
		MaxInputCharsPerChunk: 60_000, MaxChunks: 64, MaxOutputTokens: 8_192,
		ActivatedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := summary.Validate(); err != nil {
		t.Fatalf("summary config validation error = %v", err)
	}
	summary.MaxChunks = 129
	if err := summary.Validate(); err == nil {
		t.Fatal("summary config allowed unbounded chunks")
	}
}
