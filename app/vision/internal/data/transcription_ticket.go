package data

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	platformredis "github.com/tiehu-ai/tiehu-fitness/internal/platform/redis"
)

const transcriptionTicketBytes = 32
const transcriptionTicketExpiryMarkerRetention = 5 * time.Minute

type TranscriptionTicketRepo struct {
	client *platformredis.Client
}

var _ biz.TranscriptionTicketRepo = (*TranscriptionTicketRepo)(nil)

func NewTranscriptionTicketRepo(client *platformredis.Client) (*TranscriptionTicketRepo, error) {
	if client == nil || client.Commands() == nil {
		return nil, fmt.Errorf("transcription ticket redis client is required")
	}
	return &TranscriptionTicketRepo{client: client}, nil
}

func (r *TranscriptionTicketRepo) Issue(ctx context.Context, claims biz.TicketClaims) (*biz.TranscriptionTicket, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := claims.Validate(time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("validate transcription ticket claims: %w", err)
	}
	ttl := time.Until(claims.ExpiresAt)
	if ttl <= 0 || ttl > 5*time.Minute {
		return nil, fmt.Errorf("transcription ticket expiry is invalid")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("encode transcription ticket claims: %w", err)
	}
	mappingKey, err := r.client.Key("transcription-session-ticket:" + claims.SessionID)
	if err != nil {
		return nil, err
	}
	if oldHash, getErr := r.client.Commands().Get(ctx, mappingKey).Result(); getErr == nil {
		oldKey, keyErr := r.client.Key("transcription-ticket:" + oldHash)
		if keyErr != nil {
			return nil, keyErr
		}
		oldExpiryKey, keyErr := r.client.Key("transcription-ticket-expiry:" + oldHash)
		if keyErr != nil {
			return nil, keyErr
		}
		if err := r.client.Commands().Del(ctx, oldKey, oldExpiryKey, mappingKey).Err(); err != nil {
			return nil, fmt.Errorf("replace transcription ticket: %w", err)
		}
	} else if getErr != redis.Nil {
		return nil, fmt.Errorf("read existing transcription ticket: %w", getErr)
	}

	for attempt := 0; attempt < 3; attempt++ {
		raw := make([]byte, transcriptionTicketBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("generate transcription ticket: %w", err)
		}
		value := base64.RawURLEncoding.EncodeToString(raw)
		digest := sha256.Sum256([]byte(value))
		hash := hex.EncodeToString(digest[:])
		ticketKey, err := r.client.Key("transcription-ticket:" + hash)
		if err != nil {
			return nil, err
		}
		created, err := r.client.Commands().SetNX(ctx, ticketKey, payload, ttl).Result()
		if err != nil {
			return nil, fmt.Errorf("store transcription ticket: %w", err)
		}
		if !created {
			continue
		}
		expiryKey, err := r.client.Key("transcription-ticket-expiry:" + hash)
		if err != nil {
			return nil, r.rollbackIssuedTicket(ctx, err, ticketKey)
		}
		if err := r.client.Commands().Set(ctx, expiryKey, strconv.FormatInt(claims.ExpiresAt.UnixMilli(), 10), ttl+transcriptionTicketExpiryMarkerRetention).Err(); err != nil {
			return nil, r.rollbackIssuedTicket(ctx, fmt.Errorf("store transcription ticket expiry marker: %w", err), ticketKey)
		}
		if err := r.client.Commands().Set(ctx, mappingKey, hash, ttl).Err(); err != nil {
			return nil, r.rollbackIssuedTicket(ctx, fmt.Errorf("index transcription ticket: %w", err), ticketKey, expiryKey)
		}
		return &biz.TranscriptionTicket{Value: value, ExpiresAt: claims.ExpiresAt}, nil
	}
	return nil, fmt.Errorf("generate unique transcription ticket")
}

