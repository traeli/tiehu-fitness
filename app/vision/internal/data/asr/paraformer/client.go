package paraformer

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

const maxProviderEventBytes = int64(1 << 20)

var (
	errProviderProtocol = errors.New("invalid provider protocol")
	modelNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

type Config struct {
	Endpoint              string
	APIKey                string
	WorkspaceID           string
	Model                 string
	VocabularyID          string
	ConnectTimeout        time.Duration
	ReadTimeout           time.Duration
	WriteTimeout          time.Duration
	FinishTimeout         time.Duration
	SessionTimeout        time.Duration
	MaxConcurrentSessions int32
	QueueCapacity         int32
	SharedSlots           chan struct{}
}

type Provider struct {
	cfg    Config
	logger *slog.Logger
	dialer websocket.Dialer
	slots  chan struct{}
}

var _ biz.ASRProvider = (*Provider)(nil)

func NewProvider(cfg Config, logger *slog.Logger) (*Provider, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	slots := cfg.SharedSlots
	if slots == nil {
		slots = make(chan struct{}, int(cfg.MaxConcurrentSessions))
	}
	return &Provider{
		cfg: cfg, logger: logger,
		dialer: websocket.Dialer{
			HandshakeTimeout: cfg.ConnectTimeout,
			TLSClientConfig:  &tls.Config{MinVersion: tls.VersionTLS12},
		},
		slots: slots,
	}, nil
}

func validateConfig(cfg Config) error {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		(endpoint.Scheme != "wss" && endpoint.Scheme != "ws") {
		return fmt.Errorf("paraformer endpoint is invalid")
	}
	if strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.WorkspaceID) == "" {
		return fmt.Errorf("paraformer credentials are required")
	}
	if !modelNamePattern.MatchString(cfg.Model) {
		return fmt.Errorf("paraformer realtime model is invalid")
	}
	for name, duration := range map[string]time.Duration{
		"connect": cfg.ConnectTimeout, "read": cfg.ReadTimeout, "write": cfg.WriteTimeout,
		"finish": cfg.FinishTimeout, "session": cfg.SessionTimeout,
	} {
		if duration <= 0 {
			return fmt.Errorf("paraformer %s timeout must be positive", name)
		}
	}
	if cfg.MaxConcurrentSessions <= 0 || cfg.QueueCapacity <= 0 || cfg.QueueCapacity > 1_024 ||
		(cfg.SharedSlots != nil && cap(cfg.SharedSlots) == 0) {
		return fmt.Errorf("paraformer concurrency or queue capacity is invalid")
	}
	return nil
}

func (p *Provider) Name() biz.ASRProviderName {
	return biz.ASRProviderNameBailianParaformer
}

