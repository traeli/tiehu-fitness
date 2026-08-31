package data

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/asr/localfake"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/asr/paraformer"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
)

// ASRProviderResolver combines versioned model settings owned by Vision's
// database with credentials and network bounds owned by process config.
type ASRProviderResolver struct {
	repo          biz.ASRProviderConfigRepo
	credentials   biz.ProviderCredentialRepo
	runtime       *conf.ASR
	queueCapacity int32
	localFake     bool
	logger        *slog.Logger
	sharedSlots   chan struct{}
	fakeProvider  biz.ASRProvider

	mu        sync.Mutex
	providers map[string]cachedASRProvider
}

type cachedASRProvider struct {
	credentialVersion int64
	provider          biz.ASRProvider
}

var _ biz.ASRProviderResolver = (*ASRProviderResolver)(nil)

func NewASRProviderResolver(repo biz.ASRProviderConfigRepo, credentials biz.ProviderCredentialRepo, runtime *conf.ASR, queueCapacity int32, localFake bool, logger *slog.Logger) (*ASRProviderResolver, error) {
	if repo == nil {
		return nil, fmt.Errorf("ASR provider config repository is required")
	}
	if !localFake && credentials == nil {
		return nil, fmt.Errorf("ASR provider credential repository is required")
	}
	if err := ValidateASRRuntimeConfig(runtime); err != nil {
		return nil, err
	}
	if queueCapacity <= 0 || queueCapacity > 1_024 {
		return nil, fmt.Errorf("ASR queue capacity must be between 1 and 1024")
	}
	if logger == nil {
		logger = slog.Default()
	}
	resolver := &ASRProviderResolver{
		repo: repo, credentials: credentials, runtime: runtime, queueCapacity: queueCapacity, localFake: localFake,
		logger: logger, sharedSlots: make(chan struct{}, int(runtime.GetMaxConcurrentSessions())),
		providers: make(map[string]cachedASRProvider),
	}
	if localFake {
		provider, err := localfake.NewProvider(localfake.Config{
			MaxConcurrentSessions: runtime.GetMaxConcurrentSessions(), QueueCapacity: queueCapacity,
		}, logger)
		if err != nil {
			return nil, fmt.Errorf("create local fake ASR provider: %w", err)
		}
		resolver.fakeProvider = provider
	}
	return resolver, nil
}

func (r *ASRProviderResolver) ResolveActive(ctx context.Context) (*biz.ASRProviderBinding, error) {
	config, err := r.repo.GetActiveASRProviderConfig(ctx)
	if err != nil {
		return nil, err
	}
	if config.Status != biz.AIProviderConfigStatusActive {
		return nil, fmt.Errorf("active ASR provider config has invalid status %q", config.Status)
	}
	return r.resolveConfig(ctx, config)
}

func (r *ASRProviderResolver) Resolve(ctx context.Context, configID string) (*biz.ASRProviderBinding, error) {
	config, err := r.repo.GetASRProviderConfig(ctx, configID)
	if err != nil {
		return nil, err
	}
	if config.Status == biz.AIProviderConfigStatusDraft {
		return nil, fmt.Errorf("draft ASR provider config cannot run a session")
	}
	return r.resolveConfig(ctx, config)
}

func (r *ASRProviderResolver) resolveConfig(ctx context.Context, config *biz.ASRProviderConfig) (*biz.ASRProviderBinding, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if r.localFake {
		binding := &biz.ASRProviderBinding{ConfigID: config.ID, Provider: r.fakeProvider}
		return binding, binding.Validate()
	}
	credential, err := r.credentials.GetProviderCredential(ctx, biz.ProviderCredentialNameBailianParaformer)
	if err != nil {
		return nil, err
	}
	if err := credential.Validate(); err != nil {
		return nil, fmt.Errorf("Bailian provider credential is invalid: %w", err)
	}
	r.logger.Info("Bailian provider credential loaded",
		"provider", credential.Provider,
		"credential_version", credential.Version,
		"credential_updated_at", credential.UpdatedAt,
		"api_key_configured", true,
		"config_id", config.ID,
		"realtime_model", config.RealtimeModel,
	)
	r.mu.Lock()
	cached := r.providers[config.ID]
	r.mu.Unlock()
	provider := cached.provider
	if provider == nil || cached.credentialVersion != credential.Version {
		created, err := r.newBailianProvider(config, credential.APIKey)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		if existing := r.providers[config.ID]; existing.provider != nil && existing.credentialVersion == credential.Version {
			provider = existing.provider
		} else {
			r.providers[config.ID] = cachedASRProvider{credentialVersion: credential.Version, provider: created}
			provider = created
		}
		r.mu.Unlock()
	}
	binding := &biz.ASRProviderBinding{ConfigID: config.ID, Provider: provider}
	return binding, binding.Validate()
}

func (r *ASRProviderResolver) newBailianProvider(config *biz.ASRProviderConfig, apiKey string) (biz.ASRProvider, error) {
	if config.Provider != biz.ASRProviderNameBailianParaformer {
		return nil, fmt.Errorf("unsupported ASR provider %q", config.Provider)
	}
	bailian := r.runtime.GetBailian()
	if bailian == nil {
		return nil, fmt.Errorf("ASR Bailian infrastructure config is required")
	}
	endpoint, err := resolveBailianEndpoint(bailian.GetEndpoint(), config.WorkspaceID, r.runtime.GetAllowTestEndpoint())
	if err != nil {
		return nil, err
	}
	provider, err := paraformer.NewProvider(paraformer.Config{
		Endpoint: endpoint, APIKey: apiKey, WorkspaceID: config.WorkspaceID,
		Model: config.RealtimeModel, VocabularyID: config.VocabularyID,
		ConnectTimeout: r.runtime.GetConnectTimeout().AsDuration(), ReadTimeout: r.runtime.GetReadTimeout().AsDuration(),
		WriteTimeout: r.runtime.GetWriteTimeout().AsDuration(), FinishTimeout: r.runtime.GetFinishTimeout().AsDuration(),
		SessionTimeout: r.runtime.GetSessionTimeout().AsDuration(), MaxConcurrentSessions: r.runtime.GetMaxConcurrentSessions(),
		QueueCapacity: r.queueCapacity, SharedSlots: r.sharedSlots,
	}, r.logger)
	if err != nil {
		return nil, fmt.Errorf("create Paraformer provider for config version %d: %w", config.Version, err)
	}
	return provider, nil
}
