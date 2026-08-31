package biz

import (
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

const pcmS16LEBytesPerSample = int64(2)

var (
	ErrTranscriptionNotFound       = stderrors.New("transcription session not found")
	ErrTranscriptionConflict       = stderrors.New("transcription session conflict")
	ErrTranscriptionStateConflict  = stderrors.New("transcription session state conflict")
	ErrTranscriptionSequence       = stderrors.New("transcription audio sequence invalid")
	ErrTranscriptionAudioLimit     = stderrors.New("transcription audio limit reached")
	ErrTranscriptionDuplicateChunk = stderrors.New("transcription audio chunk already accepted")
	ErrTranscriptionTicketInvalid  = stderrors.New("transcription ticket is invalid or already consumed")
	ErrTranscriptionTicketExpired  = stderrors.New("transcription ticket is expired")
	ErrASRBackpressure             = stderrors.New("ASR provider backpressure")
	ErrASRProviderRejected         = stderrors.New("ASR provider rejected request")
	ErrASRProviderUnavailable      = stderrors.New("ASR provider unavailable")
)

// TranscriptionSessionStatus is the durable lifecycle owned by vision-service.
type TranscriptionSessionStatus string

const (
	TranscriptionSessionStatusPending    TranscriptionSessionStatus = "pending"
	TranscriptionSessionStatusConnecting TranscriptionSessionStatus = "connecting"
	TranscriptionSessionStatusStreaming  TranscriptionSessionStatus = "streaming"
	TranscriptionSessionStatusFinishing  TranscriptionSessionStatus = "finishing"
	TranscriptionSessionStatusSucceeded  TranscriptionSessionStatus = "succeeded"
	TranscriptionSessionStatusFailed     TranscriptionSessionStatus = "failed"
	TranscriptionSessionStatusCancelled  TranscriptionSessionStatus = "cancelled"
	TranscriptionSessionStatusExpired    TranscriptionSessionStatus = "expired"
)

func ParseTranscriptionSessionStatus(raw string) (TranscriptionSessionStatus, error) {
	status := TranscriptionSessionStatus(raw)
	switch status {
	case TranscriptionSessionStatusPending, TranscriptionSessionStatusConnecting,
		TranscriptionSessionStatusStreaming, TranscriptionSessionStatusFinishing,
		TranscriptionSessionStatusSucceeded, TranscriptionSessionStatusFailed,
		TranscriptionSessionStatusCancelled, TranscriptionSessionStatusExpired:
		return status, nil
	default:
		return "", fmt.Errorf("unknown transcription session status %q", raw)
	}
}

func (s TranscriptionSessionStatus) IsTerminal() bool {
	return s == TranscriptionSessionStatusSucceeded || s == TranscriptionSessionStatusFailed ||
		s == TranscriptionSessionStatusCancelled || s == TranscriptionSessionStatusExpired
}

func (s TranscriptionSessionStatus) CanTransitionTo(next TranscriptionSessionStatus) bool {
	if s == next {
		return s == TranscriptionSessionStatusFinishing || s.IsTerminal()
	}
	switch s {
	case TranscriptionSessionStatusPending:
		return next == TranscriptionSessionStatusConnecting || next == TranscriptionSessionStatusCancelled ||
			next == TranscriptionSessionStatusExpired || next == TranscriptionSessionStatusFailed
	case TranscriptionSessionStatusConnecting:
		return next == TranscriptionSessionStatusStreaming || next == TranscriptionSessionStatusCancelled ||
			next == TranscriptionSessionStatusExpired || next == TranscriptionSessionStatusFailed
	case TranscriptionSessionStatusStreaming:
		return next == TranscriptionSessionStatusFinishing || next == TranscriptionSessionStatusCancelled ||
			next == TranscriptionSessionStatusExpired || next == TranscriptionSessionStatusFailed
	case TranscriptionSessionStatusFinishing:
		return next == TranscriptionSessionStatusSucceeded || next == TranscriptionSessionStatusCancelled ||
			next == TranscriptionSessionStatusExpired || next == TranscriptionSessionStatusFailed
	default:
		return false
	}
}

type ASRProviderName string

const (
	ASRProviderNameBailianParaformer ASRProviderName = "bailian_paraformer"
	ASRProviderNameLocalFake         ASRProviderName = "local_fake"
)

func ParseASRProviderName(raw string) (ASRProviderName, error) {
	switch ASRProviderName(raw) {
	case ASRProviderNameBailianParaformer, ASRProviderNameLocalFake:
		return ASRProviderName(raw), nil
	default:
		return "", fmt.Errorf("unknown ASR provider %q", raw)
	}
}

type AudioFormat string

const AudioFormatPCMS16LE AudioFormat = "pcm_s16le"

func ParseAudioFormat(raw string) (AudioFormat, error) {
	if AudioFormat(raw) != AudioFormatPCMS16LE {
		return "", fmt.Errorf("unknown audio format %q", raw)
	}
	return AudioFormatPCMS16LE, nil
}

type TranscriptEventType string

const (
	TranscriptEventTypePartial TranscriptEventType = "partial"
	TranscriptEventTypeFinal   TranscriptEventType = "final"
)

func ParseTranscriptEventType(raw string) (TranscriptEventType, error) {
	switch TranscriptEventType(raw) {
	case TranscriptEventTypePartial, TranscriptEventTypeFinal:
		return TranscriptEventType(raw), nil
	default:
		return "", fmt.Errorf("unknown transcript event type %q", raw)
	}
}

type MeetingLanguage string

const (
	MeetingLanguageAuto MeetingLanguage = "auto"
	MeetingLanguageZhCN MeetingLanguage = "zh-CN"
	MeetingLanguageEnUS MeetingLanguage = "en-US"
)

func ParseMeetingLanguage(raw string) (MeetingLanguage, error) {
	switch MeetingLanguage(raw) {
	case MeetingLanguageAuto, MeetingLanguageZhCN, MeetingLanguageEnUS:
		return MeetingLanguage(raw), nil
	default:
		return "", fmt.Errorf("unknown meeting language %q", raw)
	}
}

type AudioSpec struct {
	Format        AudioFormat
	MIMEType      string
	SampleRate    int32
	Channels      int32
	ChunkDuration time.Duration
	MaxChunkBytes int32
}

func (s AudioSpec) Validate() error {
	if s.Format != AudioFormatPCMS16LE || s.MIMEType != "audio/pcm" {
		return fmt.Errorf("audio must use audio/pcm PCM S16LE")
	}
	if s.SampleRate != 16_000 || s.Channels != 1 {
		return fmt.Errorf("audio must be 16000 Hz mono")
	}
	if s.ChunkDuration < 100*time.Millisecond || s.ChunkDuration > 200*time.Millisecond {
		return fmt.Errorf("audio chunk duration must be between 100ms and 200ms")
	}
	if s.MaxChunkBytes <= 0 || s.MaxChunkBytes > 65_536 {
		return fmt.Errorf("audio max chunk bytes must be between 1 and 65536")
	}
	return nil
}

func (s AudioSpec) BytesPerSecond() (int64, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	return int64(s.SampleRate) * int64(s.Channels) * pcmS16LEBytesPerSample, nil
}

type AudioChunk struct {
	SessionID  string
	Sequence   int64
	Data       []byte
	CapturedAt time.Time
}

func (c AudioChunk) Validate(spec AudioSpec) error {
	if _, err := uuid.Parse(c.SessionID); err != nil {
		return fmt.Errorf("audio chunk session id is invalid: %w", err)
	}
	if c.Sequence <= 0 {
		return fmt.Errorf("audio chunk sequence must be positive")
	}
	if len(c.Data) == 0 || int64(len(c.Data)) > int64(spec.MaxChunkBytes) {
		return fmt.Errorf("audio chunk bytes must be between 1 and %d", spec.MaxChunkBytes)
	}
	if len(c.Data)%int(pcmS16LEBytesPerSample) != 0 {
		return fmt.Errorf("audio chunk must contain complete PCM samples")
	}
	return nil
}

type TranscriptSegment struct {
	ID           string
	SessionID    string
	Sequence     int64
	StartOffset  time.Duration
	EndOffset    time.Duration
	SpeakerLabel string
	Content      string
	Language     MeetingLanguage
	Confidence   float64
	CreatedAt    time.Time
}

// TranscriptEvent is one provider-neutral partial or final recognition update.
// Revision increases when the provider replaces the same logical sentence.
type TranscriptEvent struct {
	Type                  TranscriptEventType
	Segment               TranscriptSegment
	Revision              int32
	ProviderUsageDuration time.Duration
}

func (e TranscriptEvent) Validate() error {
	if _, err := ParseTranscriptEventType(string(e.Type)); err != nil {
		return err
	}
	if e.Revision <= 0 {
		return fmt.Errorf("transcript event revision must be positive")
	}
	if e.ProviderUsageDuration < 0 {
		return fmt.Errorf("provider usage duration must not be negative")
	}
	if e.Type == TranscriptEventTypeFinal {
		return e.Segment.Validate()
	}
	if e.Segment.Sequence <= 0 || e.Segment.StartOffset < 0 || e.Segment.EndOffset < e.Segment.StartOffset || strings.TrimSpace(e.Segment.Content) == "" {
		return fmt.Errorf("partial transcript segment is invalid")
	}
	return nil
}

func (s TranscriptSegment) Validate() error {
	if _, err := uuid.Parse(s.ID); err != nil {
		return fmt.Errorf("segment id is invalid: %w", err)
	}
	if s.Sequence <= 0 || s.StartOffset < 0 || s.EndOffset < s.StartOffset {
		return fmt.Errorf("segment sequence or offsets are invalid")
	}
	if strings.TrimSpace(s.Content) == "" || s.Confidence < 0 || s.Confidence > 1 {
		return fmt.Errorf("segment content or confidence is invalid")
	}
	if _, err := ParseMeetingLanguage(string(s.Language)); err != nil {
		return err
	}
	return nil
}

type ASRAttempt struct {
	ID            string
	SessionID     string
	Provider      ASRProviderName
	Status        ASRAttemptStatus
	AttemptNumber int32
	StartedAt     time.Time
	FinishedAt    *time.Time
	ErrorCode     string
}

type GrantedAudioDuration time.Duration

func (d GrantedAudioDuration) Duration() time.Duration { return time.Duration(d) }

type AcceptedAudioDuration time.Duration

func (d AcceptedAudioDuration) Duration() time.Duration { return time.Duration(d) }

type ASRJobStatus string

const (
	ASRJobStatusPending    ASRJobStatus = "pending"
	ASRJobStatusProcessing ASRJobStatus = "processing"
	ASRJobStatusSucceeded  ASRJobStatus = "succeeded"
	ASRJobStatusFailed     ASRJobStatus = "failed"
	ASRJobStatusCancelled  ASRJobStatus = "cancelled"
)

func ParseASRJobStatus(raw string) (ASRJobStatus, error) {
	switch ASRJobStatus(raw) {
	case ASRJobStatusPending, ASRJobStatusProcessing, ASRJobStatusSucceeded, ASRJobStatusFailed, ASRJobStatusCancelled:
		return ASRJobStatus(raw), nil
	default:
		return "", fmt.Errorf("unknown ASR job status %q", raw)
	}
}

type ASRAttemptStatus string

const (
	ASRAttemptStatusProcessing ASRAttemptStatus = "processing"
	ASRAttemptStatusSucceeded  ASRAttemptStatus = "succeeded"
	ASRAttemptStatusFailed     ASRAttemptStatus = "failed"
	ASRAttemptStatusCancelled  ASRAttemptStatus = "cancelled"
)

func ParseASRAttemptStatus(raw string) (ASRAttemptStatus, error) {
	switch ASRAttemptStatus(raw) {
	case ASRAttemptStatusProcessing, ASRAttemptStatusSucceeded, ASRAttemptStatusFailed, ASRAttemptStatusCancelled:
		return ASRAttemptStatus(raw), nil
	default:
		return "", fmt.Errorf("unknown ASR attempt status %q", raw)
	}
}

type TranscriptionSession struct {
	ID                   string
	ProviderConfigID     string
	MeetingID            string
	UserID               string
	ReservationID        string
	Language             MeetingLanguage
	Status               TranscriptionSessionStatus
	Provider             ASRProviderName
	IdempotencyKey       string
	GrantedAudioDuration GrantedAudioDuration
	AcceptedAudioBytes   int64
	LastAudioSequence    int64
	FailureCode          string
	FinishedAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (s TranscriptionSession) AcceptedAudioDuration(spec AudioSpec) (AcceptedAudioDuration, error) {
	bytesPerSecond, err := spec.BytesPerSecond()
	if err != nil {
		return 0, err
	}
	if s.AcceptedAudioBytes < 0 || s.AcceptedAudioBytes > math.MaxInt64/int64(time.Second) {
		return 0, fmt.Errorf("accepted audio bytes are out of range")
	}
	return AcceptedAudioDuration(s.AcceptedAudioBytes * int64(time.Second) / bytesPerSecond), nil
}

func (s TranscriptionSession) GrantedAudioBytes(spec AudioSpec) (int64, error) {
	bytesPerSecond, err := spec.BytesPerSecond()
	if err != nil {
		return 0, err
	}
	seconds := int64(s.GrantedAudioDuration.Duration() / time.Second)
	if seconds <= 0 || seconds > math.MaxInt64/bytesPerSecond {
		return 0, fmt.Errorf("granted audio duration is out of range")
	}
	return seconds * bytesPerSecond, nil
}

type PrepareTranscriptionInput struct {
	MeetingID            string
	UserID               string
	ReservationID        string
	Language             MeetingLanguage
	GrantedAudioDuration time.Duration
	IdempotencyKey       string
}

type TranscriptionTicket struct {
	Value     string
	ExpiresAt time.Time
}

type TranscriptionConnection struct {
	Session      *TranscriptionSession
	WebSocketURL string
	Ticket       TranscriptionTicket
	Audio        AudioSpec
}

type TicketClaims struct {
	Version             int32     `json:"version"`
	SessionID           string    `json:"session_id"`
	MeetingID           string    `json:"meeting_id"`
	UserID              string    `json:"user_id"`
	GrantedAudioSeconds int64     `json:"granted_audio_seconds"`
	Audio               AudioSpec `json:"audio"`
	ExpiresAt           time.Time `json:"expires_at"`
}

func (c TicketClaims) Validate(now time.Time) error {
	if c.Version != 1 {
		return fmt.Errorf("transcription ticket version is invalid")
	}
	for name, value := range map[string]string{"session_id": c.SessionID, "meeting_id": c.MeetingID, "user_id": c.UserID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("transcription ticket %s is invalid: %w", name, err)
		}
	}
	if c.GrantedAudioSeconds <= 0 || c.GrantedAudioSeconds > int64((24*time.Hour)/time.Second) {
		return fmt.Errorf("transcription ticket audio grant is invalid")
	}
	if err := c.Audio.Validate(); err != nil {
		return fmt.Errorf("transcription ticket audio spec is invalid: %w", err)
	}
	if c.ExpiresAt.IsZero() || !c.ExpiresAt.After(now) {
		return ErrTranscriptionTicketExpired
	}
	return nil
}

type AcceptAudioResult struct {
	Session      *TranscriptionSession
	Duplicate    bool
	LimitReached bool
}

type TranscriptionSessionRepo interface {
	CreateOrGet(context.Context, *TranscriptionSession) (*TranscriptionSession, bool, error)
	Get(context.Context, string, string) (*TranscriptionSession, error)
	Transition(context.Context, string, []TranscriptionSessionStatus, TranscriptionSessionStatus, string) (*TranscriptionSession, error)
	AcceptAudio(context.Context, string, int64, int64, int64) (*AcceptAudioResult, error)
	Complete(context.Context, string, []TranscriptSegment) (*TranscriptionSession, error)
}

type TranscriptionTicketRepo interface {
	Issue(context.Context, TicketClaims) (*TranscriptionTicket, error)
	Consume(context.Context, string) (*TicketClaims, error)
	RevokeSession(context.Context, string) error
}

type ASRAttemptRepo interface {
	StartAttempt(context.Context, string, ASRProviderName) (*ASRAttempt, error)
	FinishAttempt(context.Context, string, ASRAttemptStatus, string) error
}

type ASRProvider interface {
	Name() ASRProviderName
	Start(context.Context, *TranscriptionSession, AudioSpec) (ASRSession, error)
}

type ASRSession interface {
	Events() <-chan TranscriptEvent
	PushAudio(context.Context, AudioChunk) error
	Finish(context.Context) ([]TranscriptSegment, error)
	Cancel(context.Context) error
}

type FinalTranscriptSink interface {
	StoreFinalSegments(context.Context, *TranscriptionSession, []TranscriptSegment) error
}

type TranscriptionUsageSink interface {
	ReportTranscriptionUsage(context.Context, *TranscriptionSession, time.Duration) error
}

type TranscriptionPolicy struct {
	WebSocketURL                   string
	AllowInsecureLoopbackWebSocket bool
	TicketTTL                      time.Duration
	Audio                          AudioSpec
	MaxQueueChunks                 int32
}

type activeASRSession struct {
	mu        sync.Mutex
	session   ASRSession
	attemptID string
}

type TranscriptionUsecase struct {
	repo      TranscriptionSessionRepo
	tickets   TranscriptionTicketRepo
	attempts  ASRAttemptRepo
	providers ASRProviderResolver
	finalSink FinalTranscriptSink
	usageSink TranscriptionUsageSink
	policy    TranscriptionPolicy
	mu        sync.Mutex
	active    map[string]*activeASRSession
}

func NewTranscriptionUsecase(repo TranscriptionSessionRepo, tickets TranscriptionTicketRepo, attempts ASRAttemptRepo, providers ASRProviderResolver, finalSink FinalTranscriptSink, usageSink TranscriptionUsageSink, policy TranscriptionPolicy) (*TranscriptionUsecase, error) {
	if repo == nil || tickets == nil {
		return nil, fmt.Errorf("transcription repository and ticket repository are required")
	}
	parsedURL, err := url.Parse(policy.WebSocketURL)
	secureWebSocket := err == nil && parsedURL.Scheme == "wss"
	localDevelopmentWebSocket := err == nil && policy.AllowInsecureLoopbackWebSocket && parsedURL.Scheme == "ws" && isLoopbackWebSocketHost(parsedURL.Hostname())
	if (!secureWebSocket && !localDevelopmentWebSocket) || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return nil, fmt.Errorf("transcription websocket URL must use WSS, except explicit local development may use loopback WS")
	}
	if policy.TicketTTL <= 0 || policy.TicketTTL > 5*time.Minute {
		return nil, fmt.Errorf("transcription ticket TTL must be between zero and five minutes")
	}
	if err := policy.Audio.Validate(); err != nil {
		return nil, err
	}
	if policy.MaxQueueChunks <= 0 || policy.MaxQueueChunks > 1_024 {
		return nil, fmt.Errorf("transcription max queue chunks must be between 1 and 1024")
	}
	return &TranscriptionUsecase{repo: repo, tickets: tickets, attempts: attempts, providers: providers, finalSink: finalSink, usageSink: usageSink, policy: policy, active: make(map[string]*activeASRSession)}, nil
}

