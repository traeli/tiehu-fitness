package startupprobe

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

func TestEmbeddedStartupAudioIsCompatible(t *testing.T) {
	pcm, fixture, err := decodePCM16WAV(embeddedAudio)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) == 0 || fixture.sampleRate != 16_000 || fixture.channels != 1 || fixture.bitsPerSample != 16 {
		t.Fatalf("embedded fixture = %#v, bytes = %d", fixture, len(pcm))
	}
}

func TestRunRequiresFinalTranscript(t *testing.T) {
	spec := probeAudioSpec()
	pcm := make([]byte, 6_400)

	t.Run("success", func(t *testing.T) {
		provider := &fakeProvider{transcript: "铁虎会议助手启动检测"}
		result, err := run(context.Background(), provider, spec, time.Second, pcm, noWait)
		if err != nil {
			t.Fatal(err)
		}
		if result.Transcript != provider.transcript || result.Provider != biz.ASRProviderNameBailianParaformer {
			t.Fatalf("result = %#v", result)
		}
	})

	t.Run("empty transcript", func(t *testing.T) {
		provider := &fakeProvider{}
		if _, err := run(context.Background(), provider, spec, time.Second, pcm, noWait); err == nil || !strings.Contains(err.Error(), "no final transcript") {
			t.Fatalf("run() error = %v", err)
		}
	})

	t.Run("provider finish failure", func(t *testing.T) {
		provider := &fakeProvider{finishErr: biz.ErrASRProviderUnavailable}
		if _, err := run(context.Background(), provider, spec, time.Second, pcm, noWait); !errors.Is(err, biz.ErrASRProviderUnavailable) {
			t.Fatalf("run() error = %v", err)
		}
	})
}

func noWait(context.Context) error { return nil }

func probeAudioSpec() biz.AudioSpec {
	return biz.AudioSpec{
		Format: biz.AudioFormatPCMS16LE, MIMEType: "audio/pcm", SampleRate: 16_000,
		Channels: 1, ChunkDuration: 200 * time.Millisecond, MaxChunkBytes: 6_400,
	}
}

type fakeProvider struct {
	transcript string
	finishErr  error
}

func (*fakeProvider) Name() biz.ASRProviderName { return biz.ASRProviderNameBailianParaformer }

func (p *fakeProvider) Start(_ context.Context, session *biz.TranscriptionSession, _ biz.AudioSpec) (biz.ASRSession, error) {
	return &fakeSession{sessionID: session.ID, transcript: p.transcript, finishErr: p.finishErr, events: make(chan biz.TranscriptEvent, 1)}, nil
}

type fakeSession struct {
	sessionID  string
	transcript string
	finishErr  error
	events     chan biz.TranscriptEvent
	closeOnce  sync.Once
}

func (s *fakeSession) Events() <-chan biz.TranscriptEvent            { return s.events }
func (*fakeSession) PushAudio(context.Context, biz.AudioChunk) error { return nil }
func (s *fakeSession) Cancel(context.Context) error {
	s.closeEvents()
	return nil
}
func (s *fakeSession) Finish(context.Context) ([]biz.TranscriptSegment, error) {
	if s.finishErr != nil {
		s.closeEvents()
		return nil, s.finishErr
	}
	if s.transcript == "" {
		s.closeEvents()
		return nil, nil
	}
	segment := biz.TranscriptSegment{
		ID: uuid.NewString(), SessionID: s.sessionID, Sequence: 1,
		StartOffset: 0, EndOffset: 200 * time.Millisecond, Content: s.transcript,
		Language: biz.MeetingLanguageZhCN, Confidence: 1, CreatedAt: time.Now().UTC(),
	}
	s.events <- biz.TranscriptEvent{Type: biz.TranscriptEventTypeFinal, Segment: segment, Revision: 1}
	s.closeEvents()
	return []biz.TranscriptSegment{segment}, nil
}

func (s *fakeSession) closeEvents() {
	s.closeOnce.Do(func() { close(s.events) })
}
