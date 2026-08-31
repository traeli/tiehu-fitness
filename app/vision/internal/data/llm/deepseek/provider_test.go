package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

func TestNewProviderValidatesCredentialsAndEndpoint(t *testing.T) {
	t.Parallel()
	valid := Config{
		APIKey: "test-key", Endpoint: "https://api.deepseek.com/chat/completions",
		Model: "deepseek-v4-flash", PromptVersion: "meeting-summary-v1",
		RequestTimeout: time.Minute, MaxInputCharsPerChunk: 1_000, MaxChunks: 8, MaxOutputTokens: 512,
	}
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantError bool
	}{
		{name: "official endpoint"},
		{name: "missing API key", mutate: func(config *Config) { config.APIKey = "" }, wantError: true},
		{name: "untrusted endpoint", mutate: func(config *Config) { config.Endpoint = "https://example.com/chat/completions" }, wantError: true},
		{name: "loopback disabled", mutate: func(config *Config) { config.Endpoint = "http://127.0.0.1:18080/chat/completions" }, wantError: true},
		{name: "loopback explicitly enabled", mutate: func(config *Config) {
			config.Endpoint = "http://127.0.0.1:18080/chat/completions"
			config.AllowTestEndpoint = true
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			if test.mutate != nil {
				test.mutate(&config)
			}
			_, err := NewProvider(config, &exchangeRecorderFake{}, slog.Default())
			if test.wantError && err == nil {
				t.Fatal("expected validation error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("NewProvider() error = %v", err)
			}
		})
	}
}

func TestTranscriptChunksPreserveSegmentOrder(t *testing.T) {
	t.Parallel()
	provider, err := NewProvider(Config{
		APIKey: "test-key", Endpoint: "https://api.deepseek.com/chat/completions",
		Model: "deepseek-v4-flash", PromptVersion: "meeting-summary-v1",
		RequestTimeout: time.Minute, MaxInputCharsPerChunk: 1_000, MaxChunks: 8, MaxOutputTokens: 512,
	}, &exchangeRecorderFake{}, slog.Default())
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	snapshot := &biz.MeetingTranscriptSnapshot{
		MeetingID: "meeting-id", Language: biz.MeetingLanguageZhCN, TranscriptRevision: 2,
		Segments: []biz.TranscriptSegment{
			{Sequence: 1, Content: "第一段", EndOffset: time.Second},
			{Sequence: 2, Content: "第二段", StartOffset: time.Second, EndOffset: 2 * time.Second},
		},
	}
	chunks, err := provider.transcriptChunks(snapshot)
	if err != nil {
		t.Fatalf("transcriptChunks() error = %v", err)
	}
	if len(chunks) != 1 || chunks[0] != "[00:00-00:01] 发言人: 第一段\n[00:01-00:02] 发言人: 第二段\n" {
		t.Fatalf("transcriptChunks() = %#v", chunks)
	}
}

func TestProviderLogsRequestResponseMetadataWithoutSensitiveContent(t *testing.T) {
	t.Parallel()
	const (
		apiKey          = "sensitive-test-key"
		privatePrompt   = "private meeting transcript"
		privateResponse = "private model response"
		unexpectedField = "unexpected_private_field"
	)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Errorf("Authorization header was not sent to provider")
		}
		content, err := json.Marshal(map[string]any{
			"topic": privateResponse, "abstract": "摘要", "key_discussions": []string{},
			"decisions": []string{}, "action_items": []string{}, "risks": []string{},
			unexpectedField: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 20},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    request,
		}, nil
	})

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	recorder := &exchangeRecorderFake{}
	provider, err := NewProvider(Config{
		APIKey: apiKey, Endpoint: "https://api.deepseek.com/chat/completions",
		Model: "deepseek-v4-flash", PromptVersion: "meeting-summary-v1",
		RequestTimeout: time.Minute, MaxInputCharsPerChunk: 1_000,
		MaxChunks: 8, MaxOutputTokens: 512,
	}, recorder, logger)
	if err != nil {
		t.Fatal(err)
	}
	provider.client = &http.Client{Transport: transport, Timeout: time.Minute}
	_, _, err = provider.call(context.Background(), deepSeekCallMetadata{
		JobID: "job-id", MeetingID: "meeting-id", TranscriptRevision: 1, Stage: "summarize", Part: 1, TotalParts: 1,
	}, privatePrompt)
	if err == nil || !strings.Contains(err.Error(), unexpectedField) {
		t.Fatalf("call() error = %v, want unknown field detail", err)
	}
	output := logs.String()
	for _, message := range []string{
		"DeepSeek meeting summary request started",
		"DeepSeek meeting summary response received",
		"DeepSeek meeting summary response content received",
		"DeepSeek meeting summary response content is invalid",
	} {
		if !strings.Contains(output, message) {
			t.Fatalf("logs do not contain %q: %s", message, output)
		}
	}
	for _, sensitive := range []string{apiKey, privatePrompt, privateResponse} {
		if strings.Contains(output, sensitive) {
			t.Fatalf("logs contain sensitive content %q: %s", sensitive, output)
		}
	}
	if recorder.request == "" || recorder.response == "" || recorder.httpStatus != http.StatusOK {
		t.Fatalf("provider exchange was not recorded: %+v", recorder)
	}
	if strings.Contains(recorder.request, apiKey) {
		t.Fatal("recorded request contains API key")
	}
}

type exchangeRecorderFake struct {
	request    string
	response   string
	httpStatus int32
	failure    string
}

func (r *exchangeRecorderFake) RecordLLMRequest(_ context.Context, _ string, payload string, _ time.Time) error {
	r.request = payload
	return nil
}

func (r *exchangeRecorderFake) RecordLLMResponse(_ context.Context, _ string, payload string, httpStatus int32, _ time.Duration, _, _ int64, failure string, _ time.Time) error {
	r.response = payload
	r.httpStatus = httpStatus
	r.failure = failure
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