func (p *Provider) Start(ctx context.Context, session *biz.TranscriptionSession, spec biz.AudioSpec) (biz.ASRSession, error) {
	if ctx == nil || session == nil {
		return nil, providerError(ErrorCodeProtocol, fmt.Errorf("session and context are required"))
	}
	if _, err := uuid.Parse(session.ID); err != nil {
		return nil, providerError(ErrorCodeProtocol, fmt.Errorf("business session id is invalid"))
	}
	if err := spec.Validate(); err != nil {
		return nil, providerError(ErrorCodeProtocol, err)
	}
	hints, err := languageHints(session.Language)
	if err != nil {
		return nil, providerError(ErrorCodeProtocol, err)
	}
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, providerError(ErrorCodeCancelled, ctx.Err())
	}
	releaseSlot := func() { <-p.slots }
	released := false
	defer func() {
		if !released {
			releaseSlot()
		}
	}()

	dialCtx, cancelDial := context.WithTimeout(ctx, p.cfg.ConnectTimeout)
	defer cancelDial()
	headers := http.Header{
		"Authorization":         []string{"Bearer " + p.cfg.APIKey},
		"X-DashScope-WorkSpace": []string{p.cfg.WorkspaceID},
		"User-Agent":            []string{"tiehu-fitness-vision/1"},
	}
	conn, response, err := p.dialer.DialContext(dialCtx, p.cfg.Endpoint, headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, providerError(ErrorCodeHandshake, err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = conn.Close()
		}
	}()
	conn.SetReadLimit(maxProviderEventBytes)
	taskID := uuid.NewString()
	if err := writeJSON(conn, newRunTask(taskID, p.cfg, hints), p.cfg.WriteTimeout); err != nil {
		return nil, providerError(ErrorCodeHandshake, err)
	}
	started, err := readEvent(conn, p.cfg.ReadTimeout)
	if err != nil {
		return nil, providerError(readErrorCode(err), err)
	}
	if started.Header.TaskID != taskID {
		return nil, providerError(ErrorCodeProtocol, fmt.Errorf("task-started task id mismatch"))
	}
	switch started.Header.Event {
	case "task-started":
		// The provider is ready for binary audio.
	case "task-failed":
		return nil, taskFailure(started.Header.ErrorCode)
	default:
		return nil, providerError(ErrorCodeProtocol, fmt.Errorf("expected task-started event"))
	}

	sessionCtx, cancelSession := context.WithTimeout(ctx, p.cfg.SessionTimeout)
	providerSession := &Session{
		cfg: p.cfg, logger: p.logger, taskID: taskID, businessSessionID: session.ID,
		startedAt: time.Now().UTC(), audio: spec,
		conn: conn, ctx: sessionCtx, cancel: cancelSession,
		commands: make(chan writeCommand, int(p.cfg.QueueCapacity)),
		events:   make(chan biz.TranscriptEvent, int(p.cfg.QueueCapacity)),
		done:     make(chan struct{}), mapper: newEventMapper(taskID, session),
		releaseSlot: releaseSlot,
	}
	released = true
	closeOnError = false
	providerSession.goSafe("writer", providerSession.writerLoop)
	providerSession.goSafe("reader", providerSession.readerLoop)
	providerSession.goSafe("cancellation", providerSession.cancellationLoop)
	p.logger.Info("paraformer session started",
		"session_id", session.ID, "task_id", taskID, "model", p.cfg.Model)
	return providerSession, nil
}

type writeCommand struct {
	messageType int
	payload     []byte
	result      chan error
}

type Session struct {
	cfg               Config
	logger            *slog.Logger
	taskID            string
	businessSessionID string
	startedAt         time.Time
	audio             biz.AudioSpec
	conn              *websocket.Conn
	ctx               context.Context
	cancel            context.CancelFunc
	commands          chan writeCommand
	events            chan biz.TranscriptEvent
	done              chan struct{}
	mapper            *eventMapper
	releaseSlot       func()

	finishMu          sync.Mutex
	finishing         bool
	finalMu           sync.Mutex
	finals            []biz.TranscriptSegment
	termOnce          sync.Once
	termMu            sync.Mutex
	termErr           error
	audioMu           sync.Mutex
	lastAudioSequence int64
}

var _ biz.ASRSession = (*Session)(nil)

func (s *Session) Events() <-chan biz.TranscriptEvent {
	return s.events
}

func (s *Session) PushAudio(ctx context.Context, chunk biz.AudioChunk) error {
	if ctx == nil {
		return providerError(ErrorCodeCancelled, context.Canceled)
	}
	if err := s.terminalErrorIfDone(); err != nil {
		return err
	}
	if err := chunk.Validate(s.audio); err != nil || chunk.SessionID != s.businessSessionID {
		return providerError(ErrorCodeProtocol, fmt.Errorf("audio chunk is invalid"))
	}
	s.audioMu.Lock()
	defer s.audioMu.Unlock()
	if chunk.Sequence != s.lastAudioSequence+1 {
		return providerError(ErrorCodeProtocol, fmt.Errorf("audio chunk sequence is invalid"))
	}
	s.finishMu.Lock()
	finishing := s.finishing
	s.finishMu.Unlock()
	if finishing {
		return providerError(ErrorCodeProtocol, fmt.Errorf("audio is not accepted after finish"))
	}
	payload := append([]byte(nil), chunk.Data...)
	if err := s.send(ctx, websocket.BinaryMessage, payload); err != nil {
		return err
	}
	s.lastAudioSequence = chunk.Sequence
	return s.terminalErrorIfDone()
}

