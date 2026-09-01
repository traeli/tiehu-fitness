package data

import (
	"context"
	stderrors "errors"
	"sync"
	"testing"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data/model"
	"gorm.io/gorm"
)

type meetingDataVisionGateway struct {
	mu               sync.Mutex
	session          *biz.MeetingTranscriptionSession
	err              error
	summaryTaskCount int
}

func (g *meetingDataVisionGateway) PrepareTranscription(_ context.Context, input biz.PrepareMeetingTranscriptionInput) (*biz.MeetingTranscriptionSession, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return nil, g.err
	}
	if g.session == nil {
		g.session = &biz.MeetingTranscriptionSession{
			ID: uuid.NewString(), WebSocketURL: "wss://vision.example.test/v1/realtime/transcriptions",
			Ticket: "single-use-ticket", ExpiresAt: time.Now().UTC().Add(time.Minute),
			GrantedAudioSeconds: input.GrantedSeconds,
			Audio: biz.MeetingAudioSpec{
				MIMEType: "audio/pcm;rate=16000", SampleRate: 16_000, Channels: 1,
				ChunkDuration: 100 * time.Millisecond, MaxChunkBytes: 6_400,
			},
		}
	}
	return g.session, nil
}

func TestMeetingRepositorySerializesConcurrentIdempotentCreate(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	usecase := newMeetingDataTestUsecase(t, db, &meetingDataVisionGateway{})
	command := biz.CreateMeetingCommand{
		UserID: userID, IdempotencyKey: uuid.NewString(), Language: biz.MeetingLanguageAuto,
		TranscriptionConsent: true, Now: time.Now().UTC().Truncate(time.Microsecond),
	}
	const workers = 8
	results := make(chan *biz.CreateMeetingResult, workers)
	errorsChannel := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := usecase.Create(context.Background(), command)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("concurrent Create() error = %v", err)
	}
	var meetingID string
	for result := range results {
		if result == nil || result.Meeting == nil || result.Session == nil {
			t.Fatal("concurrent Create() returned an incomplete result")
		}
		if meetingID == "" {
			meetingID = result.Meeting.ID
		}
		if result.Meeting.ID != meetingID {
			t.Fatalf("concurrent meeting ID = %s, want %s", result.Meeting.ID, meetingID)
		}
	}
	if meetingID == "" {
		t.Fatal("concurrent Create() returned no successful result")
	}
	assertMeetingRowCounts(t, db, userID, 1, 1)
}

func (*meetingDataVisionGateway) CancelTranscription(context.Context, biz.CancelMeetingTranscriptionInput) error {
	return nil
}

func (g *meetingDataVisionGateway) PrepareMeetingSummary(_ context.Context, _ biz.PrepareMeetingSummaryInput) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.summaryTaskCount++
	return nil
}