func (uc *TranscriptionUsecase) AudioSpec() AudioSpec {
	return uc.policy.Audio
}

func (uc *TranscriptionUsecase) Prepare(ctx context.Context, input PrepareTranscriptionInput) (*TranscriptionConnection, error) {
	if ctx == nil {
		return nil, kratoserrors.BadRequest("CONTEXT_REQUIRED", "context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validatePrepareInput(input); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if uc.providers == nil {
		return nil, kratoserrors.ServiceUnavailable("ASR_PROVIDER_UNAVAILABLE", "ASR provider is not configured")
	}
	binding, err := uc.providers.ResolveActive(ctx)
	if err != nil {
		return nil, kratoserrors.ServiceUnavailable("ASR_PROVIDER_UNAVAILABLE", "active ASR provider could not be loaded").WithCause(err)
	}
	if err := binding.Validate(); err != nil {
		return nil, kratoserrors.InternalServer("ASR_PROVIDER_INVALID", "active ASR provider is invalid").WithCause(err)
	}
	provider := binding.Provider.Name()
	if provider == ASRProviderNameLocalFake && !uc.policy.AllowInsecureLoopbackWebSocket {
		return nil, kratoserrors.InternalServer("ASR_PROVIDER_INVALID", "local fake ASR provider requires the local development WebSocket")
	}
	if _, err := ParseASRProviderName(string(provider)); err != nil {
		return nil, kratoserrors.InternalServer("ASR_PROVIDER_INVALID", "configured ASR provider is invalid").WithCause(err)
	}
	session := &TranscriptionSession{
		ID: uuid.NewString(), ProviderConfigID: binding.ConfigID, MeetingID: input.MeetingID, UserID: input.UserID, ReservationID: input.ReservationID,
		Language: input.Language, Status: TranscriptionSessionStatusPending, Provider: provider,
		IdempotencyKey: input.IdempotencyKey, GrantedAudioDuration: GrantedAudioDuration(input.GrantedAudioDuration), CreatedAt: now, UpdatedAt: now,
	}
	stored, _, err := uc.repo.CreateOrGet(ctx, session)
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	if _, err := uuid.Parse(stored.ProviderConfigID); err != nil {
		return nil, kratoserrors.InternalServer("ASR_PROVIDER_CONFIG_INVALID", "transcription provider configuration is invalid").WithCause(err)
	}
	if stored.Status.IsTerminal() {
		return nil, kratoserrors.Conflict("TRANSCRIPTION_SESSION_TERMINAL", "transcription session is already terminal")
	}
	ticket, err := uc.tickets.Issue(ctx, TicketClaims{
		Version: 1, SessionID: stored.ID, MeetingID: stored.MeetingID, UserID: stored.UserID,
		GrantedAudioSeconds: int64(stored.GrantedAudioDuration.Duration() / time.Second), Audio: uc.policy.Audio,
		ExpiresAt: now.Add(uc.policy.TicketTTL),
	})
	if err != nil {
		return nil, kratoserrors.ServiceUnavailable("TRANSCRIPTION_TICKET_UNAVAILABLE", "transcription ticket could not be issued").WithCause(err)
	}
	return &TranscriptionConnection{Session: stored, WebSocketURL: uc.policy.WebSocketURL, Ticket: *ticket, Audio: uc.policy.Audio}, nil
}

func isLoopbackWebSocketHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ConsumeTicket establishes the one-time trust boundary for the public
// realtime connection. Only claims issued by the ticket repository may select
// a transcription session; client-supplied identifiers are never trusted.
func (uc *TranscriptionUsecase) ConsumeTicket(ctx context.Context, rawTicket string) (*TicketClaims, error) {
	if ctx == nil {
		return nil, kratoserrors.BadRequest("CONTEXT_REQUIRED", "context is required")
	}
	claims, err := uc.tickets.Consume(ctx, rawTicket)
	if err != nil {
		switch {
		case stderrors.Is(err, ErrTranscriptionTicketExpired):
			return nil, kratoserrors.Unauthorized("TRANSCRIPTION_SESSION_EXPIRED", "transcription session ticket is expired").WithCause(err)
		case stderrors.Is(err, ErrTranscriptionTicketInvalid):
			return nil, kratoserrors.Unauthorized("TRANSCRIPTION_TICKET_INVALID", "transcription session ticket is invalid").WithCause(err)
		case stderrors.Is(err, context.Canceled), stderrors.Is(err, context.DeadlineExceeded):
			return nil, err
		default:
			return nil, kratoserrors.ServiceUnavailable("TRANSCRIPTION_TICKET_UNAVAILABLE", "transcription session ticket could not be consumed").WithCause(err)
		}
	}
	if claims == nil {
		return nil, kratoserrors.Unauthorized("TRANSCRIPTION_TICKET_INVALID", "transcription session ticket is invalid")
	}
	if err := claims.Validate(time.Now().UTC()); err != nil {
		if stderrors.Is(err, ErrTranscriptionTicketExpired) {
			return nil, kratoserrors.Unauthorized("TRANSCRIPTION_SESSION_EXPIRED", "transcription session ticket is expired").WithCause(err)
		}
		return nil, kratoserrors.Unauthorized("TRANSCRIPTION_TICKET_INVALID", "transcription session ticket is invalid").WithCause(err)
	}
	if claims.Audio != uc.policy.Audio {
		return nil, kratoserrors.Unauthorized("TRANSCRIPTION_TICKET_INVALID", "transcription session ticket constraints are invalid")
	}
	return claims, nil
}

func validatePrepareInput(input PrepareTranscriptionInput) error {
	for name, value := range map[string]string{
		"meeting_id": input.MeetingID, "user_id": input.UserID, "reservation_id": input.ReservationID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			return kratoserrors.BadRequest("TRANSCRIPTION_IDENTIFIER_INVALID", name+" must be a UUID").WithCause(err)
		}
	}
	if _, err := ParseMeetingLanguage(string(input.Language)); err != nil {
		return kratoserrors.BadRequest("TRANSCRIPTION_LANGUAGE_INVALID", "transcription language is invalid").WithCause(err)
	}
	if input.GrantedAudioDuration <= 0 || input.GrantedAudioDuration%time.Second != 0 || input.GrantedAudioDuration > 24*time.Hour {
		return kratoserrors.BadRequest("TRANSCRIPTION_GRANT_INVALID", "granted audio duration must be whole seconds between 1 second and 24 hours")
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" || len(key) > 128 {
		return kratoserrors.BadRequest("IDEMPOTENCY_KEY_INVALID", "idempotency key must contain between 1 and 128 characters")
	}
	return nil
}

