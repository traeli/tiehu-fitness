package server

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/gorilla/websocket"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
)

const normalWebSocketClosure = 1000

type realtimeWebSocketConfig struct {
	handshakeTimeout time.Duration
	idleTimeout      time.Duration
	writeTimeout     time.Duration
	maxMessageBytes  int64
	queueCapacity    int
	allowedOrigins   []string
}

type RealtimeWebSocketHandler struct {
	service        *service.RealtimeTranscriptionService
	logger         *slog.Logger
	cfg            realtimeWebSocketConfig
	upgrader       websocket.Upgrader
	slots          chan struct{}
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	connectionsMu  sync.Mutex
	connections    map[*websocket.Conn]struct{}
	stopped        bool
}

func NewRealtimeWebSocketHandler(cfg *conf.RealtimeTranscription, svc *service.RealtimeTranscriptionService, logger *slog.Logger) (*RealtimeWebSocketHandler, error) {
	if cfg == nil || svc == nil || logger == nil {
		return nil, fmt.Errorf("realtime websocket config, service, and logger are required")
	}
	parsed, err := validateRealtimeWebSocketConfig(cfg)
	if err != nil {
		return nil, err
	}
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	handler := &RealtimeWebSocketHandler{
		service: svc, logger: logger, cfg: parsed,
		slots: make(chan struct{}, int(cfg.GetMaxConnections())), shutdownCtx: shutdownCtx, shutdownCancel: shutdownCancel,
		connections: make(map[*websocket.Conn]struct{}),
	}
	handler.upgrader = websocket.Upgrader{
		HandshakeTimeout: parsed.handshakeTimeout,
		ReadBufferSize:   4 * 1024,
		WriteBufferSize:  4 * 1024,
		CheckOrigin: func(request *http.Request) bool {
			return handler.originAllowed(request.Header.Get("Origin"))
		},
	}
	return handler, nil
}

func (h *RealtimeWebSocketHandler) Start(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return h.Stop(context.WithoutCancel(ctx))
	case <-h.shutdownCtx.Done():
	}
	return nil
}

func (h *RealtimeWebSocketHandler) Stop(context.Context) error {
	h.shutdownCancel()
	h.connectionsMu.Lock()
	h.stopped = true
	connections := make([]*websocket.Conn, 0, len(h.connections))
	for conn := range h.connections {
		connections = append(connections, conn)
	}
	h.connectionsMu.Unlock()
	for _, conn := range connections {
		// Close is safe concurrently with the single application writer and
		// unblocks any connection currently waiting in ReadMessage.
		_ = conn.Close()
	}
	return nil
}

type inboundWebSocketMessage struct {
	messageType int
	payload     []byte
	err         error
}

type finishResult struct {
	message *service.RealtimeSessionFinished
	err     error
}

