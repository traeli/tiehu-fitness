package biz

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
)

type meetingFakeRepo struct {
	meeting       *Meeting
	reservation   *MeetingUsageReservation
	createCalls   int
	stopCalls     int
	failPrepCalls int
}

func (r *meetingFakeRepo) FindByCreateIdempotency(_ context.Context, userID, key string) (*Meeting, error) {
	if r.meeting == nil || r.meeting.UserID != userID || r.meeting.CreateIdempotencyKey != key {
		return nil, ErrMeetingNotFound
	}
	return r.meeting, nil
}

func (r *meetingFakeRepo) CreateWithQuota(_ context.Context, input MeetingCreatePersistenceInput, quota MeetingQuotaReserveInput) (*MeetingCreatePersistenceResult, error) {
	r.createCalls++
	if r.meeting != nil {
		return &MeetingCreatePersistenceResult{Meeting: r.meeting, Reservation: r.reservation, Existing: true}, nil
	}
	grant := quota.Policy.MaxMeetingAudioSeconds
	if grant > quota.Policy.MonthlyAudioSeconds {
		grant = quota.Policy.MonthlyAudioSeconds
	}
	r.meeting = &Meeting{
		ID: input.MeetingID, UserID: input.UserID, ReservationID: quota.ReservationID,
		CreateIdempotencyKey: input.IdempotencyKey, CreateRequestFingerprint: input.RequestFingerprint,
		Status: MeetingStatusRecording, TranscriptionStatus: MeetingTranscriptionStatusPending,
		Language: input.Language, RetainAudio: input.RetainAudio, GrantedAudioSeconds: grant,
		StartedAt: input.Now, CreatedAt: input.Now, UpdatedAt: input.Now,
	}
	r.reservation = &MeetingUsageReservation{
		ID: quota.ReservationID, UserID: input.UserID, MeetingID: input.MeetingID,
		Period: quota.Period, GrantedSeconds: grant, Status: MeetingUsageReservationStatusActive,
		ExpiresAt: quota.ExpiresAt,
	}
	return &MeetingCreatePersistenceResult{Meeting: r.meeting, Reservation: r.reservation}, nil
}

func (r *meetingFakeRepo) MarkTranscriptionPrepared(_ context.Context, userID, meetingID string, session *MeetingTranscriptionSession, now time.Time) (*Meeting, error) {
	if r.meeting == nil || r.meeting.ID != meetingID || r.meeting.UserID != userID {
		return nil, ErrMeetingNotFound
	}
	r.meeting.TranscriptionSessionID = session.ID
	r.meeting.TranscriptionStatus = MeetingTranscriptionStatusConnecting
	r.meeting.UpdatedAt = now
	return r.meeting, nil
}

func (r *meetingFakeRepo) FailPreparationAndRelease(_ context.Context, userID, meetingID string, now time.Time) (*Meeting, error) {
	r.failPrepCalls++
	if r.meeting == nil || r.meeting.ID != meetingID || r.meeting.UserID != userID {
		return nil, ErrMeetingNotFound
	}
	r.meeting.Status = MeetingStatusFailed
	r.meeting.TranscriptionStatus = MeetingTranscriptionStatusFailed
	r.meeting.StoppedAt = &now
	return r.meeting, nil
}

func (r *meetingFakeRepo) Stop(_ context.Context, userID, meetingID, idempotencyKey string, now time.Time) (*Meeting, error) {
	r.stopCalls++
	if r.meeting == nil || r.meeting.ID != meetingID || r.meeting.UserID != userID {
		return nil, ErrMeetingNotFound
	}
	if r.meeting.Status == MeetingStatusRecording {
		r.meeting.Status = MeetingStatusProcessing
		r.meeting.TranscriptionStatus = MeetingTranscriptionStatusFinishing
		r.meeting.StopIdempotencyKey = idempotencyKey
		r.meeting.StoppedAt = &now
	}
	return r.meeting, nil
}

func (r *meetingFakeRepo) Get(_ context.Context, userID, meetingID string) (*Meeting, error) {
	if r.meeting == nil || r.meeting.ID != meetingID || r.meeting.UserID != userID {
		return nil, ErrMeetingNotFound
	}
	return r.meeting, nil
}

func (*meetingFakeRepo) AppendFinalTranscriptSegments(context.Context, string, string, string, []*MeetingTranscriptSegment) (int64, error) {
	return 0, nil
}

func (*meetingFakeRepo) ValidateTranscriptionIdentity(context.Context, string, string, string) error {
	return nil
}

func (r *meetingFakeRepo) FinalizeTranscription(_ context.Context, input FinalizeMeetingTranscriptionCommand) (*FinalizeMeetingTranscriptionResult, error) {
	r.meeting.Status = input.MeetingStatus
	r.meeting.TranscriptionStatus = input.TranscriptionStatus
	return &FinalizeMeetingTranscriptionResult{
		Meeting: r.meeting,
		Usage:   &MeetingUsageRecord{ActualSeconds: input.TotalAcceptedSeconds, Reason: input.SettlementReason},
	}, nil
}