func (s *Session) Finish(ctx context.Context) ([]biz.TranscriptSegment, error) {
	if ctx == nil {
		return nil, providerError(ErrorCodeCancelled, context.Canceled)
	}
	finishCtx, cancel := context.WithTimeout(ctx, s.cfg.FinishTimeout)
	defer cancel()
	s.finishMu.Lock()
	firstFinish := !s.finishing
	s.finishing = true
	s.finishMu.Unlock()
	if firstFinish {
		payload, err := json.Marshal(newFinishTask(s.taskID))
		if err != nil {
			return nil, providerError(ErrorCodeInternal, err)
		}
		if err := s.send(finishCtx, websocket.TextMessage, payload); err != nil {
			s.terminate(err)
			return nil, err
		}
	}
	select {
	case <-s.done:
		if err := s.terminalError(); err != nil {
			return nil, err
		}
		s.finalMu.Lock()
		defer s.finalMu.Unlock()
		return append([]biz.TranscriptSegment(nil), s.finals...), nil
	case <-finishCtx.Done():
		err := providerError(ErrorCodeTimeout, finishCtx.Err())
		s.terminate(err)
		return nil, err
	}
}

func (s *Session) Cancel(ctx context.Context) error {
	select {
	case <-s.done:
		if err := s.terminalError(); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	default:
	}
	s.terminate(providerError(ErrorCodeCancelled, context.Canceled))
	if ctx == nil {
		return nil
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) send(ctx context.Context, messageType int, payload []byte) error {
	result := make(chan error, 1)
	command := writeCommand{messageType: messageType, payload: payload, result: result}
	select {
	case <-s.done:
		return s.terminalErrorOrConnectionLost()
	case <-ctx.Done():
		return providerError(ErrorCodeBackpressure, ctx.Err())
	case s.commands <- command:
	}
	select {
	case <-s.done:
		return s.terminalErrorOrConnectionLost()
	case <-ctx.Done():
		return providerError(ErrorCodeTimeout, ctx.Err())
	case err := <-result:
		return err
	}
}

func (s *Session) writerLoop() {
	for {
		select {
		case <-s.done:
			return
		case command := <-s.commands:
			if err := s.conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout)); err != nil {
				mapped := providerError(ErrorCodeConnectionLost, err)
				command.result <- mapped
				s.terminate(mapped)
				return
			}
			if err := s.conn.WriteMessage(command.messageType, command.payload); err != nil {
				mapped := providerError(readErrorCode(err), err)
				command.result <- mapped
				s.terminate(mapped)
				return
			}
			command.result <- nil
		}
	}
}

func (s *Session) readerLoop() {
	defer close(s.events)
	// ReadTimeout protects the initial task-started handshake only. During a
	// meeting Paraformer may legitimately emit no recognition event while the
	// audio is silent, so a per-event deadline would terminate healthy sessions.
	if err := s.conn.SetReadDeadline(time.Time{}); err != nil {
		s.terminate(providerError(ErrorCodeConnectionLost, err))
		return
	}
	for {
		event, err := readEvent(s.conn, 0)
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			s.terminate(providerError(readErrorCode(err), err))
			return
		}
		if event.Header.TaskID != s.taskID {
			s.terminate(providerError(ErrorCodeProtocol, fmt.Errorf("provider task id mismatch")))
			return
		}
		switch event.Header.Event {
		case "result-generated":
			mapped, err := s.mapper.mapResult(event)
			if err != nil {
				s.terminate(providerError(ErrorCodeProtocol, err))
				return
			}
			if mapped == nil {
				continue
			}
			if mapped.Type == biz.TranscriptEventTypeFinal {
				s.finalMu.Lock()
				s.finals = append(s.finals, mapped.Segment)
				s.finalMu.Unlock()
			}
			select {
			case s.events <- *mapped:
			case <-s.done:
				return
			}
		case "task-finished":
			s.finishMu.Lock()
			finishing := s.finishing
			s.finishMu.Unlock()
			if !finishing {
				s.terminate(providerError(ErrorCodeProtocol, fmt.Errorf("task-finished arrived before finish-task")))
				return
			}
			s.terminate(nil)
			return
		case "task-failed":
			s.terminate(taskFailure(event.Header.ErrorCode))
			return
		default:
			s.terminate(providerError(ErrorCodeProtocol, fmt.Errorf("unknown provider event")))
			return
		}
	}
}

