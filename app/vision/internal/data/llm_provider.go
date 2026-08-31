package data

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/llm/deepseek"
	llmlocalfake "github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/llm/localfake"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
)

type MeetingSummarizerResolver struct {
	repo        biz.MeetingSummaryProviderConfigRepo
	credentials biz.ProviderCredentialRepo
	recorder    biz.MeetingSummaryLLMExchangeRecorder
	runtime     *conf.LLM
	localFake   bool
	logger      *slog.Logger

	mu        sync.Mutex
	providers map[string]cachedMeetingSummarizer
}

type cachedMeetingSummarizer struct {
	credentialVersion int64
	provider          biz.MeetingSummarizer
}

var _ biz.MeetingSummarizerResolver = (*MeetingSummarizerResolver)(nil)

func NewMeetingSummarizerResolver(repo biz.MeetingSummaryProviderConfigRepo, credentials biz.ProviderCredentialRepo, recorder biz.MeetingSummaryLLMExchangeRecorder, runtime *conf.LLM, localFake bool, logger *slog.Logger) (*MeetingSummarizerResolver, error) {
	if repo == nil {
		return nil, fmt.Errorf("meeting summary provider config repository is required")
	}
	if !localFake && credentials == nil {
		return nil, fmt.Errorf("meeting summary provider credential repository is required")
	}
	if !localFake && recorder == nil {
		return nil, fmt.Errorf("meeting summary LLM exchange recorder is required")
	}
	if runtime == nil || runtime.GetRequestTimeout() == nil || runtime.GetDeepseek() == nil {
		return nil, fmt.Errorf("LLM infrastructure config is incomplete")
	}
	requestTimeout := runtime.GetRequestTimeout().AsDuration()
	if requestTimeout <= 0 || requestTimeout > 10*time.Minute {
		return nil, fmt.Errorf("LLM request timeout is invalid")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &MeetingSummarizerResolver{
		repo: repo, credentials: credentials, recorder: recorder, runtime: runtime, localFake: localFake, logger: logger,
		providers: make(map[string]cachedMeetingSummarizer),
	}, nil
}

func (r *MeetingSummarizerResolver) ResolveActive(ctx context.Context) (*biz.MeetingSummarizerBinding, error) {
	config, err := r.repo.GetActiveMeetingSummaryProviderConfig(ctx)
	if err != nil {
		return nil, err
	}
	if config.Status != biz.AIProviderConfigStatusActive {
		return nil, fmt.Errorf("active meeting summary provider config has invalid status %q", config.Status)
	}
	return r.resolveConfig(ctx, config)
}

func (r *MeetingSummarizerResolver) Resolve(ctx context.Context, configID string) (*biz.MeetingSummarizerBinding, error) {
	config, err := r.repo.GetMeetingSummaryProviderConfig(ctx, configID)
	if err != nil {
		return nil, err
	}
	if config.Status == biz.AIProviderConfigStatusDraft {
		return nil, fmt.Errorf("draft meeting summary provider config cannot run a job")
	}
	return r.resolveConfig(ctx, config)
}

func (r *MeetingSummarizerResolver) resolveConfig(ctx context.Context, config *biz.MeetingSummaryProviderConfig) (*biz.MeetingSummarizerBinding, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	credentialVersion := int64(0)
	apiKey := ""
	if !r.localFake {
		credential, err := r.credentials.GetProviderCredential(ctx, biz.ProviderCredentialNameDeepSeek)
		if err != nil {
			return nil, err
		}
		if err := credential.Validate(); err != nil {
			return nil, fmt.Errorf("DeepSeek provider credential is invalid: %w", err)
		}
		credentialVersion = credential.Version
		apiKey = credential.APIKey
		r.logger.Info("DeepSeek provider credential loaded",
			"provider", credential.Provider,
			"credential_version", credential.Version,
			"credential_updated_at", credential.UpdatedAt,
			"api_key_configured", true,
			"config_id", config.ID,
			"model", config.ModelName,
		)
	}
	r.mu.Lock()
	cached := r.providers[config.ID]
	r.mu.Unlock()
	provider := cached.provider
	if provider == nil || cached.credentialVersion != credentialVersion {
		created, err := r.newProvider(config, apiKey)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		if existing := r.providers[config.ID]; existing.provider != nil && existing.credentialVersion == credentialVersion {
			provider = existing.provider
		} else {
			r.providers[config.ID] = cachedMeetingSummarizer{credentialVersion: credentialVersion, provider: created}
			provider = created
		}
		r.mu.Unlock()
	}
	providerName := config.Provider
	if r.localFake {
		providerName = biz.MeetingSummaryProviderNameLocalFake
	}
	binding := &biz.MeetingSummarizerBinding{
		ConfigID: config.ID, Provider: providerName, ModelName: config.ModelName,
		PromptVersion: config.PromptVersion, Summarizer: provider,
	}
	return binding, binding.Validate()
}

func (r *MeetingSummarizerResolver) newProvider(config *biz.MeetingSummaryProviderConfig, apiKey string) (biz.MeetingSummarizer, error) {
	if r.localFake {
		return llmlocalfake.NewProvider(config.ModelName, config.PromptVersion)
	}
	if config.Provider != biz.MeetingSummaryProviderNameDeepSeek {
		return nil, fmt.Errorf("unsupported meeting summary provider %q", config.Provider)
	}
	deepseekConfig := r.runtime.GetDeepseek()
	provider, err := deepseek.NewProvider(deepseek.Config{
		APIKey: apiKey, Endpoint: strings.TrimSpace(deepseekConfig.GetEndpoint()),
		Model: config.ModelName, PromptVersion: config.PromptVersion,
		RequestTimeout:        r.runtime.GetRequestTimeout().AsDuration(),
		MaxInputCharsPerChunk: int(config.MaxInputCharsPerChunk), MaxChunks: int(config.MaxChunks),
		MaxOutputTokens: int(config.MaxOutputTokens), AllowTestEndpoint: r.runtime.GetAllowTestEndpoint(),
	}, r.recorder, r.logger)
	if err != nil {
		return nil, fmt.Errorf("create DeepSeek provider for config version %d: %w", config.Version, err)
	}
	return provider, nil
}