func (*meetingFakeRepo) ListTranscriptSegments(context.Context, ListMeetingTranscriptInput) ([]*MeetingTranscriptSegment, bool, error) {
	return nil, false, nil
}

type meetingFakeVision struct {
	session      *MeetingTranscriptionSession
	prepareErr   error
	prepareCalls int
	cancelCalls  int
	lastCancel   CancelMeetingTranscriptionInput
}

func (v *meetingFakeVision) PrepareTranscription(_ context.Context, input PrepareMeetingTranscriptionInput) (*MeetingTranscriptionSession, error) {
	v.prepareCalls++
	if v.prepareErr != nil {
		return nil, v.prepareErr
	}
	if v.session == nil {
		v.session = &MeetingTranscriptionSession{
			ID: uuid.NewString(), WebSocketURL: "wss://vision.example.test/v1/realtime/transcriptions",
			Ticket: "single-use-ticket", ExpiresAt: time.Now().UTC().Add(time.Minute),
			GrantedAudioSeconds: input.GrantedSeconds,
			Audio: MeetingAudioSpec{
				MIMEType: "audio/pcm;rate=16000", SampleRate: 16_000, Channels: 1,
				ChunkDuration: 100 * time.Millisecond, MaxChunkBytes: 6_400,
			},
		}
	}
	return v.session, nil
}

func (v *meetingFakeVision) CancelTranscription(_ context.Context, input CancelMeetingTranscriptionInput) error {
	v.cancelCalls++
	v.lastCancel = input
	return nil
}

func TestMeetingStateTransitions(t *testing.T) {
	meetingCases := []struct {
		from, to MeetingStatus
		want     bool
	}{
		{MeetingStatusRecording, MeetingStatusProcessing, true},
		{MeetingStatusRecording, MeetingStatusCancelled, true},
		{MeetingStatusProcessing, MeetingStatusCompleted, true},
		{MeetingStatusProcessing, MeetingStatusPartiallyCompleted, true},
		{MeetingStatusPartiallyCompleted, MeetingStatusCompleted, true},
		{MeetingStatusCompleted, MeetingStatusProcessing, false},
		{MeetingStatusCancelled, MeetingStatusRecording, false},
	}
	for _, tt := range meetingCases {
		if got := tt.from.CanTransitionTo(tt.to); got != tt.want {
			t.Errorf("%s.CanTransitionTo(%s) = %v, want %v", tt.from.String(), tt.to.String(), got, tt.want)
		}
	}

	transcriptionCases := []struct {
		from, to MeetingTranscriptionStatus
		want     bool
	}{
		{MeetingTranscriptionStatusPending, MeetingTranscriptionStatusConnecting, true},
		{MeetingTranscriptionStatusConnecting, MeetingTranscriptionStatusStreaming, true},
		{MeetingTranscriptionStatusStreaming, MeetingTranscriptionStatusFinishing, true},
		{MeetingTranscriptionStatusFinishing, MeetingTranscriptionStatusSucceeded, true},
		{MeetingTranscriptionStatusSucceeded, MeetingTranscriptionStatusStreaming, false},
		{MeetingTranscriptionStatusFailed, MeetingTranscriptionStatusFinishing, false},
	}
	for _, tt := range transcriptionCases {
		if got := tt.from.CanTransitionTo(tt.to); got != tt.want {
			t.Errorf("%s.CanTransitionTo(%s) = %v, want %v", tt.from.String(), tt.to.String(), got, tt.want)
		}
	}
}

