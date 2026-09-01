package server

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/asr/localfake"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/protobuf/types/known/durationpb"
)

type realtimeTestRepo struct {
	mu      sync.Mutex
	session *biz.TranscriptionSession
	chunks  map[int64]int64
}

func (r *realtimeTestRepo) CreateOrGet(context.Context, *biz.TranscriptionSession) (*biz.TranscriptionSession, bool, error) {
	return nil, false, stderrors.New("not used")
}

func (r *realtimeTestRepo) Get(ctx context.Context, sessionID, meetingID string) (*biz.TranscriptionSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.ID != sessionID || meetingID != "" && r.session.MeetingID != meetingID {
		return nil, biz.ErrTranscriptionNotFound
	}
	copy := *r.session
	return &copy, nil
}

func (r *realtimeTestRepo) Transition(ctx context.Context, sessionID string, allowed []biz.TranscriptionSessionStatus, next biz.TranscriptionSessionStatus, failure string) (*biz.TranscriptionSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.ID != sessionID {
		return nil, biz.ErrTranscriptionNotFound
	}
	allowedNow := false
	for _, status := range allowed {
		if status == r.session.Status {
			allowedNow = true
			break
		}
	}
	if !allowedNow || !r.session.Status.CanTransitionTo(next) {
		return nil, biz.ErrTranscriptionStateConflict
	}
	r.session.Status, r.session.FailureCode, r.session.UpdatedAt = next, failure, time.Now().UTC()
	copy := *r.session
	return &copy, nil
}

func (r *realtimeTestRepo) AcceptAudio(ctx context.Context, sessionID string, sequence, sizeBytes, grantedBytes int64) (*biz.AcceptAudioResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.ID != sessionID {
		return nil, biz.ErrTranscriptionNotFound
	}
	if _, ok := r.chunks[sequence]; ok {
		copy := *r.session
		return &biz.AcceptAudioResult{Session: &copy, Duplicate: true}, nil
	}
	if r.session.Status != biz.TranscriptionSessionStatusStreaming || sequence != r.session.LastAudioSequence+1 {
		return nil, biz.ErrTranscriptionSequence
	}
	if sizeBytes <= 0 || r.session.AcceptedAudioBytes > grantedBytes-sizeBytes {
		r.session.Status = biz.TranscriptionSessionStatusFinishing
		copy := *r.session
		return &biz.AcceptAudioResult{Session: &copy, LimitReached: true}, nil
	}
	r.chunks[sequence] = sizeBytes
	r.session.AcceptedAudioBytes += sizeBytes
	r.session.LastAudioSequence = sequence
	if r.session.AcceptedAudioBytes == grantedBytes {
		r.session.Status = biz.TranscriptionSessionStatusFinishing
	}
	copy := *r.session
	return &biz.AcceptAudioResult{Session: &copy, LimitReached: copy.Status == biz.TranscriptionSessionStatusFinishing}, nil
}

func (r *realtimeTestRepo) Complete(ctx context.Context, sessionID string, segments []biz.TranscriptSegment) (*biz.TranscriptionSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.ID != sessionID || r.session.Status != biz.TranscriptionSessionStatusFinishing {
		return nil, biz.ErrTranscriptionStateConflict
	}
	for _, segment := range segments {
		if err := segment.Validate(); err != nil {
			return nil, err
		}
	}
	r.session.Status = biz.TranscriptionSessionStatusSucceeded
	copy := *r.session
	return &copy, nil
}

type realtimeTestTickets struct {
	mu       sync.Mutex
	value    string
	claims   biz.TicketClaims
	consumed bool
}

func (t *realtimeTestTickets) Issue(context.Context, biz.TicketClaims) (*biz.TranscriptionTicket, error) {
	return nil, stderrors.New("not used")
}

func (t *realtimeTestTickets) Consume(_ context.Context, value string) (*biz.TicketClaims, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if value != t.value || t.consumed {
		return nil, biz.ErrTranscriptionTicketInvalid
	}
	t.consumed = true
	claims := t.claims
	return &claims, nil
}

func (*realtimeTestTickets) RevokeSession(context.Context, string) error { return nil }

