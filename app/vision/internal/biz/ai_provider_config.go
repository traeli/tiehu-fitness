package biz

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	providerModelNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	workspaceIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)
	promptVersionPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// AIProviderConfigStatus models the lifecycle of immutable provider settings.
// Switching models creates and activates a new row instead of mutating a row
// already referenced by a transcription session or summary job.
type AIProviderConfigStatus string

const (
	AIProviderConfigStatusDraft   AIProviderConfigStatus = "draft"
	AIProviderConfigStatusActive  AIProviderConfigStatus = "active"
	AIProviderConfigStatusRetired AIProviderConfigStatus = "retired"
)

func ParseAIProviderConfigStatus(raw string) (AIProviderConfigStatus, error) {
	switch AIProviderConfigStatus(raw) {
	case AIProviderConfigStatusDraft, AIProviderConfigStatusActive, AIProviderConfigStatusRetired:
		return AIProviderConfigStatus(raw), nil
	default:
		return "", fmt.Errorf("unknown AI provider config status %q", raw)
	}
}

func (s AIProviderConfigStatus) CanTransitionTo(next AIProviderConfigStatus) bool {
	switch s {
	case AIProviderConfigStatusDraft:
		return next == AIProviderConfigStatusActive || next == AIProviderConfigStatusRetired
	case AIProviderConfigStatusActive:
		return next == AIProviderConfigStatusRetired
	default:
		return false
	}
}

type MeetingSummaryProviderName string

const (
	MeetingSummaryProviderNameDeepSeek  MeetingSummaryProviderName = "deepseek"
	MeetingSummaryProviderNameLocalFake MeetingSummaryProviderName = "local_fake"
)

func ParseMeetingSummaryProviderName(raw string) (MeetingSummaryProviderName, error) {
	switch MeetingSummaryProviderName(raw) {
	case MeetingSummaryProviderNameDeepSeek, MeetingSummaryProviderNameLocalFake:
		return MeetingSummaryProviderName(raw), nil
	default:
		return "", fmt.Errorf("unknown meeting summary provider %q", raw)
	}
}

type ProviderCredentialName string

const (
	ProviderCredentialNameBailianParaformer ProviderCredentialName = "bailian_paraformer"
	ProviderCredentialNameDeepSeek          ProviderCredentialName = "deepseek"
)

func ParseProviderCredentialName(raw string) (ProviderCredentialName, error) {
	switch ProviderCredentialName(raw) {
	case ProviderCredentialNameBailianParaformer, ProviderCredentialNameDeepSeek:
		return ProviderCredentialName(raw), nil
	default:
		return "", fmt.Errorf("unknown provider credential %q", raw)
	}
}

type ProviderCredential struct {
	Provider  ProviderCredentialName
	APIKey    string
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (c *ProviderCredential) Validate() error {
	if c == nil {
		return fmt.Errorf("provider credential is required")
	}
	if _, err := ParseProviderCredentialName(string(c.Provider)); err != nil {
		return err
	}
	if strings.TrimSpace(c.APIKey) == "" || c.APIKey != strings.TrimSpace(c.APIKey) || len(c.APIKey) > 4_096 {
		return fmt.Errorf("provider credential API key is invalid")
	}
	if c.Version <= 0 || c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return fmt.Errorf("provider credential version or timestamps are invalid")
	}
	return nil
}

type ASRProviderConfig struct {
	ID            string
	Version       int64
	Status        AIProviderConfigStatus
	Provider      ASRProviderName
	WorkspaceID   string
	RealtimeModel string
	FileModel     string
	VocabularyID  string
	ActivatedAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (c *ASRProviderConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("ASR provider config is required")
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return fmt.Errorf("ASR provider config ID is invalid: %w", err)
	}
	if c.Version <= 0 {
		return fmt.Errorf("ASR provider config version must be positive")
	}
	status, err := ParseAIProviderConfigStatus(string(c.Status))
	if err != nil {
		return err
	}
	if c.Provider != ASRProviderNameBailianParaformer {
		return fmt.Errorf("ASR provider config provider is unsupported")
	}
	if !workspaceIDPattern.MatchString(c.WorkspaceID) {
		return fmt.Errorf("ASR provider config workspace ID is invalid")
	}
	if !providerModelNamePattern.MatchString(c.RealtimeModel) || !providerModelNamePattern.MatchString(c.FileModel) {
		return fmt.Errorf("ASR provider config model name is invalid")
	}
	if len(c.VocabularyID) > 128 || (c.VocabularyID != "" && !providerModelNamePattern.MatchString(c.VocabularyID)) {
		return fmt.Errorf("ASR provider config vocabulary ID is invalid")
	}
	if status == AIProviderConfigStatusDraft && c.ActivatedAt != nil {
		return fmt.Errorf("draft ASR provider config must not be activated")
	}
	if status != AIProviderConfigStatusDraft && c.ActivatedAt == nil {
		return fmt.Errorf("active or retired ASR provider config requires activation time")
	}
	if c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return fmt.Errorf("ASR provider config timestamps are invalid")
	}
	return nil
}