func TestValidateMeetingTranscriptionSessionWebSocketPolicy(t *testing.T) {
	now := time.Now().UTC()
	base := MeetingTranscriptionSession{
		ID: uuid.NewString(), Ticket: "single-use-ticket", ExpiresAt: now.Add(time.Minute),
		GrantedAudioSeconds: 60,
		Audio: MeetingAudioSpec{
			MIMEType: "audio/pcm;rate=16000", SampleRate: 16_000, Channels: 1,
			ChunkDuration: 200 * time.Millisecond, MaxChunkBytes: 6_400,
		},
	}
	tests := []struct {
		name, websocketURL string
		wantErr            bool
	}{
		{name: "secure public endpoint", websocketURL: "wss://vision.example.test/v1/realtime/transcriptions"},
		{name: "IPv4 loopback development endpoint", websocketURL: "ws://127.0.0.1:8100/v1/realtime/transcriptions"},
		{name: "IPv6 loopback development endpoint", websocketURL: "ws://[::1]:8100/v1/realtime/transcriptions"},
		{name: "localhost development endpoint", websocketURL: "ws://localhost:8100/v1/realtime/transcriptions"},
		{name: "public plaintext endpoint", websocketURL: "ws://vision.example.test/v1/realtime/transcriptions", wantErr: true},
		{name: "HTTP endpoint", websocketURL: "http://127.0.0.1:8100/v1/realtime/transcriptions", wantErr: true},
		{name: "credential in endpoint", websocketURL: "wss://user@vision.example.test/v1/realtime/transcriptions", wantErr: true},
		{name: "query in endpoint", websocketURL: "wss://vision.example.test/v1/realtime/transcriptions?ticket=secret", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := base
			session.WebSocketURL = tt.websocketURL
			err := validateMeetingTranscriptionSession(&session, 60, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateMeetingTranscriptionSession() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFinalTranscriptSegmentAcceptsAutoLanguage(t *testing.T) {
	meetingID := uuid.NewString()
	segment := &MeetingTranscriptSegment{
		ID: uuid.NewString(), SequenceNo: 1, EndOffset: time.Second,
		Content: "auto language transcript", Language: MeetingLanguageAuto, CreatedAt: time.Now().UTC(),
	}
	if err := validateFinalTranscriptSegment(segment, meetingID, 0); err != nil {
		t.Fatalf("validateFinalTranscriptSegment(auto) error = %v", err)
	}
	if segment.MeetingID != meetingID {
		t.Fatalf("segment meeting ID = %q, want %q", segment.MeetingID, meetingID)
	}
}

func TestMeetingCreateAndStopAreIdempotent(t *testing.T) {
	repo := &meetingFakeRepo{}
	vision := &meetingFakeVision{}
	quotaRepo := &quotaFakeRepo{}
	quotaRepo.defaultPolicy = mustMeetingTestPolicy(t)
	rateLimiter := &quotaFakeRateLimiter{decision: MeetingCreateRateDecision{Allowed: true}}
	quotaUsecase, err := NewMeetingQuotaUsecase(quotaRepo, quotaRepo, rateLimiter)
	if err != nil {
		t.Fatal(err)
	}
	usecase, err := NewMeetingUsecase(repo, quotaUsecase, vision)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	createKey := uuid.NewString()
	now := time.Now().UTC()
	command := CreateMeetingCommand{
		UserID: userID, IdempotencyKey: createKey, Language: MeetingLanguageAuto,
		TranscriptionConsent: true, Now: now,
	}
	first, err := usecase.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := usecase.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Meeting.ID != second.Meeting.ID || repo.createCalls != 1 || rateLimiter.calls != 1 {
		t.Fatalf("idempotent create = first %#v, second %#v, create calls %d, rate calls %d", first, second, repo.createCalls, rateLimiter.calls)
	}
	if vision.prepareCalls != 2 {
		t.Fatalf("vision prepare calls = %d, want 2 idempotent ticket retrievals", vision.prepareCalls)
	}

	conflicting := command
	conflicting.RetainAudio = true
	if _, err := usecase.Create(context.Background(), conflicting); kratoserrors.Reason(err) != "MEETING_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflicting create error = %v", err)
	}

	stopKey := uuid.NewString()
	stopped, err := usecase.Stop(context.Background(), userID, first.Meeting.ID, stopKey, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := usecase.Stop(context.Background(), userID, first.Meeting.ID, stopKey, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != MeetingStatusProcessing || repeated.Status != MeetingStatusProcessing || repeated.StopIdempotencyKey != stopKey {
		t.Fatalf("idempotent stop = first %#v, second %#v", stopped, repeated)
	}
	if vision.cancelCalls != 2 || vision.lastCancel.Reason != MeetingTranscriptionCancelReasonUserCancelled {
		t.Fatalf("stop cancellation = calls %d, input %#v", vision.cancelCalls, vision.lastCancel)
	}
}

func TestMeetingPreparationFailureCompensates(t *testing.T) {
	repo := &meetingFakeRepo{}
	vision := &meetingFakeVision{prepareErr: stderrors.New("provider unavailable")}
	quotaRepo := &quotaFakeRepo{defaultPolicy: mustMeetingTestPolicy(t)}
	quotaUsecase, err := NewMeetingQuotaUsecase(
		quotaRepo, quotaRepo,
		&quotaFakeRateLimiter{decision: MeetingCreateRateDecision{Allowed: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	usecase, err := NewMeetingUsecase(repo, quotaUsecase, vision)
	if err != nil {
		t.Fatal(err)
	}
	_, err = usecase.Create(context.Background(), CreateMeetingCommand{
		UserID: uuid.NewString(), IdempotencyKey: uuid.NewString(), Language: MeetingLanguageZhCN,
		TranscriptionConsent: true, Now: time.Now().UTC(),
	})
	if err == nil || repo.failPrepCalls != 1 || repo.meeting.Status != MeetingStatusFailed || repo.meeting.TranscriptionStatus != MeetingTranscriptionStatusFailed {
		t.Fatalf("preparation failure = error %v, meeting %#v, compensation calls %d", err, repo.meeting, repo.failPrepCalls)
	}
}

func mustMeetingTestPolicy(t *testing.T) MeetingQuotaPolicy {
	t.Helper()
	policy, err := NewMeetingQuotaPolicy(validMeetingQuotaConfig())
	if err != nil {
		t.Fatal(err)
	}
	return policy
}
