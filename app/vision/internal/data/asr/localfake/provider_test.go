package localfake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
)

func TestProviderStreamsAndFinalizesSyntheticSegments(t *testing.T) {
	provider, err := NewProvider(Config{MaxConcurrentSessions: 1, QueueCapacity: 16}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	spec := fakeAudioSpec()
	runtime, err := provider.Start(context.Background(), &biz.TranscriptionSession{
		ID: sessionID, Language: biz.MeetingLanguageZhCN,
	}, spec)
	if err != nil {
		t.Fatal(err)
	}
	for sequence := int64(1); sequence <= chunksPerSegment+1; sequence++ {
		if err := runtime.PushAudio(context.Background(), biz.AudioChunk{
			SessionID: sessionID, Sequence: sequence, CapturedAt: time.Now(), Data: make([]byte, 6_400),
		}); err != nil {
			t.Fatalf("PushAudio(%d) error = %v", sequence, err)
		}
		<-runtime.Events()
	}
	finals, err := runtime.Finish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(finals) != 2 || finals[0].Sequence != 1 || finals[1].Sequence != 2 {
		t.Fatalf("Finish() finals = %#v", finals)
	}
	tail, ok := <-runtime.Events()
	if !ok || tail.Type != biz.TranscriptEventTypeFinal || tail.Segment.Sequence != 2 {
		t.Fatalf("tail event = %#v, open = %v", tail, ok)
	}
	if _, open := <-runtime.Events(); open {
		t.Fatal("Events() remained open after Finish")
	}
}

func TestProviderEnforcesCapacityAndCancellation(t *testing.T) {
	provider, err := NewProvider(Config{MaxConcurrentSessions: 1, QueueCapacity: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.Start(context.Background(), &biz.TranscriptionSession{ID: uuid.NewString(), Language: biz.MeetingLanguageAuto}, fakeAudioSpec())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.Start(ctx, &biz.TranscriptionSession{ID: uuid.NewString(), Language: biz.MeetingLanguageAuto}, fakeAudioSpec())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context canceled", err)
	}
	if err := first.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := provider.Start(context.Background(), &biz.TranscriptionSession{ID: uuid.NewString(), Language: biz.MeetingLanguageAuto}, fakeAudioSpec())
	if err != nil {
		t.Fatalf("Start() after release error = %v", err)
	}
	if err := second.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func fakeAudioSpec() biz.AudioSpec {
	return biz.AudioSpec{
		Format: biz.AudioFormatPCMS16LE, MIMEType: "audio/pcm", SampleRate: 16_000,
		Channels: 1, ChunkDuration: 200 * time.Millisecond, MaxChunkBytes: 6_400,
	}
}