func TestMeetingFinalizationTransitionAllowsUnchangedDimension(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                 string
		currentMeeting       biz.MeetingStatus
		targetMeeting        biz.MeetingStatus
		currentTranscription biz.MeetingTranscriptionStatus
		targetTranscription  biz.MeetingTranscriptionStatus
		want                 bool
	}{
		{
			name:           "client stop already moved meeting to processing",
			currentMeeting: biz.MeetingStatusProcessing, targetMeeting: biz.MeetingStatusProcessing,
			currentTranscription: biz.MeetingTranscriptionStatusFinishing, targetTranscription: biz.MeetingTranscriptionStatusSucceeded,
			want: true,
		},
		{
			name:           "vision completes before client stop",
			currentMeeting: biz.MeetingStatusRecording, targetMeeting: biz.MeetingStatusProcessing,
			currentTranscription: biz.MeetingTranscriptionStatusStreaming, targetTranscription: biz.MeetingTranscriptionStatusSucceeded,
			want: true,
		},
		{
			name:           "terminal meeting cannot return to processing",
			currentMeeting: biz.MeetingStatusFailed, targetMeeting: biz.MeetingStatusProcessing,
			currentTranscription: biz.MeetingTranscriptionStatusFailed, targetTranscription: biz.MeetingTranscriptionStatusSucceeded,
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := meetingFinalizationTransitionAllowed(
				test.currentMeeting,
				test.targetMeeting,
				test.currentTranscription,
				test.targetTranscription,
			)
			if got != test.want {
				t.Fatalf("meetingFinalizationTransitionAllowed() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMeetingRepositoryLifecycleIdempotencyOwnershipAndTranscript(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	otherUserID := createQuotaTestUser(t, db)
	vision := &meetingDataVisionGateway{}
	usecase := newMeetingDataTestUsecase(t, db, vision)
	now := time.Now().UTC().Truncate(time.Microsecond)
	createKey := uuid.NewString()
	command := biz.CreateMeetingCommand{
		UserID: userID, IdempotencyKey: createKey, Language: biz.MeetingLanguageZhCN,
		TranscriptionConsent: true, Now: now,
	}
	first, err := usecase.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := usecase.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Meeting.ID != first.Meeting.ID || repeated.Meeting.ReservationID != first.Meeting.ReservationID {
		t.Fatalf("repeated create = %#v, first = %#v", repeated, first)
	}
	assertMeetingRowCounts(t, db, userID, 1, 1)

	conflicting := command
	conflicting.RetainAudio = true
	if _, err := usecase.Create(context.Background(), conflicting); kratoserrors.Reason(err) != "MEETING_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflicting create error = %v", err)
	}
	if _, err := usecase.Get(context.Background(), otherUserID, first.Meeting.ID); kratoserrors.Reason(err) != "MEETING_NOT_FOUND" {
		t.Fatalf("non-owner get error = %v", err)
	}

	stopKey := uuid.NewString()
	stopped, err := usecase.Stop(context.Background(), userID, first.Meeting.ID, stopKey, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repeatedStop, err := usecase.Stop(context.Background(), userID, first.Meeting.ID, stopKey, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != biz.MeetingStatusProcessing || repeatedStop.StopIdempotencyKey != stopKey ||
		stopped.StoppedAt == nil || repeatedStop.StoppedAt == nil || !repeatedStop.StoppedAt.Equal(*stopped.StoppedAt) {
		t.Fatalf("idempotent stop = first %#v, repeated %#v", stopped, repeatedStop)
	}

	confidence := float32(0.95)
	segments := []*biz.MeetingTranscriptSegment{
		{
			ID: uuid.NewString(), SequenceNo: 1, StartOffset: 0, EndOffset: time.Second,
			Content: "第一段", Language: biz.MeetingLanguageZhCN, Confidence: &confidence, CreatedAt: now.Add(time.Second),
		},
		{
			ID: uuid.NewString(), SequenceNo: 2, StartOffset: time.Second, EndOffset: 2 * time.Second,
			Content: "second segment", Language: biz.MeetingLanguageEnUS, CreatedAt: now.Add(2 * time.Second),
		},
	}
	batchID := uuid.NewString()
	lastSequence, err := usecase.AppendFinalTranscriptSegments(
		context.Background(), first.Meeting.ID, first.Session.ID, batchID, segments,
	)
	if err != nil || lastSequence != 2 {
		t.Fatalf("append transcript = (%d, %v)", lastSequence, err)
	}
	lastSequence, err = usecase.AppendFinalTranscriptSegments(
		context.Background(), first.Meeting.ID, first.Session.ID, batchID, segments,
	)
	if err != nil || lastSequence != 2 {
		t.Fatalf("repeat transcript batch = (%d, %v)", lastSequence, err)
	}

	changed := *segments[1]
	changed.Content = "conflicting content"
	if _, err := usecase.AppendFinalTranscriptSegments(
		context.Background(), first.Meeting.ID, first.Session.ID, batchID,
		[]*biz.MeetingTranscriptSegment{segments[0], &changed},
	); kratoserrors.Reason(err) != "TRANSCRIPT_SEGMENT_CONFLICT" {
		t.Fatalf("changed repeated batch error = %v", err)
	}
	if _, err := usecase.AppendFinalTranscriptSegments(
		context.Background(), first.Meeting.ID, first.Session.ID, uuid.NewString(),
		[]*biz.MeetingTranscriptSegment{&changed},
	); kratoserrors.Reason(err) != "TRANSCRIPT_SEGMENT_CONFLICT" {
		t.Fatalf("duplicate sequence error = %v", err)
	}

	firstPage, err := usecase.ListTranscriptSegments(context.Background(), userID, first.Meeting.ID, 1, "")
	if err != nil || len(firstPage.Segments) != 1 || firstPage.NextPageToken == "" || firstPage.Segments[0].SequenceNo != 1 {
		t.Fatalf("first transcript page = (%#v, %v)", firstPage, err)
	}
	secondPage, err := usecase.ListTranscriptSegments(context.Background(), userID, first.Meeting.ID, 1, firstPage.NextPageToken)
	if err != nil || len(secondPage.Segments) != 1 || secondPage.NextPageToken != "" || secondPage.Segments[0].SequenceNo != 2 {
		t.Fatalf("second transcript page = (%#v, %v)", secondPage, err)
	}
	if _, err := usecase.ListTranscriptSegments(context.Background(), otherUserID, first.Meeting.ID, 1, ""); kratoserrors.Reason(err) != "MEETING_NOT_FOUND" {
		t.Fatalf("non-owner transcript list error = %v", err)
	}

	var transcriptMeeting model.Meeting
	if err := db.WithContext(context.Background()).Where("id = ?", first.Meeting.ID).Take(&transcriptMeeting).Error; err != nil {
		t.Fatal(err)
	}
	persistedSegments, err := decodeTranscriptSegments(first.Meeting.ID, transcriptMeeting.TranscriptSegments)
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedSegments) != 2 {
		t.Fatalf("compact transcript segment count = %d", len(persistedSegments))
	}
}

func TestMeetingPreparationFailureAtomicallyReleasesQuota(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	usecase := newMeetingDataTestUsecase(t, db, &meetingDataVisionGateway{err: stderrors.New("vision unavailable")})
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := usecase.Create(context.Background(), biz.CreateMeetingCommand{
		UserID: userID, IdempotencyKey: uuid.NewString(), Language: biz.MeetingLanguageAuto,
		TranscriptionConsent: true, Now: now,
	})
	if err == nil {
		t.Fatal("Create() expected vision preparation failure")
	}
	var meetingRow model.Meeting
	if err := db.WithContext(context.Background()).Where("user_id = ?", userID).Take(&meetingRow).Error; err != nil {
		t.Fatal(err)
	}
	if meetingRow.Status != biz.MeetingStatusFailed.String() || meetingRow.TranscriptionStatus != biz.MeetingTranscriptionStatusFailed.String() {
		t.Fatalf("failed meeting row = %#v", meetingRow)
	}
	if meetingRow.QuotaStatus != biz.MeetingUsageReservationStatusReleased.String() || meetingRow.QuotaFinalizedAt == nil {
		t.Fatalf("released quota on meeting = %#v", meetingRow)
	}
	var period model.UserMeetingMonthlyQuota
	if err := db.WithContext(context.Background()).Where("user_id = ?", userID).Take(&period).Error; err != nil {
		t.Fatal(err)
	}
	if period.ReservedSeconds != 0 || period.ConsumedSeconds != 0 {
		t.Fatalf("quota after preparation failure = %#v", period)
	}
}

func TestMeetingTranscriptionCompletionAtomicallySettlesAndReleasesQuota(t *testing.T) {
	db := openQuotaTestDatabase(t)
	userID := createQuotaTestUser(t, db)
	vision := &meetingDataVisionGateway{}
	usecase := newMeetingDataTestUsecase(t, db, vision)
	now := time.Now().UTC().Truncate(time.Microsecond)
	created, err := usecase.Create(context.Background(), biz.CreateMeetingCommand{
		UserID: userID, IdempotencyKey: uuid.NewString(), Language: biz.MeetingLanguageZhCN,
		TranscriptionConsent: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usecase.Stop(context.Background(), userID, created.Meeting.ID, uuid.NewString(), now.Add(1500*time.Millisecond)); err != nil {
		t.Fatalf("Stop() before transcription completion error = %v", err)
	}
	if _, err := usecase.ReportTranscriptionUsage(context.Background(), created.Meeting.ID, created.Session.ID, created.Meeting.ReservationID, 2, now.Add(time.Second)); err != nil {
		t.Fatalf("ReportTranscriptionUsage() error = %v", err)
	}
	if _, err := usecase.AppendFinalTranscriptSegments(context.Background(), created.Meeting.ID, created.Session.ID, uuid.NewString(), []*biz.MeetingTranscriptSegment{{
		ID: uuid.NewString(), SequenceNo: 1, EndOffset: time.Second,
		Content: "auto language transcript", Language: biz.MeetingLanguageAuto, CreatedAt: now.Add(time.Second),
	}}); err != nil {
		t.Fatalf("AppendFinalTranscriptSegments(auto) error = %v", err)
	}
	command := biz.FinalizeMeetingTranscriptionCommand{
		MeetingID: created.Meeting.ID, SessionID: created.Session.ID, ReservationID: created.Meeting.ReservationID,
		TotalAcceptedSeconds: 2, ProviderUsageSeconds: 3, FinalizedAt: now.Add(2 * time.Second),
	}
	first, err := usecase.CompleteTranscription(context.Background(), command)
	if err != nil {
		t.Fatalf("CompleteTranscription() error = %v", err)
	}
	repeated, err := usecase.CompleteTranscription(context.Background(), command)
	if err != nil {
		t.Fatalf("CompleteTranscription(repeated) error = %v", err)
	}
	if first.Meeting.Status != biz.MeetingStatusProcessing || first.Meeting.TranscriptionStatus != biz.MeetingTranscriptionStatusSucceeded ||
		first.Usage.ActualSeconds != 2 || first.Usage.ProviderUsageSeconds != 3 || repeated.Usage.ID != first.Usage.ID {
		t.Fatalf("completion results = first %#v, repeated %#v", first, repeated)
	}
	vision.mu.Lock()
	summaryTaskCount := vision.summaryTaskCount
	vision.mu.Unlock()
	if summaryTaskCount != 2 {
		t.Fatalf("summary task submissions = %d, want 2 idempotent submissions", summaryTaskCount)
	}
	var settledMeeting model.Meeting
	if err := db.WithContext(context.Background()).Where("reservation_id = ?", created.Meeting.ReservationID).Take(&settledMeeting).Error; err != nil {
		t.Fatal(err)
	}
	var period model.UserMeetingMonthlyQuota
	if err := db.WithContext(context.Background()).Where("user_id = ? AND period_start = ?", userID, settledMeeting.QuotaPeriodStart).Take(&period).Error; err != nil {
		t.Fatal(err)
	}
	if settledMeeting.QuotaStatus != biz.MeetingUsageReservationStatusSettled.String() || settledMeeting.ActualAudioSeconds != 2 ||
		period.ReservedSeconds != 0 || period.ConsumedSeconds != 2 {
		t.Fatalf("settled compact ledger = meeting %#v, period %#v", settledMeeting, period)
	}
	vision.mu.Lock()
	vision.session = nil
	vision.mu.Unlock()
	if _, err := usecase.Create(context.Background(), biz.CreateMeetingCommand{
		UserID: userID, IdempotencyKey: uuid.NewString(), Language: biz.MeetingLanguageAuto,
		TranscriptionConsent: true, Now: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("Create() after released concurrent slot error = %v", err)
	}
}

func newMeetingDataTestUsecase(t *testing.T, db *gorm.DB, gateway biz.VisionTranscriptionGateway) *biz.MeetingUsecase {
	t.Helper()
	policy := quotaTestPolicy(t, 100, 100, 1)
	quotaRepo := NewMeetingQuotaRepo(db)
	quotaUsecase, err := biz.NewMeetingQuotaUsecase(
		staticMeetingQuotaPolicyProvider{policy: policy}, quotaRepo, alwaysAllowMeetingRateLimiter{},
	)
	if err != nil {
		t.Fatal(err)
	}
	usecase, err := biz.NewMeetingUsecase(NewMeetingRepo(db), quotaUsecase, gateway)
	if err != nil {
		t.Fatal(err)
	}
	return usecase
}

func assertMeetingRowCounts(t *testing.T, db *gorm.DB, userID string, meetings, reservations int64) {
	t.Helper()
	var meetingCount int64
	if err := db.WithContext(context.Background()).Model(&model.Meeting{}).Where("user_id = ?", userID).Count(&meetingCount).Error; err != nil {
		t.Fatal(err)
	}
	if meetingCount != meetings || meetingCount != reservations {
		t.Fatalf("compact row count = meetings %d; want meetings %d and logical reservations %d", meetingCount, meetings, reservations)
	}
}