type MeetingSummaryProviderConfig struct {
	ID                    string
	Version               int64
	Status                AIProviderConfigStatus
	Provider              MeetingSummaryProviderName
	ModelName             string
	PromptVersion         string
	MaxInputCharsPerChunk int32
	MaxChunks             int32
	MaxOutputTokens       int32
	ActivatedAt           *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (c *MeetingSummaryProviderConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("meeting summary provider config is required")
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return fmt.Errorf("meeting summary provider config ID is invalid: %w", err)
	}
	if c.Version <= 0 {
		return fmt.Errorf("meeting summary provider config version must be positive")
	}
	status, err := ParseAIProviderConfigStatus(string(c.Status))
	if err != nil {
		return err
	}
	if c.Provider != MeetingSummaryProviderNameDeepSeek {
		return fmt.Errorf("meeting summary provider config provider is unsupported")
	}
	if !providerModelNamePattern.MatchString(c.ModelName) || !promptVersionPattern.MatchString(c.PromptVersion) {
		return fmt.Errorf("meeting summary provider model or prompt version is invalid")
	}
	if c.MaxInputCharsPerChunk < 1_000 || c.MaxInputCharsPerChunk > 500_000 ||
		c.MaxChunks <= 0 || c.MaxChunks > 128 || c.MaxOutputTokens < 256 || c.MaxOutputTokens > 100_000 {
		return fmt.Errorf("meeting summary provider bounds are invalid")
	}
	if status == AIProviderConfigStatusDraft && c.ActivatedAt != nil {
		return fmt.Errorf("draft meeting summary provider config must not be activated")
	}
	if status != AIProviderConfigStatusDraft && c.ActivatedAt == nil {
		return fmt.Errorf("active or retired meeting summary provider config requires activation time")
	}
	if c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return fmt.Errorf("meeting summary provider config timestamps are invalid")
	}
	return nil
}

type ASRProviderConfigRepo interface {
	GetActiveASRProviderConfig(context.Context) (*ASRProviderConfig, error)
	GetASRProviderConfig(context.Context, string) (*ASRProviderConfig, error)
}

type MeetingSummaryProviderConfigRepo interface {
	GetActiveMeetingSummaryProviderConfig(context.Context) (*MeetingSummaryProviderConfig, error)
	GetMeetingSummaryProviderConfig(context.Context, string) (*MeetingSummaryProviderConfig, error)
}

type ProviderCredentialRepo interface {
	GetProviderCredential(context.Context, ProviderCredentialName) (*ProviderCredential, error)
}

type ASRProviderBinding struct {
	ConfigID string
	Provider ASRProvider
}

func (b *ASRProviderBinding) Validate() error {
	if b == nil || b.Provider == nil {
		return fmt.Errorf("ASR provider binding is required")
	}
	if _, err := uuid.Parse(b.ConfigID); err != nil {
		return fmt.Errorf("ASR provider binding config ID is invalid: %w", err)
	}
	if _, err := ParseASRProviderName(string(b.Provider.Name())); err != nil {
		return err
	}
	return nil
}

type ASRProviderResolver interface {
	ResolveActive(context.Context) (*ASRProviderBinding, error)
	Resolve(context.Context, string) (*ASRProviderBinding, error)
}

type MeetingSummarizerBinding struct {
	ConfigID      string
	Provider      MeetingSummaryProviderName
	ModelName     string
	PromptVersion string
	Summarizer    MeetingSummarizer
}

func (b *MeetingSummarizerBinding) Validate() error {
	if b == nil || b.Summarizer == nil {
		return fmt.Errorf("meeting summarizer binding is required")
	}
	if _, err := uuid.Parse(b.ConfigID); err != nil {
		return fmt.Errorf("meeting summarizer binding config ID is invalid: %w", err)
	}
	if _, err := ParseMeetingSummaryProviderName(string(b.Provider)); err != nil {
		return err
	}
	if !providerModelNamePattern.MatchString(strings.TrimSpace(b.ModelName)) || !promptVersionPattern.MatchString(strings.TrimSpace(b.PromptVersion)) {
		return fmt.Errorf("meeting summarizer binding model metadata is invalid")
	}
	return nil
}

type MeetingSummarizerResolver interface {
	ResolveActive(context.Context) (*MeetingSummarizerBinding, error)
	Resolve(context.Context, string) (*MeetingSummarizerBinding, error)
}