type realtimeTestProvider struct {
	session *realtimeTestASRSession
}

func (*realtimeTestProvider) Name() biz.ASRProviderName { return biz.ASRProviderNameBailianParaformer }

func (p *realtimeTestProvider) Start(context.Context, *biz.TranscriptionSession, biz.AudioSpec) (biz.ASRSession, error) {
	return p.session, nil
}

type realtimeTestASRSession struct {
	mu        sync.Mutex
	events    chan biz.TranscriptEvent
	segment   biz.TranscriptSegment
	pushes    int
	pushErr   error
	panicPush bool
	finished  bool
}

func (s *realtimeTestASRSession) Events() <-chan biz.TranscriptEvent { return s.events }

func (s *realtimeTestASRSession) PushAudio(context.Context, biz.AudioChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.panicPush {
		panic("test provider panic")
	}
	s.pushes++
	return s.pushErr
}

func (s *realtimeTestASRSession) Finish(context.Context) ([]biz.TranscriptSegment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.finished {
		s.finished = true
		s.events <- biz.TranscriptEvent{Type: biz.TranscriptEventTypeFinal, Segment: s.segment, Revision: 1, ProviderUsageDuration: time.Second}
		close(s.events)
	}
	return []biz.TranscriptSegment{s.segment}, nil
}

func (s *realtimeTestASRSession) Cancel(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.finished {
		s.finished = true
		close(s.events)
	}
	return nil
}

func TestRealtimeWebSocketProtocolAndOneTimeTicket(t *testing.T) {
	handler, tickets, provider := newRealtimeTestHandler(t, 2)
	httpServer := kratoshttp.NewServer(kratoshttp.Timeout(30 * time.Second))
	httpServer.Route("/v1/realtime").GET("/transcriptions", handler.Handle)
	testServer := httptest.NewServer(httpServer)
	t.Cleanup(testServer.Close)

	conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
	start := fmt.Sprintf(`{"version":1,"type":"start","sessionTicket":%q,"audio":{"mimeType":"audio/pcm;rate=16000","sampleRate":16000,"channels":1,"chunkDurationMs":200}}`, tickets.value)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(start)); err != nil {
		t.Fatal(err)
	}
	var ready service.RealtimeSessionReady
	readRealtimeType(t, conn, "session_ready", &ready)
	if ready.SessionID != tickets.claims.SessionID || ready.GrantedAudioSeconds != 2 {
		t.Fatalf("session_ready = %#v", ready)
	}

	chunk := append([]byte(`{"version":1,"type":"audio_chunk","sequenceNo":1,"capturedAt":1787800000000,"mimeType":"audio/pcm;rate=16000"}`+"\n"), make([]byte, 3_200)...)
	if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
		t.Fatal(err)
	}
	var ack service.RealtimeACK
	readRealtimeType(t, conn, "ack", &ack)
	if ack.ACKSequenceNo != 1 || ack.AcceptedAudioMS != 100 {
		t.Fatalf("ack = %#v", ack)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
		t.Fatal(err)
	}
	readRealtimeType(t, conn, "ack", &ack)
	provider.session.mu.Lock()
	pushes := provider.session.pushes
	provider.session.mu.Unlock()
	if pushes != 1 {
		t.Fatalf("provider pushes = %d, want 1", pushes)
	}

	if err := conn.WriteJSON(map[string]any{"version": 1, "type": "ping", "sentAt": int64(1787800000000)}); err != nil {
		t.Fatal(err)
	}
	var pong service.RealtimePong
	readRealtimeType(t, conn, "pong", &pong)
	if pong.SentAt != 1787800000000 {
		t.Fatalf("pong = %#v", pong)
	}
	if err := conn.WriteJSON(map[string]any{"version": 1, "type": "finish", "lastSequenceNo": int64(1)}); err != nil {
		t.Fatal(err)
	}
	var segment service.RealtimeTranscriptSegment
	readRealtimeType(t, conn, "transcript_segment", &segment)
	if !segment.IsFinal || segment.Revision != 1 || segment.Content != "尾部文本" {
		t.Fatalf("segment = %#v", segment)
	}
	var finished service.RealtimeSessionFinished
	readRealtimeType(t, conn, "session_finished", &finished)
	if finished.LastACKSequenceNo != 1 || finished.FinalSegmentSequenceNo != 1 || finished.FinishReason != service.RealtimeFinishReasonClientFinished {
		t.Fatalf("session_finished = %#v", finished)
	}
	_ = conn.Close()

	replayed := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
	defer replayed.Close()
	if err := replayed.WriteMessage(websocket.TextMessage, []byte(start)); err != nil {
		t.Fatal(err)
	}
	var ticketError service.RealtimeError
	readRealtimeType(t, replayed, "error", &ticketError)
	if ticketError.Code != service.RealtimeErrorCodeTicketInvalid || ticketError.Retryable {
		t.Fatalf("replayed ticket error = %#v", ticketError)
	}
}