func (s *Session) cancellationLoop() {
	select {
	case <-s.done:
		return
	case <-s.ctx.Done():
		s.terminate(providerError(ErrorCodeCancelled, s.ctx.Err()))
	}
}

func (s *Session) goSafe(operation string, fn func()) {
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("paraformer session panic",
					"operation", operation, "session_id", s.businessSessionID,
					"task_id", s.taskID, "panic", recovered, "stack", string(debug.Stack()))
				s.terminate(providerError(ErrorCodeInternal, fmt.Errorf("%s goroutine panic", operation)))
			}
		}()
		fn()
	}()
}

func (s *Session) terminate(err error) {
	s.termOnce.Do(func() {
		s.termMu.Lock()
		s.termErr = err
		s.termMu.Unlock()
		s.cancel()
		_ = s.conn.Close()
		s.releaseSlot()
		duration := time.Since(s.startedAt)
		var providerErr *Error
		switch {
		case err == nil:
			s.logger.Info("paraformer session ended",
				"session_id", s.businessSessionID, "task_id", s.taskID,
				"model", s.cfg.Model, "result", "succeeded", "duration", duration)
		case errors.As(err, &providerErr) && providerErr.Code == ErrorCodeCancelled:
			s.logger.Info("paraformer session ended",
				"session_id", s.businessSessionID, "task_id", s.taskID,
				"model", s.cfg.Model, "result", "cancelled", "duration", duration)
		default:
			code := ErrorCodeInternal
			providerCode := ""
			var cause error
			if errors.As(err, &providerErr) {
				code = providerErr.Code
				providerCode = providerErr.ProviderCode
				cause = providerErr.Unwrap()
			}
			// Provider errors deliberately omit raw response messages and
			// credentials. The retained cause contains only our protocol/network
			// diagnostic and is safe for the server-side structured log.
			s.logger.Error("paraformer session ended",
				"session_id", s.businessSessionID, "task_id", s.taskID,
				"model", s.cfg.Model, "result", "failed", "duration", duration,
				"error_code", code, "provider_code", providerCode, "cause", cause)
		}
		close(s.done)
	})
}

func (s *Session) terminalError() error {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	return s.termErr
}

func (s *Session) terminalErrorIfDone() error {
	select {
	case <-s.done:
		return s.terminalErrorOrConnectionLost()
	default:
		return nil
	}
}

func (s *Session) terminalErrorOrConnectionLost() error {
	if err := s.terminalError(); err != nil {
		return err
	}
	return providerError(ErrorCodeConnectionLost, io.EOF)
}

func writeJSON(conn *websocket.Conn, value any, timeout time.Duration) error {
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	return conn.WriteJSON(value)
}

func readEvent(conn *websocket.Conn, timeout time.Duration) (*serverEvent, error) {
	if timeout > 0 {
		if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return nil, err
		}
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if messageType != websocket.TextMessage {
		return nil, fmt.Errorf("%w: provider event must be text", errProviderProtocol)
	}
	var event serverEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("%w: decode provider event: %v", errProviderProtocol, err)
	}
	if event.Header.Event == "" || event.Header.TaskID == "" {
		return nil, fmt.Errorf("%w: provider event header is incomplete", errProviderProtocol)
	}
	return &event, nil
}

func readErrorCode(err error) ErrorCode {
	if errors.Is(err, errProviderProtocol) {
		return ErrorCodeProtocol
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrorCodeTimeout
	}
	return ErrorCodeConnectionLost
}
