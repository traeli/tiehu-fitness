package biz

import (
	"context"
	stderrors "errors"
	"sync"
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

type transcriptionFakeRepo struct {
	mu       sync.Mutex
	sessions map[string]*TranscriptionSession
	byKey    map[string]string
	chunks   map[string]map[int64]int64
}

func newTranscriptionFakeRepo() *transcriptionFakeRepo {
	return &transcriptionFakeRepo{sessions: make(map[string]*TranscriptionSession), byKey: make(map[string]string), chunks: make(map[string]map[int64]int64)}
}

func (r *transcriptionFakeRepo) CreateOrGet(ctx context.Context, session *TranscriptionSession) (*TranscriptionSession, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.byKey[session.IdempotencyKey]; ok {
		existing := r.sessions[id]
		if existing.MeetingID != session.MeetingID || existing.UserID != session.UserID || existing.ReservationID != session.ReservationID ||
			existing.Language != session.Language || existing.GrantedAudioDuration != session.GrantedAudioDuration {
			return nil, false, ErrTranscriptionConflict
		}
		return cloneTranscriptionSession(existing), false, nil
	}
	copy := cloneTranscriptionSession(session)
	r.sessions[copy.ID] = copy
	r.byKey[copy.IdempotencyKey] = copy.ID
	r.chunks[copy.ID] = make(map[int64]int64)
	return cloneTranscriptionSession(copy), true, nil
}

func (r *transcriptionFakeRepo) Get(_ context.Context, sessionID, meetingID string) (*TranscriptionSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[sessionID]
	if !ok || meetingID != "" && meetingID != session.MeetingID {
		return nil, ErrTranscriptionNotFound
	}
	return cloneTranscriptionSession(session), nil
}

func (r *transcriptionFakeRepo) Transition(_ context.Context, sessionID string, allowed []TranscriptionSessionStatus, next TranscriptionSessionStatus, failureCode string) (*TranscriptionSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[sessionID]
	if !ok {
		return nil, ErrTranscriptionNotFound
	}
	allowedNow := false
	for _, status := range allowed {
		allowedNow = allowedNow || status == session.Status
	}
	if !allowedNow || !session.Status.CanTransitionTo(next) {
		return nil, ErrTranscriptionStateConflict
	}
	session.Status = next
	session.FailureCode = failureCode
	session.UpdatedAt = time.Now().UTC()
	return cloneTranscriptionSession(session), nil
}

func (r *transcriptionFakeRepo) AcceptAudio(_ context.Context, sessionID string, sequence, sizeBytes, grantedBytes int64) (*AcceptAudioResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[sessionID]
	if !ok {
		return nil, ErrTranscriptionNotFound
	}
	if _, duplicate := r.chunks[sessionID][sequence]; duplicate {
		return &AcceptAudioResult{Session: cloneTranscriptionSession(session), Duplicate: true}, nil
	}
	if session.Status != TranscriptionSessionStatusStreaming {
		return nil, ErrTranscriptionStateConflict
	}
	if sequence != session.LastAudioSequence+1 {
		return nil, ErrTranscriptionSequence
	}
	if session.AcceptedAudioBytes > grantedBytes-sizeBytes {
		session.Status = TranscriptionSessionStatusFinishing
		return &AcceptAudioResult{Session: cloneTranscriptionSession(session), LimitReached: true}, nil
	}
	r.chunks[sessionID][sequence] = sizeBytes
	session.AcceptedAudioBytes += sizeBytes
	session.LastAudioSequence = sequence
	if session.AcceptedAudioBytes == grantedBytes {
		session.Status = TranscriptionSessionStatusFinishing
	}
	return &AcceptAudioResult{Session: cloneTranscriptionSession(session), LimitReached: session.Status == TranscriptionSessionStatusFinishing}, nil
}

func (r *transcriptionFakeRepo) Complete(_ context.Context, sessionID string, segments []TranscriptSegment) (*TranscriptionSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[sessionID]
	if !ok {
		return nil, ErrTranscriptionNotFound
	}
	if session.Status == TranscriptionSessionStatusSucceeded {
		return cloneTranscriptionSession(session), nil
	}
	if session.Status != TranscriptionSessionStatusFinishing {
		return nil, ErrTranscriptionStateConflict
	}
	for _, segment := range segments {
		if err := segment.Validate(); err != nil {
			return nil, err
		}
	}
	session.Status = TranscriptionSessionStatusSucceeded
	return cloneTranscriptionSession(session), nil
}

func cloneTranscriptionSession(session *TranscriptionSession) *TranscriptionSession {
	copy := *session
	return &copy
}

type transcriptionFakeTickets struct {
	issues     int
	revokes    int
	claims     TicketClaims
	consumeErr error
}

func (t *transcriptionFakeTickets) Issue(_ context.Context, claims TicketClaims) (*TranscriptionTicket, error) {
	t.issues++
	t.claims = claims
	return &TranscriptionTicket{Value: "test-ticket", ExpiresAt: claims.ExpiresAt}, nil
}

func (t *transcriptionFakeTickets) Consume(context.Context, string) (*TicketClaims, error) {
	if t.consumeErr != nil {
		return nil, t.consumeErr
	}
	claims := t.claims
	return &claims, nil
}

func (t *transcriptionFakeTickets) RevokeSession(context.Context, string) error {
	t.revokes++
	return nil
}

type transcriptionFakeProvider struct {
	startErr error
	session  *transcriptionFakeASRSession
}

const transcriptionTestProviderConfigID = "10000000-0000-4000-8000-000000000099"

func (p *transcriptionFakeProvider) Name() ASRProviderName { return ASRProviderNameBailianParaformer }

func (p *transcriptionFakeProvider) ResolveActive(context.Context) (*ASRProviderBinding, error) {
	return &ASRProviderBinding{ConfigID: transcriptionTestProviderConfigID, Provider: p}, nil
}

func (p *transcriptionFakeProvider) Resolve(_ context.Context, configID string) (*ASRProviderBinding, error) {
	return &ASRProviderBinding{ConfigID: configID, Provider: p}, nil
}

func (p *transcriptionFakeProvider) Start(context.Context, *TranscriptionSession, AudioSpec) (ASRSession, error) {
	if p.startErr != nil {
		return nil, p.startErr
	}
	return p.session, nil
}

type transcriptionFakeASRSession struct {
	pushes   int
	finishes int
	cancels  int
	segments []TranscriptSegment
	pushErr  error
}

func (s *transcriptionFakeASRSession) Events() <-chan TranscriptEvent { return nil }

type transcriptionFakeAttempts struct {
	starts   int
	finishes []ASRAttemptStatus
}

func (a *transcriptionFakeAttempts) StartAttempt(_ context.Context, sessionID string, provider ASRProviderName) (*ASRAttempt, error) {
	a.starts++
	return &ASRAttempt{
		ID: uuid.NewString(), SessionID: sessionID, Provider: provider,
		Status: ASRAttemptStatusProcessing, AttemptNumber: int32(a.starts), StartedAt: time.Now().UTC(),
	}, nil
}

func (a *transcriptionFakeAttempts) FinishAttempt(_ context.Context, _ string, status ASRAttemptStatus, _ string) error {
	a.finishes = append(a.finishes, status)
	return nil
}

type transcriptionFakeFinalSink struct{ calls int }

func (s *transcriptionFakeFinalSink) StoreFinalSegments(context.Context, *TranscriptionSession, []TranscriptSegment) error {
	s.calls++
	return nil
}

type transcriptionFakeUsageSink struct{ calls int }

func (s *transcriptionFakeUsageSink) ReportTranscriptionUsage(context.Context, *TranscriptionSession, time.Duration) error {
	s.calls++
	return nil
}

func (s *transcriptionFakeASRSession) PushAudio(context.Context, AudioChunk) error {
	s.pushes++
	return s.pushErr
}

func (s *transcriptionFakeASRSession) Finish(context.Context) ([]TranscriptSegment, error) {
	s.finishes++
	return s.segments, nil
}

func (s *transcriptionFakeASRSession) Cancel(context.Context) error {
	s.cancels++
	return nil
}

func validTranscriptionPolicy() TranscriptionPolicy {
	return TranscriptionPolicy{
		WebSocketURL: "wss://vision.example.test/v1/realtime/transcriptions", TicketTTL: time.Minute,
		Audio:          AudioSpec{Format: AudioFormatPCMS16LE, MIMEType: "audio/pcm", SampleRate: 16_000, Channels: 1, ChunkDuration: 200 * time.Millisecond, MaxChunkBytes: 6_400},
		MaxQueueChunks: 64,
	}
}

func validPrepareTranscriptionInput() PrepareTranscriptionInput {
	return PrepareTranscriptionInput{
		MeetingID: uuid.NewString(), UserID: uuid.NewString(), ReservationID: uuid.NewString(),
		Language: MeetingLanguageZhCN, GrantedAudioDuration: time.Minute, IdempotencyKey: uuid.NewString(),
	}
}

func TestTranscriptionUsecaseAllowsInsecureWebSocketOnlyForExplicitLoopbackDevelopment(t *testing.T) {
	policy := validTranscriptionPolicy()
	policy.WebSocketURL = "ws://127.0.0.1:8100/v1/realtime/transcriptions"
	if _, err := NewTranscriptionUsecase(newTranscriptionFakeRepo(), &transcriptionFakeTickets{}, nil, nil, nil, nil, policy); err == nil {
		t.Fatal("NewTranscriptionUsecase() allowed loopback WS without explicit development flag")
	}
	policy.AllowInsecureLoopbackWebSocket = true
	if _, err := NewTranscriptionUsecase(newTranscriptionFakeRepo(), &transcriptionFakeTickets{}, nil, nil, nil, nil, policy); err != nil {
		t.Fatalf("NewTranscriptionUsecase() explicit loopback error = %v", err)
	}
	policy.WebSocketURL = "ws://vision.example.test/v1/realtime/transcriptions"
	if _, err := NewTranscriptionUsecase(newTranscriptionFakeRepo(), &transcriptionFakeTickets{}, nil, nil, nil, nil, policy); err == nil {
		t.Fatal("NewTranscriptionUsecase() allowed public insecure WS")
	}
}

func TestTranscriptionUsecaseRejectsLocalFakeProviderOnPublicWSS(t *testing.T) {
	localProvider := &namedTranscriptionFakeProvider{ASRProviderName: ASRProviderNameLocalFake}
	uc, err := NewTranscriptionUsecase(newTranscriptionFakeRepo(), &transcriptionFakeTickets{}, nil, localProvider, nil, nil, validTranscriptionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Prepare(context.Background(), validPrepareTranscriptionInput()); err == nil {
		t.Fatal("Prepare() allowed local fake provider on public WSS")
	}
}

type namedTranscriptionFakeProvider struct{ ASRProviderName }

func (p *namedTranscriptionFakeProvider) Name() ASRProviderName { return p.ASRProviderName }

func (p *namedTranscriptionFakeProvider) ResolveActive(context.Context) (*ASRProviderBinding, error) {
	return &ASRProviderBinding{ConfigID: transcriptionTestProviderConfigID, Provider: p}, nil
}

func (p *namedTranscriptionFakeProvider) Resolve(_ context.Context, configID string) (*ASRProviderBinding, error) {
	return &ASRProviderBinding{ConfigID: configID, Provider: p}, nil
}

func (*namedTranscriptionFakeProvider) Start(context.Context, *TranscriptionSession, AudioSpec) (ASRSession, error) {
	return nil, ErrASRProviderUnavailable
}

func TestParseASRProviderNameIncludesExplicitLocalFake(t *testing.T) {
	provider, err := ParseASRProviderName("local_fake")
	if err != nil || provider != ASRProviderNameLocalFake {
		t.Fatalf("ParseASRProviderName(local_fake) = %q, %v", provider, err)
	}
}

func TestTranscriptionSessionStatusTransitions(t *testing.T) {
	tests := []struct {
		from, to TranscriptionSessionStatus
		want     bool
	}{
		{TranscriptionSessionStatusPending, TranscriptionSessionStatusConnecting, true},
		{TranscriptionSessionStatusConnecting, TranscriptionSessionStatusStreaming, true},
		{TranscriptionSessionStatusStreaming, TranscriptionSessionStatusFinishing, true},
		{TranscriptionSessionStatusFinishing, TranscriptionSessionStatusSucceeded, true},
		{TranscriptionSessionStatusSucceeded, TranscriptionSessionStatusStreaming, false},
		{TranscriptionSessionStatusPending, TranscriptionSessionStatusSucceeded, false},
	}
	for _, test := range tests {
		if got := test.from.CanTransitionTo(test.to); got != test.want {
			t.Fatalf("%s.CanTransitionTo(%s) = %v, want %v", test.from, test.to, got, test.want)
		}
	}
	if _, err := ParseTranscriptionSessionStatus("mystery"); err == nil {
		t.Fatal("ParseTranscriptionSessionStatus() expected error")
	}
}

func TestAudioSpecAndChunkBoundaries(t *testing.T) {
	spec := validTranscriptionPolicy().Audio
	if err := spec.Validate(); err != nil {
		t.Fatalf("AudioSpec.Validate() error = %v", err)
	}
	chunk := AudioChunk{SessionID: uuid.NewString(), Sequence: 1, Data: make([]byte, spec.MaxChunkBytes)}
	if err := chunk.Validate(spec); err != nil {
		t.Fatalf("AudioChunk.Validate() error = %v", err)
	}
	chunk.Data = make([]byte, spec.MaxChunkBytes+1)
	if err := chunk.Validate(spec); err == nil {
		t.Fatal("AudioChunk.Validate() expected max size error")
	}
	invalid := spec
	invalid.SampleRate = 48_000
	if err := invalid.Validate(); err == nil {
		t.Fatal("AudioSpec.Validate() expected sample rate error")
	}
}

func TestTranscriptionUsecaseLifecycleIsIdempotent(t *testing.T) {
	repo := newTranscriptionFakeRepo()
	tickets := &transcriptionFakeTickets{}
	asr := &transcriptionFakeASRSession{}
	provider := &transcriptionFakeProvider{session: asr}
	attempts := &transcriptionFakeAttempts{}
	finalSink := &transcriptionFakeFinalSink{}
	usageSink := &transcriptionFakeUsageSink{}
	uc, err := NewTranscriptionUsecase(repo, tickets, attempts, provider, finalSink, usageSink, validTranscriptionPolicy())
	if err != nil {
		t.Fatalf("NewTranscriptionUsecase() error = %v", err)
	}
	input := validPrepareTranscriptionInput()
	first, err := uc.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	second, err := uc.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() repeat error = %v", err)
	}
	if first.Session.ID != second.Session.ID || tickets.issues != 2 {
		t.Fatalf("Prepare() idempotency session = %q/%q, issues = %d", first.Session.ID, second.Session.ID, tickets.issues)
	}
	if _, err := uc.Start(context.Background(), first.Session.ID, input.MeetingID); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	asr.segments = []TranscriptSegment{{
		ID: uuid.NewString(), SessionID: first.Session.ID, Sequence: 1, StartOffset: 0, EndOffset: time.Second,
		Content: "final transcript", Language: MeetingLanguageEnUS, Confidence: 0.9,
	}}
	chunk := AudioChunk{SessionID: first.Session.ID, Sequence: 1, Data: make([]byte, 3_200)}
	if _, err := uc.PushAudio(context.Background(), chunk); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	duplicate, err := uc.PushAudio(context.Background(), chunk)
	if err != nil {
		t.Fatalf("PushAudio() duplicate error = %v", err)
	}
	if !duplicate.Duplicate || asr.pushes != 1 {
		t.Fatalf("PushAudio() duplicate = %v, provider pushes = %d", duplicate.Duplicate, asr.pushes)
	}
	if _, err := uc.Finish(context.Background(), first.Session.ID); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	finished, err := uc.Finish(context.Background(), first.Session.ID)
	if err != nil {
		t.Fatalf("Finish() repeat error = %v", err)
	}
	if finished.Status != TranscriptionSessionStatusSucceeded || asr.finishes != 1 || finalSink.calls != 1 || usageSink.calls != 1 ||
		attempts.starts != 1 || len(attempts.finishes) != 1 || attempts.finishes[0] != ASRAttemptStatusSucceeded {
		t.Fatalf("Finish() status = %s, provider/sink calls = %d/%d/%d", finished.Status, asr.finishes, finalSink.calls, usageSink.calls)
	}
}

func TestTranscriptionUsecaseCancelAndProviderFailure(t *testing.T) {
	repo := newTranscriptionFakeRepo()
	tickets := &transcriptionFakeTickets{}
	provider := &transcriptionFakeProvider{startErr: stderrors.New("provider unavailable")}
	attempts := &transcriptionFakeAttempts{}
	uc, err := NewTranscriptionUsecase(repo, tickets, attempts, provider, nil, nil, validTranscriptionPolicy())
	if err != nil {
		t.Fatalf("NewTranscriptionUsecase() error = %v", err)
	}
	input := validPrepareTranscriptionInput()
	prepared, err := uc.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if _, err := uc.Start(context.Background(), prepared.Session.ID, input.MeetingID); kratoserrors.Reason(err) != "ASR_PROVIDER_UNAVAILABLE" {
		t.Fatalf("Start() reason = %q, want ASR_PROVIDER_UNAVAILABLE", kratoserrors.Reason(err))
	}
	failed, err := uc.Get(context.Background(), prepared.Session.ID, input.MeetingID)
	if err != nil || failed.Status != TranscriptionSessionStatusFailed {
		t.Fatalf("Get() failed state = %v, %v", failed, err)
	}
	if len(attempts.finishes) != 1 || attempts.finishes[0] != ASRAttemptStatusFailed {
		t.Fatalf("provider failure attempt statuses = %v", attempts.finishes)
	}
	cancelled, err := uc.Cancel(context.Background(), prepared.Session.ID, input.MeetingID)
	if err != nil {
		t.Fatalf("Cancel() terminal error = %v", err)
	}
	if cancelled.Status != TranscriptionSessionStatusFailed {
		t.Fatalf("Cancel() terminal status = %s", cancelled.Status)
	}
}

func TestTranscriptionUsecaseBackpressureDoesNotAcceptChunk(t *testing.T) {
	repo := newTranscriptionFakeRepo()
	tickets := &transcriptionFakeTickets{}
	asr := &transcriptionFakeASRSession{pushErr: ErrASRBackpressure}
	uc, err := NewTranscriptionUsecase(repo, tickets, nil, &transcriptionFakeProvider{session: asr}, nil, nil, validTranscriptionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	input := validPrepareTranscriptionInput()
	prepared, err := uc.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.Start(context.Background(), prepared.Session.ID, input.MeetingID); err != nil {
		t.Fatal(err)
	}
	chunk := AudioChunk{SessionID: prepared.Session.ID, Sequence: 1, Data: make([]byte, 3_200)}
	if _, err := uc.PushAudio(context.Background(), chunk); kratoserrors.Reason(err) != "TRANSCRIPTION_BACKPRESSURE" {
		t.Fatalf("PushAudio() reason = %q", kratoserrors.Reason(err))
	}
	stored, err := uc.Get(context.Background(), prepared.Session.ID, input.MeetingID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AcceptedAudioBytes != 0 || stored.LastAudioSequence != 0 || stored.Status != TranscriptionSessionStatusStreaming {
		t.Fatalf("session after backpressure = %#v", stored)
	}
	asr.pushErr = nil
	result, err := uc.PushAudio(context.Background(), chunk)
	if err != nil || result.Session.LastAudioSequence != 1 {
		t.Fatalf("PushAudio(retry) = %#v, %v", result, err)
	}
}

func TestPrepareTranscriptionRejectsNilAndBounds(t *testing.T) {
	repo := newTranscriptionFakeRepo()
	uc, err := NewTranscriptionUsecase(repo, &transcriptionFakeTickets{}, nil, nil, nil, nil, validTranscriptionPolicy())
	if err != nil {
		t.Fatalf("NewTranscriptionUsecase() error = %v", err)
	}
	input := validPrepareTranscriptionInput()
	input.GrantedAudioDuration = 0
	if _, err := uc.Prepare(context.Background(), input); kratoserrors.Reason(err) != "TRANSCRIPTION_GRANT_INVALID" {
		t.Fatalf("Prepare() reason = %q", kratoserrors.Reason(err))
	}
	if _, err := uc.Prepare(nil, validPrepareTranscriptionInput()); kratoserrors.Reason(err) != "CONTEXT_REQUIRED" {
		t.Fatalf("Prepare(nil) reason = %q", kratoserrors.Reason(err))
	}
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := uc.Prepare(cancelledCtx, validPrepareTranscriptionInput()); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("Prepare(cancelled) error = %v", err)
	}
}