func (uc *TranscriptionUsecase) Get(ctx context.Context, sessionID, meetingID string) (*TranscriptionSession, error) {
	if _, err := uuid.Parse(sessionID); err != nil {
		return nil, kratoserrors.BadRequest("TRANSCRIPTION_SESSION_ID_INVALID", "session_id must be a UUID")
	}
	if _, err := uuid.Parse(meetingID); err != nil {
		return nil, kratoserrors.BadRequest("MEETING_ID_INVALID", "meeting_id must be a UUID")
	}
	session, err := uc.repo.Get(ctx, sessionID, meetingID)
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	return session, nil
}

func (uc *TranscriptionUsecase) Start(ctx context.Context, sessionID, meetingID string) (*TranscriptionSession, error) {
	if uc.providers == nil {
		return nil, kratoserrors.ServiceUnavailable("ASR_PROVIDER_UNAVAILABLE", "ASR provider is not configured")
	}
	stored, err := uc.Get(ctx, sessionID, meetingID)
	if err != nil {
		return nil, err
	}
	binding, err := uc.providers.Resolve(ctx, stored.ProviderConfigID)
	if err != nil {
		return nil, kratoserrors.ServiceUnavailable("ASR_PROVIDER_UNAVAILABLE", "transcription ASR provider could not be loaded").WithCause(err)
	}
	if err := binding.Validate(); err != nil {
		return nil, kratoserrors.InternalServer("ASR_PROVIDER_INVALID", "transcription ASR provider is invalid").WithCause(err)
	}
	provider := binding.Provider
	if stored.Provider != provider.Name() {
		return nil, kratoserrors.Conflict("TRANSCRIPTION_PROVIDER_CONFLICT", "transcription session provider does not match the configured provider")
	}
	session, err := uc.repo.Transition(ctx, sessionID, []TranscriptionSessionStatus{TranscriptionSessionStatusPending}, TranscriptionSessionStatusConnecting, "")
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	var attempt *ASRAttempt
	if uc.attempts != nil {
		attempt, err = uc.attempts.StartAttempt(ctx, sessionID, provider.Name())
		if err != nil {
			_, _ = uc.repo.Transition(ctx, sessionID, []TranscriptionSessionStatus{TranscriptionSessionStatusConnecting}, TranscriptionSessionStatusFailed, "ASR_ATTEMPT_START_FAILED")
			return nil, kratoserrors.InternalServer("ASR_ATTEMPT_START_FAILED", "ASR attempt could not be recorded").WithCause(err)
		}
	}
	asrSession, err := provider.Start(ctx, session, uc.policy.Audio)
	if err != nil {
		if attempt != nil {
			_ = uc.attempts.FinishAttempt(ctx, attempt.ID, ASRAttemptStatusFailed, "ASR_START_FAILED")
		}
		_, _ = uc.repo.Transition(ctx, sessionID, []TranscriptionSessionStatus{TranscriptionSessionStatusConnecting}, TranscriptionSessionStatusFailed, "ASR_START_FAILED")
		if stderrors.Is(err, ErrASRProviderRejected) {
			return nil, kratoserrors.Forbidden("ASR_PROVIDER_REJECTED", "ASR provider rejected the session").WithCause(err)
		}
		return nil, kratoserrors.ServiceUnavailable("ASR_PROVIDER_UNAVAILABLE", "ASR session could not be started").WithCause(err)
	}
	runtime := &activeASRSession{session: asrSession}
	if attempt != nil {
		runtime.attemptID = attempt.ID
	}
	uc.mu.Lock()
	uc.active[sessionID] = runtime
	uc.mu.Unlock()
	streaming, err := uc.repo.Transition(ctx, sessionID, []TranscriptionSessionStatus{TranscriptionSessionStatusConnecting}, TranscriptionSessionStatusStreaming, "")
	if err != nil {
		_ = asrSession.Cancel(ctx)
		if runtime.attemptID != "" {
			_ = uc.attempts.FinishAttempt(ctx, runtime.attemptID, ASRAttemptStatusCancelled, "SESSION_START_ABORTED")
		}
		uc.removeActive(sessionID)
		return nil, mapTranscriptionRepoError(err)
	}
	return streaming, nil
}