func (r *TranscriptionTicketRepo) rollbackIssuedTicket(ctx context.Context, cause error, keys ...string) error {
	if err := r.client.Commands().Del(ctx, keys...).Err(); err != nil {
		return stderrors.Join(cause, fmt.Errorf("rollback issued transcription ticket: %w", err))
	}
	return cause
}

func (r *TranscriptionTicketRepo) Consume(ctx context.Context, rawTicket string) (*biz.TicketClaims, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if strings.TrimSpace(rawTicket) != rawTicket || len(rawTicket) != base64.RawURLEncoding.EncodedLen(transcriptionTicketBytes) {
		return nil, biz.ErrTranscriptionTicketInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(rawTicket)
	if err != nil || len(decoded) != transcriptionTicketBytes {
		return nil, biz.ErrTranscriptionTicketInvalid
	}
	digest := sha256.Sum256([]byte(rawTicket))
	hash := hex.EncodeToString(digest[:])
	ticketKey, err := r.client.Key("transcription-ticket:" + hash)
	if err != nil {
		return nil, err
	}
	expiryKey, err := r.client.Key("transcription-ticket-expiry:" + hash)
	if err != nil {
		return nil, err
	}
	payload, err := r.client.Commands().GetDel(ctx, ticketKey).Bytes()
	if err == redis.Nil {
		expiresRaw, expiryErr := r.client.Commands().GetDel(ctx, expiryKey).Result()
		if expiryErr != nil && expiryErr != redis.Nil {
			return nil, fmt.Errorf("read transcription ticket expiry marker: %w", expiryErr)
		}
		if expiryErr == nil {
			expiresMillis, parseErr := strconv.ParseInt(expiresRaw, 10, 64)
			if parseErr != nil {
				return nil, fmt.Errorf("parse transcription ticket expiry marker: %w", parseErr)
			}
			if expiresMillis <= time.Now().UTC().UnixMilli() {
				return nil, biz.ErrTranscriptionTicketExpired
			}
		}
		return nil, biz.ErrTranscriptionTicketInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("consume transcription ticket: %w", err)
	}
	if err := r.client.Commands().Del(ctx, expiryKey).Err(); err != nil {
		return nil, fmt.Errorf("remove transcription ticket expiry marker: %w", err)
	}
	var claims biz.TicketClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("decode transcription ticket claims: %w", err)
	}
	mappingKey, err := r.client.Key("transcription-session-ticket:" + claims.SessionID)
	if err != nil {
		return nil, err
	}
	// A compare-and-delete avoids removing a newer ticket issued for the same
	// session while this consumed ticket was in flight.
	const removeMatchingIndex = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) end return 0`
	if err := r.client.Commands().Eval(ctx, removeMatchingIndex, []string{mappingKey}, hash).Err(); err != nil {
		return nil, fmt.Errorf("remove consumed transcription ticket index: %w", err)
	}
	if err := claims.Validate(time.Now().UTC()); err != nil {
		if stderrors.Is(err, biz.ErrTranscriptionTicketExpired) {
			return nil, biz.ErrTranscriptionTicketExpired
		}
		return nil, fmt.Errorf("validate consumed transcription ticket claims: %w", err)
	}
	return &claims, nil
}

func (r *TranscriptionTicketRepo) RevokeSession(ctx context.Context, sessionID string) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	mappingKey, err := r.client.Key("transcription-session-ticket:" + sessionID)
	if err != nil {
		return err
	}
	hash, err := r.client.Commands().Get(ctx, mappingKey).Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read transcription ticket index: %w", err)
	}
	ticketKey, err := r.client.Key("transcription-ticket:" + hash)
	if err != nil {
		return err
	}
	expiryKey, err := r.client.Key("transcription-ticket-expiry:" + hash)
	if err != nil {
		return err
	}
	if err := r.client.Commands().Del(ctx, ticketKey, expiryKey, mappingKey).Err(); err != nil {
		return fmt.Errorf("revoke transcription ticket: %w", err)
	}
	return nil
}