func TestRealtimeWebSocketOriginAndConnectionLimit(t *testing.T) {
	handler, _, _ := newRealtimeTestHandler(t, 1)
	if handler.originAllowed("utools://plugin/path") || handler.originAllowed("http://localhost:5173/path") {
		t.Fatal("origin matcher accepted an origin containing a path")
	}
	if !handler.originAllowed("file://") {
		t.Fatal("origin matcher rejected the explicitly allowed uTools production file origin")
	}
	if handler.originAllowed("") {
		t.Fatal("origin matcher accepted a missing origin")
	}
	httpServer := kratoshttp.NewServer(kratoshttp.Timeout(30 * time.Second))
	httpServer.Route("/v1/realtime").GET("/transcriptions", handler.Handle)
	testServer := httptest.NewServer(httpServer)
	t.Cleanup(testServer.Close)
	websocketURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/v1/realtime/transcriptions"

	header := http.Header{"Origin": []string{"https://evil.example"}}
	conn, response, err := websocket.DefaultDialer.Dial(websocketURL, header)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("disallowed origin response = %#v, error = %v", response, err)
	}
	if response.Body != nil {
		_ = response.Body.Close()
	}

	first := dialRealtimeTestWebSocket(t, testServer.URL, "http://127.0.0.1:3000")
	defer first.Close()
	secondHeader := http.Header{"Origin": []string{"http://127.0.0.1:3000"}}
	second, response, err := websocket.DefaultDialer.Dial(websocketURL, secondHeader)
	if second != nil {
		_ = second.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("connection limit response = %#v, error = %v", response, err)
	}
	if response.Body != nil {
		_ = response.Body.Close()
	}
}

func TestRealtimeWebSocketRejectsInvalidProtocolAndSequence(t *testing.T) {
	handler, tickets, provider := newRealtimeTestHandler(t, 2)
	httpServer := kratoshttp.NewServer(kratoshttp.Timeout(30 * time.Second))
	httpServer.Route("/v1/realtime").GET("/transcriptions", handler.Handle)
	testServer := httptest.NewServer(httpServer)
	t.Cleanup(testServer.Close)

	invalid := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
	if err := invalid.WriteJSON(map[string]any{"version": 2, "type": "start", "sessionTicket": tickets.value}); err != nil {
		t.Fatal(err)
	}
	var protocolError service.RealtimeError
	readRealtimeType(t, invalid, "error", &protocolError)
	if protocolError.Code != service.RealtimeErrorCodeProtocol {
		t.Fatalf("invalid start error = %#v", protocolError)
	}
	_ = invalid.Close()

	// Invalid protocol is rejected before the trust boundary, so it must not
	// burn the one-time ticket.
	conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
	startRealtimeTestSession(t, conn, tickets)
	chunk := realtimeTestAudioChunk(2, 3_200)
	if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
		t.Fatal(err)
	}
	var sequenceError service.RealtimeError
	readRealtimeType(t, conn, "error", &sequenceError)
	if sequenceError.Code != service.RealtimeErrorCodeAudioSequence {
		t.Fatalf("sequence error = %#v", sequenceError)
	}
	provider.session.mu.Lock()
	pushes := provider.session.pushes
	provider.session.mu.Unlock()
	if pushes != 0 {
		t.Fatalf("out-of-order audio reached provider %d times", pushes)
	}
	_ = conn.Close()
}

