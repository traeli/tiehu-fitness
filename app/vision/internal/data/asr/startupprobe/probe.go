package startupprobe

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
)

const (
	pcmBitsPerSample = uint16(16)
	maxFixtureBytes  = 2 * 1024 * 1024
)

//go:embed assets/vision-startup-check.wav
var embeddedAudio []byte

// Result is the safe, non-user output of one provider startup probe.
type Result struct {
	Provider      biz.ASRProviderName
	AudioDuration time.Duration
	Elapsed       time.Duration
	Transcript    string
}

// Run verifies the complete real-time provider protocol with an embedded
// speech fixture before Vision accepts traffic. This probe is intentionally an
// adapter-level check: it must not create meetings or mutate repositories.
func Run(ctx context.Context, provider biz.ASRProvider, spec biz.AudioSpec, cfg *conf.ASRStartupProbe) (*Result, error) {
	if cfg == nil {
		return nil, fmt.Errorf("startup probe config is required")
	}
	if !cfg.GetEnabled() {
		return nil, nil
	}
	if ctx == nil || provider == nil {
		return nil, fmt.Errorf("startup probe context and provider are required")
	}
	if cfg.GetTimeout() == nil || cfg.GetTimeout().AsDuration() <= 0 {
		return nil, fmt.Errorf("startup probe timeout must be positive")
	}
	pcm, fixtureSpec, err := decodePCM16WAV(embeddedAudio)
	if err != nil {
		return nil, fmt.Errorf("decode embedded startup audio: %w", err)
	}
	if err := compatibleFixture(fixtureSpec, spec); err != nil {
		return nil, err
	}
	return run(ctx, provider, spec, cfg.GetTimeout().AsDuration(), pcm, func(waitCtx context.Context) error {
		timer := time.NewTimer(spec.ChunkDuration)
		defer timer.Stop()
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-timer.C:
			return nil
		}
	})
}

type fixtureAudioSpec struct {
	sampleRate    uint32
	channels      uint16
	bitsPerSample uint16
}

type collectedEvents struct {
	finals []biz.TranscriptSegment
	err    error
}

func run(
	ctx context.Context,
	provider biz.ASRProvider,
	spec biz.AudioSpec,
	timeout time.Duration,
	pcm []byte,
	wait func(context.Context) error,
) (*Result, error) {
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("startup probe audio spec: %w", err)
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return nil, fmt.Errorf("startup probe PCM is empty or incomplete")
	}
	if timeout <= 0 || wait == nil {
		return nil, fmt.Errorf("startup probe timeout and pacer are required")
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	sessionID := uuid.NewString()
	now := time.Now().UTC()
	session, err := provider.Start(probeCtx, &biz.TranscriptionSession{
		ID: sessionID, MeetingID: uuid.NewString(), UserID: uuid.NewString(), ReservationID: uuid.NewString(),
		Language: biz.MeetingLanguageZhCN, Status: biz.TranscriptionSessionStatusConnecting,
		Provider: provider.Name(), IdempotencyKey: "startup-probe-" + sessionID,
		GrantedAudioDuration: biz.GrantedAudioDuration(timeout), CreatedAt: now, UpdatedAt: now,
	}, spec)
	if err != nil {
		return nil, fmt.Errorf("start provider session: %w", err)
	}
	eventsDone := collect(session.Events())
	cancelProvider := func() {
		cancelCtx, cancelSession := context.WithTimeout(context.WithoutCancel(probeCtx), time.Second)
		defer cancelSession()
		_ = session.Cancel(cancelCtx)
	}
	chunkBytes, err := probeChunkBytes(spec)
	if err != nil {
		cancelProvider()
		return nil, err
	}
	sequence := int64(0)
	for offset := 0; offset < len(pcm); offset += chunkBytes {
		end := offset + chunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		sequence++
		if err := session.PushAudio(probeCtx, biz.AudioChunk{
			SessionID: sessionID, Sequence: sequence, Data: pcm[offset:end],
			CapturedAt: startedAt.Add(time.Duration(offset/chunkBytes) * spec.ChunkDuration),
		}); err != nil {
			cancelProvider()
			return nil, fmt.Errorf("send startup audio chunk %d: %w", sequence, err)
		}
		if end < len(pcm) {
			if err := wait(probeCtx); err != nil {
				cancelProvider()
				return nil, fmt.Errorf("pace startup audio: %w", err)
			}
		}
	}
	segments, err := session.Finish(probeCtx)
	if err != nil {
		cancelProvider()
		return nil, fmt.Errorf("finish provider session: %w", err)
	}
	collected, err := awaitEvents(probeCtx, eventsDone)
	if err != nil {
		return nil, err
	}
	if collected.err != nil {
		return nil, fmt.Errorf("collect provider events: %w", collected.err)
	}
	if len(segments) == 0 {
		segments = collected.finals
	}
	transcript, err := finalTranscript(segments)
	if err != nil {
		return nil, err
	}
	bytesPerSecond, err := spec.BytesPerSecond()
	if err != nil {
		return nil, err
	}
	return &Result{
		Provider: provider.Name(), AudioDuration: time.Duration(int64(len(pcm)) * int64(time.Second) / bytesPerSecond),
		Elapsed: time.Since(startedAt), Transcript: transcript,
	}, nil
}

