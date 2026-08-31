// Package localfake provides an explicitly enabled development ASR strategy.
// It exercises the real vision WebSocket and persistence paths without making
// a provider network call; it never attempts to recognize the audio content.
package localfake

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

const chunksPerSegment = int64(10)

type Config struct {
	MaxConcurrentSessions int32
	QueueCapacity         int32
}

type Provider struct {
	cfg    Config
	logger *slog.Logger
	slots  chan struct{}
}

var _ biz.ASRProvider = (*Provider)(nil)

func NewProvider(cfg Config, logger *slog.Logger) (*Provider, error) {
	if cfg.MaxConcurrentSessions <= 0 || cfg.MaxConcurrentSessions > 10_000 {
		return nil, fmt.Errorf("local fake ASR max concurrent sessions must be between 1 and 10000")
	}
	if cfg.QueueCapacity <= 0 || cfg.QueueCapacity > 1_024 {
		return nil, fmt.Errorf("local fake ASR queue capacity must be between 1 and 1024")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Provider{cfg: cfg, logger: logger, slots: make(chan struct{}, int(cfg.MaxConcurrentSessions))}, nil
}

func (*Provider) Name() biz.ASRProviderName { return biz.ASRProviderNameLocalFake }

func (p *Provider) Start(ctx context.Context, session *biz.TranscriptionSession, spec biz.AudioSpec) (biz.ASRSession, error) {
	if ctx == nil || session == nil {
		return nil, fmt.Errorf("local fake ASR session and context are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(session.ID); err != nil {
		return nil, fmt.Errorf("local fake ASR session id is invalid: %w", err)
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	bytesPerSecond, err := spec.BytesPerSecond()
	if err != nil {
		return nil, err
	}
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := sync.OnceFunc(func() { <-p.slots })
	result := &Session{
		sessionID:      session.ID,
		language:       session.Language,
		spec:           spec,
		bytesPerSecond: bytesPerSecond,
		events:         make(chan biz.TranscriptEvent, int(p.cfg.QueueCapacity)),
		release:        release,
	}
	p.logger.Info("local fake ASR session started", "session_id", session.ID)
	return result, nil
}

type Session struct {
	mu             sync.Mutex
	sessionID      string
	language       biz.MeetingLanguage
	spec           biz.AudioSpec
	bytesPerSecond int64
	events         chan biz.TranscriptEvent
	release        func()
	closed         bool
	lastAudio      int64
	totalBytes     int64
	segmentID      string
	segmentSeq     int64
	revision       int32
	segmentFrom    time.Duration
	finals         []biz.TranscriptSegment
}

var _ biz.ASRSession = (*Session)(nil)

func (s *Session) Events() <-chan biz.TranscriptEvent { return s.events }

func (s *Session) PushAudio(ctx context.Context, chunk biz.AudioChunk) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := chunk.Validate(s.spec); err != nil || chunk.SessionID != s.sessionID {
		return fmt.Errorf("local fake ASR audio chunk is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return biz.ErrASRProviderUnavailable
	}
	if chunk.Sequence != s.lastAudio+1 {
		return biz.ErrASRProviderRejected
	}
	if s.segmentID == "" {
		s.segmentID = uuid.NewString()
		s.segmentSeq++
		s.revision = 0
		s.segmentFrom = s.audioDuration(s.totalBytes)
	}
	nextBytes := s.totalBytes + int64(len(chunk.Data))
	nextRevision := s.revision + 1
	isFinal := chunk.Sequence%chunksPerSegment == 0
	segment := biz.TranscriptSegment{
		ID: s.segmentID, SessionID: s.sessionID, Sequence: s.segmentSeq,
		StartOffset: s.segmentFrom, EndOffset: s.audioDuration(nextBytes),
		Content:  fmt.Sprintf("[本地模拟] 已接收第 %d 段音频", s.segmentSeq),
		Language: s.language, Confidence: 1, CreatedAt: time.Now().UTC(),
	}
	eventType := biz.TranscriptEventTypePartial
	if isFinal {
		eventType = biz.TranscriptEventTypeFinal
	}
	event := biz.TranscriptEvent{
		Type: eventType, Segment: segment, Revision: nextRevision,
		ProviderUsageDuration: segment.EndOffset,
	}
	select {
	case s.events <- event:
	case <-ctx.Done():
		return ctx.Err()
	default:
		return biz.ErrASRBackpressure
	}
	s.lastAudio = chunk.Sequence
	s.totalBytes = nextBytes
	s.revision = nextRevision
	if isFinal {
		s.finals = append(s.finals, segment)
		s.segmentID = ""
	}
	return nil
}

func (s *Session) Finish(ctx context.Context) ([]biz.TranscriptSegment, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return append([]biz.TranscriptSegment(nil), s.finals...), nil
	}
	if s.segmentID != "" {
		segment := biz.TranscriptSegment{
			ID: s.segmentID, SessionID: s.sessionID, Sequence: s.segmentSeq,
			StartOffset: s.segmentFrom, EndOffset: s.audioDuration(s.totalBytes),
			Content:  fmt.Sprintf("[本地模拟] 已接收第 %d 段音频", s.segmentSeq),
			Language: s.language, Confidence: 1, CreatedAt: time.Now().UTC(),
		}
		event := biz.TranscriptEvent{
			Type: biz.TranscriptEventTypeFinal, Segment: segment, Revision: s.revision + 1,
			ProviderUsageDuration: segment.EndOffset,
		}
		select {
		case s.events <- event:
			s.finals = append(s.finals, segment)
		case <-ctx.Done():
			s.closeLocked()
			return nil, ctx.Err()
		}
	}
	s.closeLocked()
	return append([]biz.TranscriptSegment(nil), s.finals...), nil
}

func (s *Session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	s.closeLocked()
	s.mu.Unlock()
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (s *Session) audioDuration(bytes int64) time.Duration {
	return time.Duration(bytes * int64(time.Second) / s.bytesPerSecond)
}

func (s *Session) closeLocked() {
	if s.closed {
		return
	}
	s.closed = true
	close(s.events)
	s.release()
}