func TestRealtimeWebSocketQuotaBoundaries(t *testing.T) {
	tests := []struct {
		name              string
		acceptedChunks    int
		acceptedChunkSize int
		overflowSize      int
		wantACK           int64
		wantAcceptedMS    int64
	}{
		{name: "exact", acceptedChunks: 10, acceptedChunkSize: 6_400, wantACK: 10, wantAcceptedMS: 2_000},
		{name: "over", acceptedChunks: 10, acceptedChunkSize: 6_080, overflowSize: 6_400, wantACK: 10, wantAcceptedMS: 1_900},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, tickets, provider := newRealtimeTestHandler(t, 1)
			httpServer := kratoshttp.NewServer(kratoshttp.Timeout(30 * time.Second))
			httpServer.Route("/v1/realtime").GET("/transcriptions", handler.Handle)
			testServer := httptest.NewServer(httpServer)
			t.Cleanup(testServer.Close)
			conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
			defer conn.Close()
			startRealtimeTestSession(t, conn, tickets)
			for sequence := 1; sequence <= test.acceptedChunks; sequence++ {
				if err := conn.WriteMessage(websocket.BinaryMessage, realtimeTestAudioChunk(int64(sequence), test.acceptedChunkSize)); err != nil {
					t.Fatal(err)
				}
				var ack service.RealtimeACK
				readRealtimeType(t, conn, "ack", &ack)
				if ack.ACKSequenceNo != int64(sequence) {
					t.Fatalf("ack sequence = %d, want %d", ack.ACKSequenceNo, sequence)
				}
			}
			if test.overflowSize > 0 {
				if err := conn.WriteMessage(websocket.BinaryMessage, realtimeTestAudioChunk(int64(test.acceptedChunks+1), test.overflowSize)); err != nil {
					t.Fatal(err)
				}
			}
			var segment service.RealtimeTranscriptSegment
			readRealtimeType(t, conn, "transcript_segment", &segment)
			var quotaError service.RealtimeError
			readRealtimeType(t, conn, "error", &quotaError)
			if quotaError.Code != service.RealtimeErrorCodeQuotaExceeded || quotaError.LastACKSequenceNo != test.wantACK {
				t.Fatalf("quota error = %#v", quotaError)
			}
			var finished service.RealtimeSessionFinished
			readRealtimeType(t, conn, "session_finished", &finished)
			if finished.LastACKSequenceNo != test.wantACK || finished.AcceptedAudioMS != test.wantAcceptedMS ||
				finished.FinishReason != service.RealtimeFinishReasonQuotaExhausted {
				t.Fatalf("quota session_finished = %#v", finished)
			}
			provider.session.mu.Lock()
			pushes := provider.session.pushes
			provider.session.mu.Unlock()
			if pushes != test.acceptedChunks {
				t.Fatalf("provider pushes = %d, want %d", pushes, test.acceptedChunks)
			}
		})
	}
}