func collect(events <-chan biz.TranscriptEvent) <-chan collectedEvents {
	done := make(chan collectedEvents, 1)
	go func() {
		result := collectedEvents{}
		defer func() {
			if recovered := recover(); recovered != nil {
				result.err = fmt.Errorf("startup probe event collector panic: %v", recovered)
			}
			done <- result
		}()
		for event := range events {
			if err := event.Validate(); err != nil {
				result.err = fmt.Errorf("invalid provider event: %w", err)
				continue
			}
			if event.Type == biz.TranscriptEventTypeFinal {
				result.finals = append(result.finals, event.Segment)
			}
		}
	}()
	return done
}

func awaitEvents(ctx context.Context, done <-chan collectedEvents) (collectedEvents, error) {
	select {
	case result := <-done:
		return result, nil
	case <-ctx.Done():
		return collectedEvents{}, fmt.Errorf("wait for provider events: %w", ctx.Err())
	}
}

func finalTranscript(segments []biz.TranscriptSegment) (string, error) {
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		if err := segment.Validate(); err != nil {
			return "", fmt.Errorf("invalid final startup transcript: %w", err)
		}
		if content := strings.TrimSpace(segment.Content); content != "" {
			parts = append(parts, content)
		}
	}
	if len(parts) == 0 {
		return "", errors.New("provider returned no final transcript for startup audio")
	}
	return strings.Join(parts, " "), nil
}

func probeChunkBytes(spec biz.AudioSpec) (int, error) {
	bytesPerSecond, err := spec.BytesPerSecond()
	if err != nil {
		return 0, err
	}
	chunkBytes := bytesPerSecond * int64(spec.ChunkDuration) / int64(time.Second)
	if chunkBytes <= 0 || chunkBytes > int64(spec.MaxChunkBytes) || chunkBytes%2 != 0 {
		return 0, fmt.Errorf("startup probe chunk size is invalid")
	}
	return int(chunkBytes), nil
}

func compatibleFixture(fixture fixtureAudioSpec, spec biz.AudioSpec) error {
	if fixture.sampleRate != uint32(spec.SampleRate) || fixture.channels != uint16(spec.Channels) || fixture.bitsPerSample != pcmBitsPerSample {
		return fmt.Errorf("embedded startup audio must be PCM S16LE %d Hz mono", spec.SampleRate)
	}
	return nil
}

func decodePCM16WAV(data []byte) ([]byte, fixtureAudioSpec, error) {
	if len(data) < 12 || len(data) > maxFixtureBytes || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fixtureAudioSpec{}, fmt.Errorf("invalid WAV container")
	}
	var spec fixtureAudioSpec
	var pcm []byte
	for offset := 12; offset+8 <= len(data); {
		chunkID := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		end := start + size
		if size < 0 || end < start || end > len(data) {
			return nil, fixtureAudioSpec{}, fmt.Errorf("invalid WAV chunk")
		}
		switch chunkID {
		case "fmt ":
			if size < 16 || binary.LittleEndian.Uint16(data[start:start+2]) != 1 {
				return nil, fixtureAudioSpec{}, fmt.Errorf("startup WAV must use PCM encoding")
			}
			spec.channels = binary.LittleEndian.Uint16(data[start+2 : start+4])
			spec.sampleRate = binary.LittleEndian.Uint32(data[start+4 : start+8])
			spec.bitsPerSample = binary.LittleEndian.Uint16(data[start+14 : start+16])
		case "data":
			pcm = append([]byte(nil), data[start:end]...)
		}
		offset = end
		if offset%2 != 0 {
			offset++
		}
	}
	if spec.sampleRate == 0 || spec.channels == 0 || len(pcm) == 0 {
		return nil, fixtureAudioSpec{}, fmt.Errorf("startup WAV is missing format or audio data")
	}
	if len(pcm)%2 != 0 {
		return nil, fixtureAudioSpec{}, fmt.Errorf("startup WAV contains an incomplete PCM sample")
	}
	return pcm, spec, nil
}