func (h *RealtimeWebSocketHandler) Handle(httpContext kratoshttp.Context) (handlerErr error) {
	request := httpContext.Request()
	response := httpContext.Response()
	select {
	case <-h.shutdownCtx.Done():
		http.Error(response, "realtime service is shutting down", http.StatusServiceUnavailable)
		return nil
	default:
	}
	// Kratos' HTTP timeout is appropriate for RPCs but shorter than a meeting.
	// Preserve request values while giving this upgraded connection its own
	// bounded lifetime; socket failure and the handler lifecycle below provide
	// explicit client-disconnect and service-shutdown cancellation paths.
	connectionCtx, cancelConnection := context.WithTimeout(context.WithoutCancel(request.Context()), 24*time.Hour)
	stopShutdownLink := context.AfterFunc(h.shutdownCtx, cancelConnection)
	defer func() {
		stopShutdownLink()
		cancelConnection()
	}()
	if request.URL.RawQuery != "" {
		http.Error(response, "websocket query parameters are not allowed", http.StatusBadRequest)
		return nil
	}
	if !websocket.IsWebSocketUpgrade(request) {
		http.Error(response, "websocket upgrade required", http.StatusUpgradeRequired)
		return nil
	}
	if !h.originAllowed(request.Header.Get("Origin")) {
		http.Error(response, "websocket origin is not allowed", http.StatusForbidden)
		return nil
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		http.Error(response, "realtime connection capacity reached", http.StatusServiceUnavailable)
		return nil
	}

	conn, err := h.upgrader.Upgrade(response, request, nil)
	if err != nil {
		return nil
	}
	if !h.trackConnection(conn) {
		_ = conn.Close()
		return nil
	}
	defer h.untrackConnection(conn)
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && !stderrors.Is(closeErr, io.ErrClosedPipe) {
			h.logger.Debug("close realtime websocket", "error", closeErr)
		}
	}()
	var realtimeSession *service.RealtimeSession
	defer func() {
		if recovered := recover(); recovered != nil {
			h.logger.Error("realtime websocket panic", "panic", recovered, "stack", string(debug.Stack()))
			_ = h.writeJSON(conn, service.RealtimeErrorFrom(stderrors.New("realtime websocket panic"), lastACK(realtimeSession)))
			if realtimeSession != nil {
				cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(connectionCtx), h.cfg.writeTimeout)
				defer cancel()
				if cancelErr := realtimeSession.Cancel(cancelCtx); cancelErr != nil {
					h.logger.Error("cancel transcription after panic", "session_id", realtimeSession.SessionID(), "error", cancelErr)
				}
			}
			handlerErr = nil
		}
	}()

	conn.SetReadLimit(h.cfg.maxMessageBytes)
	if err := conn.SetReadDeadline(time.Now().Add(h.cfg.handshakeTimeout)); err != nil {
		return nil
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		return nil
	}
	if messageType != websocket.TextMessage {
		_ = h.writeJSON(conn, service.RealtimeErrorFrom(service.RealtimeProtocolViolation("first message must be text"), 0))
		return nil
	}
	realtimeSession, ready, err := h.service.Start(connectionCtx, payload)
	if err != nil {
		_ = h.writeJSON(conn, service.RealtimeErrorFrom(err, 0))
		return nil
	}
	if err := h.writeJSON(conn, ready); err != nil {
		h.cancelDisconnected(connectionCtx, realtimeSession)
		return nil
	}
	events, err := realtimeSession.Events()
	if err != nil {
		_ = h.writeJSON(conn, service.RealtimeErrorFrom(err, realtimeSession.LastACKSequence()))
		h.cancelDisconnected(connectionCtx, realtimeSession)
		return nil
	}
	if err := conn.SetReadDeadline(time.Now().Add(h.cfg.idleTimeout)); err != nil {
		h.cancelDisconnected(connectionCtx, realtimeSession)
		return nil
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(h.cfg.idleTimeout))
	})
	inbound := make(chan inboundWebSocketMessage, h.cfg.queueCapacity)
	readCtx, stopRead := context.WithCancel(connectionCtx)
	defer stopRead()
	go h.readLoop(readCtx, conn, inbound)

	var finalSequence int64
	var finishing bool
	var quotaExhausted bool
	var finishDone <-chan finishResult
	for {
		select {
		case <-connectionCtx.Done():
			h.cancelDisconnected(connectionCtx, realtimeSession)
			return nil
		case incoming := <-inbound:
			if incoming.err != nil {
				h.cancelDisconnected(connectionCtx, realtimeSession)
				return nil
			}
			if finishing {
				_ = h.writeJSON(conn, service.RealtimeErrorFrom(service.RealtimeProtocolViolation("messages are not accepted after finish"), realtimeSession.LastACKSequence()))
				continue
			}
			switch incoming.messageType {
			case websocket.TextMessage:
				control, parseErr := realtimeSession.ParseControl(incoming.payload)
				if parseErr != nil {
					_ = h.writeJSON(conn, service.RealtimeErrorFrom(parseErr, realtimeSession.LastACKSequence()))
					h.cancelDisconnected(connectionCtx, realtimeSession)
					return nil
				}
				switch control.Type {
				case service.RealtimeClientMessageTypePing:
					if err := h.writeJSON(conn, realtimeSession.Pong(control.SentAt)); err != nil {
						h.cancelDisconnected(connectionCtx, realtimeSession)
						return nil
					}
				case service.RealtimeClientMessageTypeFinish:
					finishing = true
					finishDone = h.startFinish(connectionCtx, realtimeSession, service.RealtimeFinishReasonClientFinished, finalSequence)
				}
			case websocket.BinaryMessage:
				ack, limitReached, pushErr := realtimeSession.PushAudio(connectionCtx, incoming.payload)
				if pushErr != nil {
					_ = h.writeJSON(conn, service.RealtimeErrorFrom(pushErr, realtimeSession.LastACKSequence()))
					h.cancelDisconnected(connectionCtx, realtimeSession)
					return nil
				}
				if ack != nil {
					if err := h.writeJSON(conn, ack); err != nil {
						h.cancelDisconnected(connectionCtx, realtimeSession)
						return nil
					}
				}
				if limitReached {
					finishing, quotaExhausted = true, true
					finishDone = h.startFinish(connectionCtx, realtimeSession, service.RealtimeFinishReasonQuotaExhausted, finalSequence)
				}
			default:
				_ = h.writeJSON(conn, service.RealtimeErrorFrom(service.RealtimeProtocolViolation("websocket message type is unsupported"), realtimeSession.LastACKSequence()))
				h.cancelDisconnected(connectionCtx, realtimeSession)
				return nil
			}
		case event, ok := <-events:
			if !ok {
				events = nil
				if !finishing {
					finishing = true
					finishDone = h.startFinish(connectionCtx, realtimeSession, service.RealtimeFinishReasonCancelled, finalSequence)
				}
				continue
			}
			segment, mapErr := realtimeSession.Transcript(event)
			if mapErr != nil {
				_ = h.writeJSON(conn, service.RealtimeErrorFrom(mapErr, realtimeSession.LastACKSequence()))
				h.cancelDisconnected(connectionCtx, realtimeSession)
				return nil
			}
			if segment.IsFinal && segment.SequenceNo > finalSequence {
				finalSequence = segment.SequenceNo
			}
			if err := h.writeJSON(conn, segment); err != nil {
				h.cancelDisconnected(connectionCtx, realtimeSession)
				return nil
			}
		case result := <-finishDone:
			if result.err != nil {
				_ = h.writeJSON(conn, service.RealtimeErrorFrom(result.err, realtimeSession.LastACKSequence()))
				return nil
			}
			// Finish was launched before the provider's last events arrived. The
			// provider closes Events before Finish returns, so drain the bounded
			// channel here and preserve every tail segment before completion.
			for events != nil {
				select {
				case event, ok := <-events:
					if !ok {
						events = nil
						continue
					}
					segment, mapErr := realtimeSession.Transcript(event)
					if mapErr != nil {
						_ = h.writeJSON(conn, service.RealtimeErrorFrom(mapErr, realtimeSession.LastACKSequence()))
						return nil
					}
					if segment.IsFinal && segment.SequenceNo > finalSequence {
						finalSequence = segment.SequenceNo
					}
					if err := h.writeJSON(conn, segment); err != nil {
						return nil
					}
				default:
					events = nil
				}
			}
			result.message.FinalSegmentSequenceNo = finalSequence
			if quotaExhausted {
				if err := h.writeJSON(conn, service.RealtimeQuotaExceeded(result.message.LastACKSequenceNo)); err != nil {
					return nil
				}
			}
			if err := h.writeJSON(conn, result.message); err != nil {
				return nil
			}
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(normalWebSocketClosure, "transcription finished"), time.Now().Add(h.cfg.writeTimeout))
			return nil
		}
	}
}

