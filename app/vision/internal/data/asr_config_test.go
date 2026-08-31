package data

import (
	"context"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestValidateASRRuntimeConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*conf.ASR)
	}{
		{name: "valid"},
		{name: "missing session timeout", mutate: func(c *conf.ASR) { c.SessionTimeout = nil }},
		{name: "unbounded finish timeout", mutate: func(c *conf.ASR) { c.FinishTimeout = durationpb.New(3 * time.Minute) }},
		{name: "zero sessions", mutate: func(c *conf.ASR) { c.MaxConcurrentSessions = 0 }},
		{name: "oversized frame", mutate: func(c *conf.ASR) { c.MaxAudioFrameBytes = maxASRFrameBytes + 1 }},
		{name: "missing startup probe", mutate: func(c *conf.ASR) { c.StartupProbe = nil }},
		{name: "missing startup probe timeout", mutate: func(c *conf.ASR) { c.StartupProbe.Timeout = nil }},
		{name: "unbounded startup probe timeout", mutate: func(c *conf.ASR) { c.StartupProbe.Timeout = durationpb.New(2 * time.Minute) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validASRRuntimeConfig()
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			err := ValidateASRRuntimeConfig(cfg)
			if tt.name == "valid" && err != nil {
				t.Fatalf("ValidateASRRuntimeConfig() error = %v", err)
			}
			if tt.name != "valid" && err == nil {
				t.Fatal("ValidateASRRuntimeConfig() expected error")
			}
		})
	}
}

func TestResolveBailianEndpointUsesWorkspaceDomain(t *testing.T) {
	endpoint, err := resolveBailianEndpoint("", "test-workspace", false)
	if err != nil {
		t.Fatalf("resolveBailianEndpoint() error = %v", err)
	}
	want := "wss://test-workspace.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference"
	if endpoint != want {
		t.Fatalf("resolveBailianEndpoint() = %q, want %q", endpoint, want)
	}
	if _, err := resolveBailianEndpoint("wss://other-workspace.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference", "test-workspace", false); err == nil {
		t.Fatal("resolveBailianEndpoint() expected workspace mismatch error")
	}
	if _, err := resolveBailianEndpoint("ws://example.com/asr", "test-workspace", false); err == nil {
		t.Fatal("resolveBailianEndpoint() allowed insecure public endpoint")
	}
	if endpoint, err := resolveBailianEndpoint("ws://127.0.0.1:18080/asr", "test-workspace", true); err != nil || endpoint == "" {
		t.Fatalf("resolveBailianEndpoint() loopback = %q, %v", endpoint, err)
	}
}

func TestASRProviderResolverLoadsVersionedModel(t *testing.T) {
	config := validASRProviderConfig()
	config.RealtimeModel = "paraformer-realtime-v3"
	repo := &asrProviderConfigFakeRepo{config: config}
	credentials := newProviderCredentialFakeRepo()
	resolver, err := NewASRProviderResolver(repo, credentials, validASRRuntimeConfig(), 64, false, nil)
	if err != nil {
		t.Fatalf("NewASRProviderResolver() error = %v", err)
	}
	binding, err := resolver.ResolveActive(context.Background())
	if err != nil {
		t.Fatalf("ResolveActive() error = %v", err)
	}
	if binding.ConfigID != config.ID || binding.Provider.Name() != biz.ASRProviderNameBailianParaformer {
		t.Fatalf("ResolveActive() binding = %+v", binding)
	}
	credentials.credentials[biz.ProviderCredentialNameBailianParaformer].Version++
	credentials.credentials[biz.ProviderCredentialNameBailianParaformer].APIKey = "rotated-test-key"
	rotated, err := resolver.ResolveActive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Provider == binding.Provider {
		t.Fatal("ResolveActive() reused provider after credential version changed")
	}
}

func TestASRProviderResolverLocalFakeDoesNotRequireAPIKey(t *testing.T) {
	runtime := validASRRuntimeConfig()
	resolver, err := NewASRProviderResolver(&asrProviderConfigFakeRepo{config: validASRProviderConfig()}, nil, runtime, 64, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := resolver.ResolveActive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if binding.Provider.Name() != biz.ASRProviderNameLocalFake {
		t.Fatalf("provider = %q", binding.Provider.Name())
	}
}

type asrProviderConfigFakeRepo struct {
	config *biz.ASRProviderConfig
}

func (r *asrProviderConfigFakeRepo) GetActiveASRProviderConfig(context.Context) (*biz.ASRProviderConfig, error) {
	return r.config, nil
}

func (r *asrProviderConfigFakeRepo) GetASRProviderConfig(context.Context, string) (*biz.ASRProviderConfig, error) {
	return r.config, nil
}

func validASRRuntimeConfig() *conf.ASR {
	return &conf.ASR{
		SessionTimeout:        durationpb.New(4 * time.Hour),
		ConnectTimeout:        durationpb.New(5 * time.Second),
		ReadTimeout:           durationpb.New(30 * time.Second),
		WriteTimeout:          durationpb.New(5 * time.Second),
		FinishTimeout:         durationpb.New(10 * time.Second),
		MaxConcurrentSessions: 100,
		MaxAudioFrameBytes:    6_400,
		StartupProbe: &conf.ASRStartupProbe{
			Enabled: true,
			Timeout: durationpb.New(30 * time.Second),
		},
		Bailian: &conf.BailianParaformer{},
	}
}

type providerCredentialFakeRepo struct {
	credentials map[biz.ProviderCredentialName]*biz.ProviderCredential
}

func newProviderCredentialFakeRepo() *providerCredentialFakeRepo {
	now := time.Now().UTC()
	return &providerCredentialFakeRepo{credentials: map[biz.ProviderCredentialName]*biz.ProviderCredential{
		biz.ProviderCredentialNameBailianParaformer: {
			Provider: biz.ProviderCredentialNameBailianParaformer, APIKey: "test-bailian-key",
			Version: 1, CreatedAt: now, UpdatedAt: now,
		},
		biz.ProviderCredentialNameDeepSeek: {
			Provider: biz.ProviderCredentialNameDeepSeek, APIKey: "test-deepseek-key",
			Version: 1, CreatedAt: now, UpdatedAt: now,
		},
	}}
}

func (r *providerCredentialFakeRepo) GetProviderCredential(_ context.Context, provider biz.ProviderCredentialName) (*biz.ProviderCredential, error) {
	return r.credentials[provider], nil
}

func validASRProviderConfig() *biz.ASRProviderConfig {
	now := time.Now().UTC()
	return &biz.ASRProviderConfig{
		ID: "10000000-0000-4000-8000-000000000099", Version: 99,
		Status: biz.AIProviderConfigStatusActive, Provider: biz.ASRProviderNameBailianParaformer,
		WorkspaceID: "test-workspace", RealtimeModel: "paraformer-realtime-v2", FileModel: "paraformer-v2",
		ActivatedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
}