func TestRealtimeWebSocketServiceShutdownCancelsActiveSession(t *testing.T) {
	handler, tickets, provider := newRealtimeTestHandler(t, 1)
	httpServer := kratoshttp.NewServer(kratoshttp.Timeout(30 * time.Second))
	httpServer.Route("/v1/realtime").GET("/transcriptions", handler.Handle)
	testServer := httptest.NewServer(httpServer)
	t.Cleanup(testServer.Close)
	conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
	startRealtimeTestSession(t, conn, tickets)
	if err := handler.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("ReadMessage() expected connection closure during shutdown")
	}
	deadline := time.Now().Add(time.Second)
	for {
		provider.session.mu.Lock()
		cancelled := provider.session.finished
		provider.session.mu.Unlock()
		if cancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider session was not cancelled during shutdown")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRealtimeWebSocketClientDisconnectCancelsActiveSession(t *testing.T) {
	handler, tickets, provider := newRealtimeTestHandler(t, 1)
	testServer := newRealtimeHTTPTestServer(t, handler)
	conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
	startRealtimeTestSession(t, conn, tickets)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		provider.session.mu.Lock()
		cancelled := provider.session.finished
		provider.session.mu.Unlock()
		if cancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider session was not cancelled after client disconnect")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRealtimeWebSocketReplayWindowFormatSizeAndBackpressure(t *testing.T) {
	t.Run("replay window", func(t *testing.T) {
		handler, tickets, _ := newRealtimeTestHandler(t, 1)
		testServer := newRealtimeHTTPTestServer(t, handler)
		conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
		defer conn.Close()
		startRealtimeTestSession(t, conn, tickets)
		for sequence := int64(1); sequence <= 5; sequence++ {
			if err := conn.WriteMessage(websocket.BinaryMessage, realtimeTestAudioChunk(sequence, 3_200)); err != nil {
				t.Fatal(err)
			}
			var ack service.RealtimeACK
			readRealtimeType(t, conn, "ack", &ack)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, realtimeTestAudioChunk(1, 3_200)); err != nil {
			t.Fatal(err)
		}
		var replayError service.RealtimeError
		readRealtimeType(t, conn, "error", &replayError)
		if replayError.Code != service.RealtimeErrorCodeAudioSequence || replayError.LastACKSequenceNo != 5 {
			t.Fatalf("replay error = %#v", replayError)
		}
	})

	t.Run("unsupported format", func(t *testing.T) {
		handler, tickets, _ := newRealtimeTestHandler(t, 1)
		testServer := newRealtimeHTTPTestServer(t, handler)
		conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
		defer conn.Close()
		startRealtimeTestSession(t, conn, tickets)
		payload := append([]byte(`{"version":1,"type":"audio_chunk","sequenceNo":1,"capturedAt":1787800000000,"mimeType":"audio/webm"}`+"\n"), make([]byte, 3_200)...)
		if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			t.Fatal(err)
		}
		var formatError service.RealtimeError
		readRealtimeType(t, conn, "error", &formatError)
		if formatError.Code != service.RealtimeErrorCodeAudioUnsupported {
			t.Fatalf("format error = %#v", formatError)
		}
	})

	t.Run("oversized frame", func(t *testing.T) {
		handler, _, _ := newRealtimeTestHandler(t, 1)
		handler.cfg.maxMessageBytes = 1_024
		testServer := newRealtimeHTTPTestServer(t, handler)
		conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, make([]byte, 2_048)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.ReadMessage(); err == nil {
			t.Fatal("oversized frame expected safe connection closure")
		}
	})

	t.Run("provider backpressure", func(t *testing.T) {
		handler, tickets, provider := newRealtimeTestHandler(t, 1)
		provider.session.pushErr = biz.ErrASRBackpressure
		testServer := newRealtimeHTTPTestServer(t, handler)
		conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
		defer conn.Close()
		startRealtimeTestSession(t, conn, tickets)
		if err := conn.WriteMessage(websocket.BinaryMessage, realtimeTestAudioChunk(1, 3_200)); err != nil {
			t.Fatal(err)
		}
		var backpressure service.RealtimeError
		readRealtimeType(t, conn, "error", &backpressure)
		if backpressure.Code != service.RealtimeErrorCodeBackpressure || !backpressure.Retryable || backpressure.LastACKSequenceNo != 0 {
			t.Fatalf("backpressure error = %#v", backpressure)
		}
	})

	t.Run("provider panic recovery", func(t *testing.T) {
		handler, tickets, provider := newRealtimeTestHandler(t, 1)
		provider.session.panicPush = true
		testServer := newRealtimeHTTPTestServer(t, handler)
		conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
		defer conn.Close()
		startRealtimeTestSession(t, conn, tickets)
		if err := conn.WriteMessage(websocket.BinaryMessage, realtimeTestAudioChunk(1, 3_200)); err != nil {
			t.Fatal(err)
		}
		var internal service.RealtimeError
		readRealtimeType(t, conn, "error", &internal)
		if internal.Code != service.RealtimeErrorCodeInternal || internal.Retryable {
			t.Fatalf("panic error = %#v", internal)
		}
	})
}