func (h *RealtimeWebSocketHandler) trackConnection(conn *websocket.Conn) bool {
	h.connectionsMu.Lock()
	defer h.connectionsMu.Unlock()
	if h.stopped {
		return false
	}
	h.connections[conn] = struct{}{}
	return true
}

func (h *RealtimeWebSocketHandler) untrackConnection(conn *websocket.Conn) {
	h.connectionsMu.Lock()
	delete(h.connections, conn)
	h.connectionsMu.Unlock()
}

func (h *RealtimeWebSocketHandler) readLoop(ctx context.Context, conn *websocket.Conn, output chan<- inboundWebSocketMessage) {
	defer func() {
		if recovered := recover(); recovered != nil {
			h.logger.Error("realtime websocket reader panic", "panic", recovered, "stack", string(debug.Stack()))
			select {
			case output <- inboundWebSocketMessage{err: fmt.Errorf("realtime reader panic")}:
			case <-ctx.Done():
			}
		}
	}()
	for {
		messageType, payload, err := conn.ReadMessage()
		if err == nil {
			err = conn.SetReadDeadline(time.Now().Add(h.cfg.idleTimeout))
		}
		message := inboundWebSocketMessage{messageType: messageType, payload: payload, err: err}
		select {
		case output <- message:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func (h *RealtimeWebSocketHandler) startFinish(ctx context.Context, session *service.RealtimeSession, reason service.RealtimeFinishReason, finalSequence int64) <-chan finishResult {
	result := make(chan finishResult, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				h.logger.Error("realtime finish panic", "session_id", session.SessionID(), "panic", recovered, "stack", string(debug.Stack()))
				result <- finishResult{err: fmt.Errorf("realtime finish panic")}
			}
		}()
		message, err := session.Finish(ctx, reason, finalSequence)
		result <- finishResult{message: message, err: err}
	}()
	return result
}

func (h *RealtimeWebSocketHandler) cancelDisconnected(parent context.Context, session *service.RealtimeSession) {
	if session == nil {
		return
	}
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), h.cfg.writeTimeout)
	defer cancel()
	if err := session.Cancel(cancelCtx); err != nil {
		h.logger.Error("cancel disconnected transcription", "session_id", session.SessionID(), "error", err)
	}
}

