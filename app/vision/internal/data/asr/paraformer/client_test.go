package paraformer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func TestProviderLifecycleAndTranscriptMapping(t *testing.T) {
	serverErrors := make(chan error, 1)
	endpoint, closeServer := fakeProviderServer(t, func(conn *websocket.Conn, request *http.Request) error {
		if request.Header.Get("Authorization") != "Bearer test-secret-key" {
			return fmt.Errorf("authorization header mismatch")
		}
		if request.Header.Get("X-DashScope-WorkSpace") != "test-workspace" {
			return fmt.Errorf("workspace header mismatch")
		}
		var start runTask
		if err := conn.ReadJSON(&start); err != nil {
			return err
		}
		if start.Header.Action != "run-task" || start.Payload.Model != "paraformer-realtime-v2" ||
			start.Payload.Parameters.Format != "pcm" || start.Payload.Parameters.SampleRate != 16_000 ||
			len(start.Payload.Parameters.LanguageHints) != 1 || start.Payload.Parameters.LanguageHints[0] != "zh" ||
			!start.Payload.Parameters.SemanticPunctuationEnabled || !start.Payload.Parameters.Heartbeat {
			return fmt.Errorf("unexpected run-task payload: %+v", start)
		}
		time.Sleep(30 * time.Millisecond)
		if err := writeServerEvent(conn, start.Header.TaskID, "task-started", nil); err != nil {
			return err
		}
		messageType, audio, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.BinaryMessage || string(audio) != "\x01\x02\x03\x04" {
			return fmt.Errorf("unexpected audio frame")
		}
		if err := writeResult(conn, start.Header.TaskID, 10, nil, "", false, true, 0); err != nil {
			return err
		}
		endPartial := int64(200)
		if err := writeResult(conn, start.Header.TaskID, 10, &endPartial, "你好", false, false, 0); err != nil {
			return err
		}
		endRevision := int64(350)
		if err := writeResult(conn, start.Header.TaskID, 10, &endRevision, "你好世界", false, false, 0); err != nil {
			return err
		}
		endFinal := int64(400)
		if err := writeResult(conn, start.Header.TaskID, 10, &endFinal, "你好，世界。", true, false, 1); err != nil {
			return err
		}
		var finish finishTask
		if err := conn.ReadJSON(&finish); err != nil {
			return err
		}
		if finish.Header.Action != "finish-task" || finish.Header.TaskID != start.Header.TaskID {
			return fmt.Errorf("unexpected finish-task payload")
		}
		return writeServerEvent(conn, start.Header.TaskID, "task-finished", nil)
	}, serverErrors)
	defer closeServer()

	provider := mustProvider(t, endpoint, 8)
	startResult := make(chan struct {
		session biz.ASRSession
		err     error
	}, 1)
	go func() {
		session, err := provider.Start(context.Background(), testBusinessSession(biz.MeetingLanguageZhCN), testAudioSpec())
		startResult <- struct {
			session biz.ASRSession
			err     error
		}{session: session, err: err}
	}()
	select {
	case result := <-startResult:
		t.Fatalf("Start() returned before task-started: %v", result.err)
	case <-time.After(10 * time.Millisecond):
	}
	result := <-startResult
	if result.err != nil {
		t.Fatalf("Start() error = %v", result.err)
	}
	if err := result.session.PushAudio(context.Background(), biz.AudioChunk{
		SessionID: testBusinessSessionID, Sequence: 1, Data: []byte{1, 2, 3, 4},
	}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	events := make([]biz.TranscriptEvent, 0, 3)
	for len(events) < 3 {
		select {
		case event := <-result.session.Events():
			events = append(events, event)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for transcript events")
		}
	}
	if events[0].Type != biz.TranscriptEventTypePartial || events[0].Revision != 1 ||
		events[1].Revision != 2 || events[2].Type != biz.TranscriptEventTypeFinal || events[2].Revision != 3 {
		t.Fatalf("unexpected mapped events: %+v", events)
	}
	if events[0].Segment.ID != events[2].Segment.ID || events[2].Segment.Sequence != 1 ||
		events[2].ProviderUsageDuration != time.Second {
		t.Fatalf("unexpected stable segment mapping: %+v", events[2])
	}
	segments, err := result.session.Finish(context.Background())
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if len(segments) != 1 || segments[0].Content != "你好，世界。" {
		t.Fatalf("Finish() segments = %+v", segments)
	}
	assertServerError(t, serverErrors)
}

func TestProviderRuntimeDoesNotExpireWhileWaitingForRecognitionEvent(t *testing.T) {
	serverErrors := make(chan error, 1)
	endpoint, closeServer := fakeProviderServer(t, func(conn *websocket.Conn, _ *http.Request) error {
		var start runTask
		if err := conn.ReadJSON(&start); err != nil {
			return err
		}
		if err := writeServerEvent(conn, start.Header.TaskID, "task-started", nil); err != nil {
			return err
		}
		messageType, _, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.BinaryMessage {
			return fmt.Errorf("expected binary audio")
		}
		// This pause is longer than the startup read timeout. It represents a
		// healthy meeting interval during which the provider has no transcript.
		time.Sleep(80 * time.Millisecond)
		var finish finishTask
		if err := conn.ReadJSON(&finish); err != nil {
			return err
		}
		return writeServerEvent(conn, start.Header.TaskID, "task-finished", nil)
	}, serverErrors)
	defer closeServer()

	cfg := testConfig(endpoint, 8)
	cfg.ReadTimeout = 30 * time.Millisecond
	provider, err := NewProvider(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := provider.Start(context.Background(), testBusinessSession(biz.MeetingLanguageAuto), testAudioSpec())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PushAudio(context.Background(), biz.AudioChunk{
		SessionID: testBusinessSessionID, Sequence: 1, Data: []byte{1, 2, 3, 4},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Finish(context.Background()); err != nil {
		t.Fatalf("Finish() after quiet provider interval error = %v", err)
	}
	assertServerError(t, serverErrors)
}

func TestProviderStartTaskFailureDoesNotLeakSecret(t *testing.T) {
	serverErrors := make(chan error, 1)
	endpoint, closeServer := fakeProviderServer(t, func(conn *websocket.Conn, _ *http.Request) error {
		var start runTask
		if err := conn.ReadJSON(&start); err != nil {
			return err
		}
		return writeServerEvent(conn, start.Header.TaskID, "task-failed", map[string]any{
			"error_code": "AUTHENTICATION_FAILURE", "error_message": "test-secret-key is invalid",
		})
	}, serverErrors)
	defer closeServer()
	provider := mustProvider(t, endpoint, 4)
	_, err := provider.Start(context.Background(), testBusinessSession(biz.MeetingLanguageAuto), testAudioSpec())
	assertProviderErrorCode(t, err, ErrorCodeTaskFailed)
	if strings.Contains(err.Error(), "test-secret-key") || strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("provider error leaked secret or raw message: %v", err)
	}
	assertServerError(t, serverErrors)
}

func TestSessionLogsDoNotLeakProviderMessage(t *testing.T) {
	serverErrors := make(chan error, 1)
	endpoint, closeServer := fakeProviderServer(t, func(conn *websocket.Conn, _ *http.Request) error {
		var start runTask
		if err := conn.ReadJSON(&start); err != nil {
			return err
		}
		if err := writeServerEvent(conn, start.Header.TaskID, "task-started", nil); err != nil {
			return err
		}
		return writeServerEvent(conn, start.Header.TaskID, "task-failed", map[string]any{
			"error_code": "CLIENT_ERROR", "error_message": "test-secret-key must never be logged",
		})
	}, serverErrors)
	defer closeServer()
	var logs bytes.Buffer
	provider, err := NewProvider(testConfig(endpoint, 4), slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	session, err := provider.Start(context.Background(), testBusinessSession(biz.MeetingLanguageAuto), testAudioSpec())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case _, open := <-session.Events():
		if open {
			t.Fatal("task-failed unexpectedly emitted a transcript event")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task-failed termination")
	}
	_, err = session.Finish(context.Background())
	assertProviderErrorCode(t, err, ErrorCodeTaskFailed)
	if strings.Contains(logs.String(), "test-secret-key") || strings.Contains(logs.String(), "must never be logged") {
		t.Fatalf("session log leaked provider material: %s", logs.String())
	}
	assertServerError(t, serverErrors)
}

func TestProviderRejectsMalformedEvents(t *testing.T) {
	tests := []struct {
		name  string
		after func(*websocket.Conn, string) error
		code  ErrorCode
	}{
		{name: "invalid json", code: ErrorCodeProtocol, after: func(conn *websocket.Conn, _ string) error {
			return conn.WriteMessage(websocket.TextMessage, []byte("{"))
		}},
		{name: "unknown event", code: ErrorCodeProtocol, after: func(conn *websocket.Conn, taskID string) error {
			return writeServerEvent(conn, taskID, "mystery-event", nil)
		}},
		{name: "missing sentence", code: ErrorCodeProtocol, after: func(conn *websocket.Conn, taskID string) error {
			return writeServerEvent(conn, taskID, "result-generated", nil)
		}},
		{name: "task failed", code: ErrorCodeTaskFailed, after: func(conn *websocket.Conn, taskID string) error {
			return writeServerEvent(conn, taskID, "task-failed", map[string]any{
				"error_code": "REQUEST_TIMEOUT", "error_message": "provider closed the task",
			})
		}},
		{name: "connection interrupted", code: ErrorCodeConnectionLost, after: func(conn *websocket.Conn, _ string) error {
			return conn.Close()
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serverErrors := make(chan error, 1)
			endpoint, closeServer := fakeProviderServer(t, func(conn *websocket.Conn, _ *http.Request) error {
				var start runTask
				if err := conn.ReadJSON(&start); err != nil {
					return err
				}
				if err := writeServerEvent(conn, start.Header.TaskID, "task-started", nil); err != nil {
					return err
				}
				return test.after(conn, start.Header.TaskID)
			}, serverErrors)
			defer closeServer()
			provider := mustProvider(t, endpoint, 4)
			session, err := provider.Start(context.Background(), testBusinessSession(biz.MeetingLanguageAuto), testAudioSpec())
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			select {
			case _, ok := <-session.Events():
				if ok {
					t.Fatal("malformed provider event unexpectedly produced a transcript")
				}
			case <-time.After(time.Second):
				t.Fatal("provider reader did not terminate after malformed event")
			}
			_, err = session.Finish(context.Background())
			assertProviderErrorCode(t, err, test.code)
			assertServerError(t, serverErrors)
		})
	}
}

func TestProviderFinishTimeoutOnSlowEventConsumer(t *testing.T) {
	serverErrors := make(chan error, 1)
	endpoint, closeServer := fakeProviderServer(t, func(conn *websocket.Conn, _ *http.Request) error {
		var start runTask
		if err := conn.ReadJSON(&start); err != nil {
			return err
		}
		if err := writeServerEvent(conn, start.Header.TaskID, "task-started", nil); err != nil {
			return err
		}
		var finish finishTask
		if err := conn.ReadJSON(&finish); err != nil {
			return err
		}
		for i := int64(0); i < 3; i++ {
			end := i*100 + 100
			if err := writeResult(conn, start.Header.TaskID, i*100, &end, fmt.Sprintf("partial-%d", i), false, false, 0); err != nil {
				return err
			}
		}
		return writeServerEvent(conn, start.Header.TaskID, "task-finished", nil)
	}, serverErrors)
	defer closeServer()
	cfg := testConfig(endpoint, 1)
	cfg.FinishTimeout = 80 * time.Millisecond
	provider, err := NewProvider(cfg, nil)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	session, err := provider.Start(context.Background(), testBusinessSession(biz.MeetingLanguageAuto), testAudioSpec())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_, err = session.Finish(context.Background())
	assertProviderErrorCode(t, err, ErrorCodeTimeout)
	assertServerError(t, serverErrors)
}

func TestProviderCancelClosesSession(t *testing.T) {
	serverErrors := make(chan error, 1)
	endpoint, closeServer := fakeProviderServer(t, func(conn *websocket.Conn, _ *http.Request) error {
		var start runTask
		if err := conn.ReadJSON(&start); err != nil {
			return err
		}
		if err := writeServerEvent(conn, start.Header.TaskID, "task-started", nil); err != nil {
			return err
		}
		_, _, err := conn.ReadMessage()
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || errors.Is(err, context.Canceled) {
			return nil
		}
		return nil
	}, serverErrors)
	defer closeServer()
	provider := mustProvider(t, endpoint, 4)
	session, err := provider.Start(context.Background(), testBusinessSession(biz.MeetingLanguageEnUS), testAudioSpec())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := session.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if err := session.PushAudio(context.Background(), biz.AudioChunk{Data: []byte{1, 2}}); err == nil {
		t.Fatal("PushAudio() after cancel expected error")
	}
	assertServerError(t, serverErrors)
}

func TestProviderBoundsConcurrentSessions(t *testing.T) {
	serverErrors := make(chan error, 1)
	endpoint, closeServer := fakeProviderServer(t, func(conn *websocket.Conn, _ *http.Request) error {
		var start runTask
		if err := conn.ReadJSON(&start); err != nil {
			return err
		}
		if err := writeServerEvent(conn, start.Header.TaskID, "task-started", nil); err != nil {
			return err
		}
		_, _, _ = conn.ReadMessage()
		return nil
	}, serverErrors)
	defer closeServer()
	cfg := testConfig(endpoint, 4)
	cfg.MaxConcurrentSessions = 1
	provider, err := NewProvider(cfg, nil)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	first, err := provider.Start(context.Background(), testBusinessSession(biz.MeetingLanguageAuto), testAudioSpec())
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	secondCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err = provider.Start(secondCtx, &biz.TranscriptionSession{ID: uuid.NewString(), Language: biz.MeetingLanguageAuto}, testAudioSpec())
	assertProviderErrorCode(t, err, ErrorCodeTimeout)
	if err := first.Cancel(context.Background()); err != nil {
		t.Fatalf("first Cancel() error = %v", err)
	}
	assertServerError(t, serverErrors)
}

const testBusinessSessionID = "0b8b04cb-7fd8-4a8b-a278-ed4feea5c927"

func testBusinessSession(language biz.MeetingLanguage) *biz.TranscriptionSession {
	return &biz.TranscriptionSession{ID: testBusinessSessionID, Language: language}
}

func testAudioSpec() biz.AudioSpec {
	return biz.AudioSpec{
		Format: biz.AudioFormatPCMS16LE, MIMEType: "audio/pcm", SampleRate: 16_000,
		Channels: 1, ChunkDuration: 200 * time.Millisecond, MaxChunkBytes: 6_400,
	}
}

func testConfig(endpoint string, queueCapacity int32) Config {
	return Config{
		Endpoint: endpoint, APIKey: "test-secret-key", WorkspaceID: "test-workspace",
		Model: "paraformer-realtime-v2", ConnectTimeout: time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: time.Second, FinishTimeout: time.Second, SessionTimeout: time.Minute,
		MaxConcurrentSessions: 2, QueueCapacity: queueCapacity,
	}
}

func mustProvider(t *testing.T, endpoint string, queueCapacity int32) *Provider {
	t.Helper()
	provider, err := NewProvider(testConfig(endpoint, queueCapacity), nil)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	return provider
}

func fakeProviderServer(t *testing.T, handler func(*websocket.Conn, *http.Request) error, serverErrors chan<- error) (string, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := testUpgrader.Upgrade(response, request, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.Close()
		serverErrors <- handler(conn, request)
	}))
	return "ws" + strings.TrimPrefix(server.URL, "http"), server.Close
}

func writeServerEvent(conn *websocket.Conn, taskID, event string, extra map[string]any) error {
	header := map[string]any{"task_id": taskID, "event": event, "attributes": map[string]any{}}
	for key, value := range extra {
		header[key] = value
	}
	return conn.WriteJSON(map[string]any{"header": header, "payload": map[string]any{}})
}

func writeResult(conn *websocket.Conn, taskID string, begin int64, end *int64, content string, final, heartbeat bool, usageSeconds int64) error {
	payload := map[string]any{
		"output": map[string]any{"sentence": map[string]any{
			"begin_time": begin, "end_time": end, "text": content,
			"heartbeat": heartbeat, "sentence_end": final,
		}},
	}
	if usageSeconds > 0 {
		payload["usage"] = map[string]any{"duration": usageSeconds}
	}
	return conn.WriteJSON(map[string]any{
		"header":  map[string]any{"task_id": taskID, "event": "result-generated", "attributes": map[string]any{}},
		"payload": payload,
	})
}

func assertProviderErrorCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected provider error %s", code)
	}
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != code {
		t.Fatalf("provider error = %v, want code %s", err, code)
	}
}

func assertServerError(t *testing.T, serverErrors <-chan error) {
	t.Helper()
	select {
	case err := <-serverErrors:
		if err != nil {
			t.Fatalf("fake provider error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fake provider did not finish")
	}
}

func TestProtocolJSONUsesSnakeCaseAndUUIDTaskID(t *testing.T) {
	message := newRunTask(uuid.NewString(), testConfig("ws://127.0.0.1", 1), []string{"en"})
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	encoded := string(payload)
	if !strings.Contains(encoded, `"task_id"`) || !strings.Contains(encoded, `"language_hints"`) || strings.Contains(encoded, "test-secret-key") {
		t.Fatalf("unexpected protocol JSON: %s", encoded)
	}
}