func TestLocalFakeProviderWebSocketSmoke(t *testing.T) {
	provider, err := localfake.NewProvider(localfake.Config{MaxConcurrentSessions: 1, QueueCapacity: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, tickets, _ := buildRealtimeTestHandler(t, 1, provider)
	testServer := newRealtimeHTTPTestServer(t, handler)
	conn := dialRealtimeTestWebSocket(t, testServer.URL, "http://localhost:5173")
	defer conn.Close()
	startRealtimeTestSession(t, conn, tickets)
	if err := conn.WriteMessage(websocket.BinaryMessage, realtimeTestAudioChunk(1, 3_200)); err != nil {
		t.Fatal(err)
	}
	var ack service.RealtimeACK
	readRealtimeType(t, conn, "ack", &ack)
	if ack.ACKSequenceNo != 1 || ack.AcceptedAudioMS != 100 {
		t.Fatalf("ACK = %#v", ack)
	}
	var partial service.RealtimeTranscriptSegment
	readRealtimeType(t, conn, "transcript_segment", &partial)
	if partial.IsFinal || !strings.Contains(partial.Content, "本地模拟") {
		t.Fatalf("partial segment = %#v", partial)
	}
	if err := conn.WriteJSON(service.RealtimeControlMessage{
		Version: 1, Type: service.RealtimeClientMessageTypeFinish, LastSequenceNo: 1,
	}); err != nil {
		t.Fatal(err)
	}
	var final service.RealtimeTranscriptSegment
	readRealtimeType(t, conn, "transcript_segment", &final)
	if !final.IsFinal || final.SegmentID != partial.SegmentID {
		t.Fatalf("final segment = %#v", final)
	}
	var finished service.RealtimeSessionFinished
	readRealtimeType(t, conn, "session_finished", &finished)
	if finished.LastACKSequenceNo != 1 || finished.FinishReason != service.RealtimeFinishReasonClientFinished {
		t.Fatalf("session finished = %#v", finished)
	}
}

func newRealtimeTestHandler(t *testing.T, maxConnections int32) (*RealtimeWebSocketHandler, *realtimeTestTickets, *realtimeTestProvider) {
	t.Helper()
	handler, tickets, provider := buildRealtimeTestHandler(t, maxConnections, nil)
	testProvider, ok := provider.(*realtimeTestProvider)
	if !ok {
		t.Fatal("default realtime test provider has an unexpected type")
	}
	return handler, tickets, testProvider
}

func buildRealtimeTestHandler(t *testing.T, maxConnections int32, provider biz.ASRProvider) (*RealtimeWebSocketHandler, *realtimeTestTickets, biz.ASRProvider) {
	t.Helper()
	now := time.Now().UTC()
	spec := biz.AudioSpec{Format: biz.AudioFormatPCMS16LE, MIMEType: "audio/pcm", SampleRate: 16_000, Channels: 1, ChunkDuration: 200 * time.Millisecond, MaxChunkBytes: 6_400}
	session := &biz.TranscriptionSession{
		ID: uuid.NewString(), ProviderConfigID: "10000000-0000-4000-8000-000000000099",
		MeetingID: uuid.NewString(), UserID: uuid.NewString(), ReservationID: uuid.NewString(),
		Language: biz.MeetingLanguageZhCN, Status: biz.TranscriptionSessionStatusPending, Provider: biz.ASRProviderNameBailianParaformer,
		IdempotencyKey: uuid.NewString(), GrantedAudioDuration: biz.GrantedAudioDuration(2 * time.Second), CreatedAt: now, UpdatedAt: now,
	}
	repo := &realtimeTestRepo{session: session, chunks: make(map[int64]int64)}
	tickets := &realtimeTestTickets{value: strings.Repeat("t", 43), claims: biz.TicketClaims{
		Version: 1, SessionID: session.ID, MeetingID: session.MeetingID, UserID: session.UserID,
		GrantedAudioSeconds: 2, Audio: spec, ExpiresAt: now.Add(time.Minute),
	}}
	if provider == nil {
		segment := biz.TranscriptSegment{
			ID: uuid.NewString(), SessionID: session.ID, Sequence: 1, StartOffset: 0, EndOffset: time.Second,
			Content: "尾部文本", Language: biz.MeetingLanguageZhCN, Confidence: 0.95, CreatedAt: now,
		}
		provider = &realtimeTestProvider{session: &realtimeTestASRSession{events: make(chan biz.TranscriptEvent, 4), segment: segment}}
	}
	session.Provider = provider.Name()
	policy := biz.TranscriptionPolicy{
		WebSocketURL: "wss://vision.example.test/v1/realtime/transcriptions", TicketTTL: time.Minute,
		Audio: spec, MaxQueueChunks: 4,
	}
	if provider.Name() == biz.ASRProviderNameLocalFake {
		policy.WebSocketURL = "ws://127.0.0.1:8100/v1/realtime/transcriptions"
		policy.AllowInsecureLoopbackWebSocket = true
	}
	uc, err := biz.NewTranscriptionUsecase(repo, tickets, nil, &realtimeTestProviderResolver{provider: provider}, nil, nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	realtimeService, err := service.NewRealtimeTranscriptionService(uc, 4)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewRealtimeWebSocketHandler(&conf.RealtimeTranscription{
		AllowedOrigins:   []string{"http://localhost:*", "http://127.0.0.1:*", "utools://*", "file://"},
		HandshakeTimeout: durationpb.New(time.Second), IdleTimeout: durationpb.New(5 * time.Second), WriteTimeout: durationpb.New(time.Second),
		MaxMessageBytes: 16_384, MaxQueueChunks: 4, MaxConnections: maxConnections,
	}, realtimeService, logger)
	if err != nil {
		t.Fatal(err)
	}
	return handler, tickets, provider
}

type realtimeTestProviderResolver struct {
	provider biz.ASRProvider
}

func (r *realtimeTestProviderResolver) ResolveActive(context.Context) (*biz.ASRProviderBinding, error) {
	return &biz.ASRProviderBinding{ConfigID: "10000000-0000-4000-8000-000000000099", Provider: r.provider}, nil
}

func (r *realtimeTestProviderResolver) Resolve(_ context.Context, configID string) (*biz.ASRProviderBinding, error) {
	return &biz.ASRProviderBinding{ConfigID: configID, Provider: r.provider}, nil
}

func dialRealtimeTestWebSocket(t *testing.T, serverURL, origin string) *websocket.Conn {
	t.Helper()
	websocketURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/v1/realtime/transcriptions"
	conn, response, err := websocket.DefaultDialer.Dial(websocketURL, http.Header{"Origin": []string{origin}})
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		t.Fatal(err)
	}
	return conn
}

func newRealtimeHTTPTestServer(t *testing.T, handler *RealtimeWebSocketHandler) *httptest.Server {
	t.Helper()
	httpServer := kratoshttp.NewServer(kratoshttp.Timeout(30 * time.Second))
	httpServer.Route("/v1/realtime").GET("/transcriptions", handler.Handle)
	testServer := httptest.NewServer(httpServer)
	t.Cleanup(testServer.Close)
	return testServer
}

func startRealtimeTestSession(t *testing.T, conn *websocket.Conn, tickets *realtimeTestTickets) {
	t.Helper()
	start := fmt.Sprintf(`{"version":1,"type":"start","sessionTicket":%q,"audio":{"mimeType":"audio/pcm;rate=16000","sampleRate":16000,"channels":1,"chunkDurationMs":200}}`, tickets.value)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(start)); err != nil {
		t.Fatal(err)
	}
	var ready service.RealtimeSessionReady
	readRealtimeType(t, conn, "session_ready", &ready)
}

func realtimeTestAudioChunk(sequence int64, size int) []byte {
	header := fmt.Sprintf(`{"version":1,"type":"audio_chunk","sequenceNo":%d,"capturedAt":1787800000000,"mimeType":"audio/pcm;rate=16000"}`+"\n", sequence)
	return append([]byte(header), make([]byte, size)...)
}

func readRealtimeType(t *testing.T, conn *websocket.Conn, wantType string, target any) {
	t.Helper()
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode envelope %q: %v", string(payload), err)
	}
	if envelope.Type != wantType {
		t.Fatalf("message type = %q, want %q; payload = %s", envelope.Type, wantType, payload)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		t.Fatalf("decode %s: %v", wantType, err)
	}
}