func (h *RealtimeWebSocketHandler) writeJSON(conn *websocket.Conn, message any) error {
	if err := conn.SetWriteDeadline(time.Now().Add(h.cfg.writeTimeout)); err != nil {
		return err
	}
	return conn.WriteJSON(message)
}

func (h *RealtimeWebSocketHandler) originAllowed(origin string) bool {
	for _, allowed := range h.cfg.allowedOrigins {
		if origin == allowed {
			return true
		}
		if allowed == "utools://*" {
			parsed, err := url.Parse(origin)
			if err == nil && parsed.Scheme == "utools" && parsed.Host != "" && parsed.Path == "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
				return true
			}
			continue
		}
		if strings.HasSuffix(allowed, ":*") {
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Path != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() == "" {
				continue
			}
			if _, err := strconv.ParseUint(parsed.Port(), 10, 16); err != nil {
				continue
			}
			base, err := url.Parse(strings.TrimSuffix(allowed, ":*"))
			if err == nil && parsed.Scheme == base.Scheme && parsed.Hostname() == base.Hostname() {
				return true
			}
		}
	}
	return false
}

func validateRealtimeWebSocketConfig(cfg *conf.RealtimeTranscription) (realtimeWebSocketConfig, error) {
	if cfg.GetHandshakeTimeout() == nil || cfg.GetIdleTimeout() == nil || cfg.GetWriteTimeout() == nil {
		return realtimeWebSocketConfig{}, fmt.Errorf("realtime websocket timeouts are required")
	}
	handshake := cfg.GetHandshakeTimeout().AsDuration()
	idle := cfg.GetIdleTimeout().AsDuration()
	write := cfg.GetWriteTimeout().AsDuration()
	if handshake <= 0 || handshake > 30*time.Second || idle <= 0 || idle > 5*time.Minute || write <= 0 || write > 30*time.Second {
		return realtimeWebSocketConfig{}, fmt.Errorf("realtime websocket timeouts are out of range")
	}
	if cfg.GetMaxMessageBytes() < 1024 || cfg.GetMaxMessageBytes() > 1<<20 {
		return realtimeWebSocketConfig{}, fmt.Errorf("realtime websocket max_message_bytes must be between 1024 and 1048576")
	}
	if cfg.GetMaxQueueChunks() <= 0 || cfg.GetMaxQueueChunks() > 1_024 || cfg.GetMaxConnections() <= 0 || cfg.GetMaxConnections() > 10_000 {
		return realtimeWebSocketConfig{}, fmt.Errorf("realtime websocket queue or connection limit is invalid")
	}
	allowed := cfg.GetAllowedOrigins()
	if len(allowed) == 0 || len(allowed) > 32 {
		return realtimeWebSocketConfig{}, fmt.Errorf("realtime websocket allowed_origins must contain between 1 and 32 entries")
	}
	for _, origin := range allowed {
		if err := validateOriginRule(origin); err != nil {
			return realtimeWebSocketConfig{}, err
		}
	}
	return realtimeWebSocketConfig{
		handshakeTimeout: handshake, idleTimeout: idle, writeTimeout: write,
		maxMessageBytes: cfg.GetMaxMessageBytes(), queueCapacity: int(cfg.GetMaxQueueChunks()),
		allowedOrigins: append([]string(nil), allowed...),
	}, nil
}

func validateOriginRule(rule string) error {
	if rule == "null" || rule == "utools://*" {
		return nil
	}
	if rule == "*" || strings.TrimSpace(rule) != rule {
		return fmt.Errorf("realtime websocket origin rule %q is invalid", rule)
	}
	value := rule
	if strings.HasSuffix(value, ":*") {
		value = strings.TrimSuffix(value, ":*")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return fmt.Errorf("realtime websocket origin rule %q is invalid", rule)
	}
	return nil
}

func lastACK(session *service.RealtimeSession) int64 {
	if session == nil {
		return 0
	}
	return session.LastACKSequence()
}