func (uc *TranscriptionUsecase) PushAudio(ctx context.Context, chunk AudioChunk) (*AcceptAudioResult, error) {
	if err := chunk.Validate(uc.policy.Audio); err != nil {
		return nil, kratoserrors.BadRequest("AUDIO_CHUNK_INVALID", "audio chunk is invalid").WithCause(err)
	}
	runtime := uc.getActive(chunk.SessionID)
	if runtime == nil {
		return nil, kratoserrors.Conflict("TRANSCRIPTION_SESSION_NOT_STREAMING", "transcription session is not streaming")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	snapshot, err := uc.repo.Get(ctx, chunk.SessionID, "")
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	grantedBytes, err := snapshot.GrantedAudioBytes(uc.policy.Audio)
	if err != nil {
		return nil, kratoserrors.InternalServer("TRANSCRIPTION_GRANT_CORRUPT", "transcription grant is invalid").WithCause(err)
	}
	chunkBytes := int64(len(chunk.Data))
	if chunk.Sequence <= snapshot.LastAudioSequence {
		result, err := uc.repo.AcceptAudio(ctx, chunk.SessionID, chunk.Sequence, chunkBytes, grantedBytes)
		if err != nil {
			return nil, mapTranscriptionRepoError(err)
		}
		return result, nil
	}
	if snapshot.Status != TranscriptionSessionStatusStreaming {
		return nil, mapTranscriptionRepoError(ErrTranscriptionStateConflict)
	}
	if snapshot.LastAudioSequence == math.MaxInt64 || chunk.Sequence != snapshot.LastAudioSequence+1 {
		return nil, mapTranscriptionRepoError(ErrTranscriptionSequence)
	}
	if snapshot.AcceptedAudioBytes > grantedBytes-chunkBytes {
		result, err := uc.repo.AcceptAudio(ctx, chunk.SessionID, chunk.Sequence, chunkBytes, grantedBytes)
		if err != nil {
			return nil, mapTranscriptionRepoError(err)
		}
		return result, nil
	}
	if err := runtime.session.PushAudio(ctx, chunk); err != nil {
		if stderrors.Is(err, ErrASRBackpressure) {
			return nil, kratoserrors.ServiceUnavailable("TRANSCRIPTION_BACKPRESSURE", "transcription provider queue is full").WithCause(err)
		}
		if runtime.attemptID != "" {
			_ = uc.attempts.FinishAttempt(ctx, runtime.attemptID, ASRAttemptStatusFailed, "ASR_PUSH_FAILED")
		}
		_, _ = uc.repo.Transition(ctx, chunk.SessionID, []TranscriptionSessionStatus{TranscriptionSessionStatusStreaming, TranscriptionSessionStatusFinishing}, TranscriptionSessionStatusFailed, "ASR_PUSH_FAILED")
		uc.removeActive(chunk.SessionID)
		return nil, kratoserrors.ServiceUnavailable("ASR_PROVIDER_UNAVAILABLE", "ASR audio delivery failed").WithCause(err)
	}
	result, err := uc.repo.AcceptAudio(ctx, chunk.SessionID, chunk.Sequence, chunkBytes, grantedBytes)
	if err != nil {
		commitErr := err
		if cancelErr := runtime.session.Cancel(ctx); cancelErr != nil && !stderrors.Is(cancelErr, context.Canceled) {
			commitErr = stderrors.Join(err, fmt.Errorf("cancel provider after audio commit failure: %w", cancelErr))
		}
		if runtime.attemptID != "" {
			_ = uc.attempts.FinishAttempt(ctx, runtime.attemptID, ASRAttemptStatusFailed, "AUDIO_ACCEPT_COMMIT_FAILED")
		}
		_, _ = uc.repo.Transition(ctx, chunk.SessionID, []TranscriptionSessionStatus{TranscriptionSessionStatusStreaming, TranscriptionSessionStatusFinishing}, TranscriptionSessionStatusFailed, "AUDIO_ACCEPT_COMMIT_FAILED")
		uc.removeActive(chunk.SessionID)
		return nil, mapTranscriptionRepoError(commitErr)
	}
	return result, nil
}

func (uc *TranscriptionUsecase) TranscriptEvents(sessionID string) (<-chan TranscriptEvent, error) {
	if _, err := uuid.Parse(sessionID); err != nil {
		return nil, kratoserrors.BadRequest("TRANSCRIPTION_SESSION_ID_INVALID", "session_id must be a UUID")
	}
	runtime := uc.getActive(sessionID)
	if runtime == nil {
		return nil, kratoserrors.Conflict("TRANSCRIPTION_SESSION_NOT_STREAMING", "transcription session is not streaming")
	}
	return runtime.session.Events(), nil
}

func (uc *TranscriptionUsecase) Finish(ctx context.Context, sessionID string) (*TranscriptionSession, error) {
	snapshot, err := uc.repo.Get(ctx, sessionID, "")
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	if snapshot.Status == TranscriptionSessionStatusSucceeded {
		return snapshot, nil
	}
	if snapshot.Status.IsTerminal() {
		return snapshot, nil
	}
	runtime := uc.getActive(sessionID)
	if runtime == nil {
		return nil, kratoserrors.Conflict("TRANSCRIPTION_RUNTIME_MISSING", "active transcription runtime is missing")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	snapshot, err = uc.repo.Get(ctx, sessionID, "")
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	if snapshot.Status.IsTerminal() {
		uc.removeActive(sessionID)
		return snapshot, nil
	}
	finishing, err := uc.repo.Transition(ctx, sessionID, []TranscriptionSessionStatus{TranscriptionSessionStatusStreaming, TranscriptionSessionStatusFinishing}, TranscriptionSessionStatusFinishing, "")
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	segments, err := runtime.session.Finish(ctx)
	if err != nil {
		if runtime.attemptID != "" {
			_ = uc.attempts.FinishAttempt(ctx, runtime.attemptID, ASRAttemptStatusFailed, "ASR_FINISH_FAILED")
		}
		_, _ = uc.repo.Transition(ctx, sessionID, []TranscriptionSessionStatus{TranscriptionSessionStatusFinishing}, TranscriptionSessionStatusFailed, "ASR_FINISH_FAILED")
		uc.removeActive(sessionID)
		if stderrors.Is(err, ErrASRProviderRejected) {
			return nil, kratoserrors.Forbidden("ASR_PROVIDER_REJECTED", "ASR provider rejected session completion").WithCause(err)
		}
		return nil, kratoserrors.ServiceUnavailable("ASR_PROVIDER_UNAVAILABLE", "ASR session could not be finished").WithCause(err)
	}
	var attemptErr error
	if runtime.attemptID != "" {
		attemptErr = uc.attempts.FinishAttempt(ctx, runtime.attemptID, ASRAttemptStatusSucceeded, "")
	}
	completed, err := uc.repo.Complete(ctx, sessionID, segments)
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	uc.removeActive(sessionID)
	if attemptErr != nil {
		return completed, kratoserrors.InternalServer("ASR_ATTEMPT_FINISH_FAILED", "ASR attempt completion could not be recorded").WithCause(attemptErr)
	}
	if uc.finalSink != nil {
		if err := uc.finalSink.StoreFinalSegments(ctx, finishing, segments); err != nil {
			return completed, kratoserrors.ServiceUnavailable("FINAL_TRANSCRIPT_DELIVERY_PENDING", "final transcript delivery is pending retry").WithCause(err)
		}
	}
	if uc.usageSink != nil {
		accepted, durationErr := completed.AcceptedAudioDuration(uc.policy.Audio)
		if durationErr != nil {
			return completed, kratoserrors.InternalServer("TRANSCRIPTION_USAGE_INVALID", "transcription usage is invalid").WithCause(durationErr)
		}
		if err := uc.usageSink.ReportTranscriptionUsage(ctx, completed, accepted.Duration()); err != nil {
			return completed, kratoserrors.ServiceUnavailable("TRANSCRIPTION_USAGE_DELIVERY_PENDING", "transcription usage delivery is pending retry").WithCause(err)
		}
	}
	return completed, nil
}

func (uc *TranscriptionUsecase) Cancel(ctx context.Context, sessionID, meetingID string) (*TranscriptionSession, error) {
	snapshot, err := uc.Get(ctx, sessionID, meetingID)
	if err != nil {
		return nil, err
	}
	if snapshot.Status.IsTerminal() {
		return snapshot, nil
	}
	runtime := uc.getActive(sessionID)
	if runtime != nil {
		runtime.mu.Lock()
		cancelErr := runtime.session.Cancel(ctx)
		runtime.mu.Unlock()
		if cancelErr != nil && !stderrors.Is(cancelErr, context.Canceled) {
			return nil, kratoserrors.ServiceUnavailable("ASR_CANCEL_FAILED", "ASR session could not be cancelled").WithCause(cancelErr)
		}
		if runtime.attemptID != "" {
			_ = uc.attempts.FinishAttempt(ctx, runtime.attemptID, ASRAttemptStatusCancelled, "SESSION_CANCELLED")
		}
	}
	cancelled, err := uc.repo.Transition(ctx, sessionID, []TranscriptionSessionStatus{snapshot.Status}, TranscriptionSessionStatusCancelled, "")
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	uc.removeActive(sessionID)
	if err := uc.tickets.RevokeSession(ctx, sessionID); err != nil {
		return cancelled, kratoserrors.ServiceUnavailable("TRANSCRIPTION_TICKET_REVOKE_FAILED", "transcription ticket revocation is pending retry").WithCause(err)
	}
	return cancelled, nil
}

func (uc *TranscriptionUsecase) Expire(ctx context.Context, sessionID string) (*TranscriptionSession, error) {
	snapshot, err := uc.repo.Get(ctx, sessionID, "")
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	if snapshot.Status.IsTerminal() {
		return snapshot, nil
	}
	runtime := uc.getActive(sessionID)
	if runtime != nil {
		runtime.mu.Lock()
		_ = runtime.session.Cancel(ctx)
		runtime.mu.Unlock()
		if runtime.attemptID != "" {
			_ = uc.attempts.FinishAttempt(ctx, runtime.attemptID, ASRAttemptStatusCancelled, "SESSION_EXPIRED")
		}
	}
	expired, err := uc.repo.Transition(ctx, sessionID, []TranscriptionSessionStatus{snapshot.Status}, TranscriptionSessionStatusExpired, "")
	if err != nil {
		return nil, mapTranscriptionRepoError(err)
	}
	uc.removeActive(sessionID)
	_ = uc.tickets.RevokeSession(ctx, sessionID)
	return expired, nil
}

func (uc *TranscriptionUsecase) getActive(sessionID string) *activeASRSession {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	return uc.active[sessionID]
}

func (uc *TranscriptionUsecase) removeActive(sessionID string) {
	uc.mu.Lock()
	delete(uc.active, sessionID)
	uc.mu.Unlock()
}

func mapTranscriptionRepoError(err error) error {
	switch {
	case stderrors.Is(err, context.Canceled), stderrors.Is(err, context.DeadlineExceeded):
		return err
	case stderrors.Is(err, ErrTranscriptionNotFound):
		return kratoserrors.NotFound("TRANSCRIPTION_SESSION_NOT_FOUND", "transcription session not found").WithCause(err)
	case stderrors.Is(err, ErrTranscriptionConflict):
		return kratoserrors.Conflict("TRANSCRIPTION_IDEMPOTENCY_CONFLICT", "idempotency key was already used with different input").WithCause(err)
	case stderrors.Is(err, ErrTranscriptionStateConflict):
		return kratoserrors.Conflict("TRANSCRIPTION_STATE_CONFLICT", "transcription state does not allow this operation").WithCause(err)
	case stderrors.Is(err, ErrTranscriptionSequence):
		return kratoserrors.BadRequest("AUDIO_SEQUENCE_INVALID", "audio sequence is invalid").WithCause(err)
	case stderrors.Is(err, ErrTranscriptionAudioLimit):
		return kratoserrors.Conflict("TRANSCRIPTION_AUDIO_LIMIT_REACHED", "granted audio limit has been reached").WithCause(err)
	default:
		return kratoserrors.InternalServer("TRANSCRIPTION_REPOSITORY_FAILED", "transcription persistence failed").WithCause(err)
	}
}
