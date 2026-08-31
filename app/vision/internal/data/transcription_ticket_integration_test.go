package data

import (
	"context"
	stderrors "errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	platformredis "github.com/tiehu-ai/tiehu-fitness/internal/platform/redis"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestTranscriptionTicketRedisAtomicConsume(t *testing.T) {
	addr := os.Getenv("TEST_VISION_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_VISION_REDIS_ADDR is not set")
	}
	client, err := platformredis.Open(context.Background(), &conf.Redis{
		Addr: addr, Username: os.Getenv("TEST_VISION_REDIS_USERNAME"), Password: os.Getenv("TEST_VISION_REDIS_PASSWORD"),
		DialTimeout: durationpb.New(time.Second), ReadTimeout: durationpb.New(time.Second), WriteTimeout: durationpb.New(time.Second),
		PoolSize: 10, MinIdleConns: 1, KeyPrefix: "visiontest:",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})
	repo, err := NewTranscriptionTicketRepo(client)
	if err != nil {
		t.Fatal(err)
	}
	claims := validTicketClaims(time.Now().UTC().Add(time.Minute))
	ticket, err := repo.Issue(context.Background(), claims)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repo.RevokeSession(context.Background(), claims.SessionID); err != nil {
			t.Errorf("revoke ticket: %v", err)
		}
	})

	const consumers = 16
	var successes atomic.Int32
	var invalid atomic.Int32
	var wait sync.WaitGroup
	wait.Add(consumers)
	for range consumers {
		go func() {
			defer wait.Done()
			consumed, consumeErr := repo.Consume(context.Background(), ticket.Value)
			switch {
			case consumeErr == nil && consumed != nil:
				if consumed.SessionID != claims.SessionID || consumed.GrantedAudioSeconds != claims.GrantedAudioSeconds || consumed.Audio != claims.Audio {
					t.Errorf("consumed claims = %#v", consumed)
				}
				successes.Add(1)
			case stderrors.Is(consumeErr, biz.ErrTranscriptionTicketInvalid):
				invalid.Add(1)
			default:
				t.Errorf("Consume() error = %v", consumeErr)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 || invalid.Load() != consumers-1 {
		t.Fatalf("concurrent consume successes/invalid = %d/%d", successes.Load(), invalid.Load())
	}
	if _, err := repo.Consume(context.Background(), "not-a-ticket"); !stderrors.Is(err, biz.ErrTranscriptionTicketInvalid) {
		t.Fatalf("Consume(invalid) error = %v", err)
	}
}

func TestTranscriptionTicketRedisExpiredClaims(t *testing.T) {
	addr := os.Getenv("TEST_VISION_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_VISION_REDIS_ADDR is not set")
	}
	client, err := platformredis.Open(context.Background(), &conf.Redis{
		Addr: addr, Username: os.Getenv("TEST_VISION_REDIS_USERNAME"), Password: os.Getenv("TEST_VISION_REDIS_PASSWORD"),
		DialTimeout: durationpb.New(time.Second), ReadTimeout: durationpb.New(time.Second), WriteTimeout: durationpb.New(time.Second),
		PoolSize: 5, MinIdleConns: 1, KeyPrefix: "visiontest:",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	repo, err := NewTranscriptionTicketRepo(client)
	if err != nil {
		t.Fatal(err)
	}
	claims := validTicketClaims(time.Now().UTC().Add(100 * time.Millisecond))
	ticket, err := repo.Issue(context.Background(), claims)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := repo.Consume(context.Background(), ticket.Value); !stderrors.Is(err, biz.ErrTranscriptionTicketExpired) {
		t.Fatalf("Consume(expired claims) error = %v", err)
	}
}

func validTicketClaims(expiresAt time.Time) biz.TicketClaims {
	return biz.TicketClaims{
		Version: 1, SessionID: uuid.NewString(), MeetingID: uuid.NewString(), UserID: uuid.NewString(),
		GrantedAudioSeconds: 60,
		Audio: biz.AudioSpec{
			Format: biz.AudioFormatPCMS16LE, MIMEType: "audio/pcm", SampleRate: 16_000, Channels: 1,
			ChunkDuration: 200 * time.Millisecond, MaxChunkBytes: 6_400,
		},
		ExpiresAt: expiresAt,
	}
}
