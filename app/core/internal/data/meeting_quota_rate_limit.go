package data

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	platformredis "github.com/tiehu-ai/tiehu-fitness/internal/platform/redis"
)

var meetingCreateRateScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
return {count, ttl}
`)

type MeetingCreateRateLimiter struct {
	commands redis.Cmdable
	key      func(string) (string, error)
}

var _ biz.MeetingCreateRateLimiter = (*MeetingCreateRateLimiter)(nil)

func NewMeetingCreateRateLimiter(client *platformredis.Client) (biz.MeetingCreateRateLimiter, error) {
	if client == nil || client.Commands() == nil {
		return nil, fmt.Errorf("Redis client is required for meeting create rate limiter")
	}
	return &MeetingCreateRateLimiter{commands: client.Commands(), key: client.Key}, nil
}

func (l *MeetingCreateRateLimiter) Allow(ctx context.Context, userID string, now time.Time, limit int32, window time.Duration) (biz.MeetingCreateRateDecision, error) {
	if limit <= 0 || window <= 0 {
		return biz.MeetingCreateRateDecision{}, fmt.Errorf("meeting create rate policy is invalid")
	}
	windowStart, ttl := fixedRateWindow(now, window)
	key, err := l.key("meeting_create_rate:v1:" + userID + ":" + strconv.FormatInt(windowStart.UnixMilli(), 10))
	if err != nil {
		return biz.MeetingCreateRateDecision{}, err
	}
	ttlMillis := ttl.Milliseconds()
	if ttl%time.Millisecond != 0 {
		ttlMillis++
	}
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	result, err := meetingCreateRateScript.Run(ctx, l.commands, []string{key}, ttlMillis).Slice()
	if err != nil {
		return biz.MeetingCreateRateDecision{}, fmt.Errorf("apply meeting create rate limit: %w", err)
	}
	if len(result) != 2 {
		return biz.MeetingCreateRateDecision{}, fmt.Errorf("meeting create rate limit returned invalid result")
	}
	count, ok := result[0].(int64)
	if !ok {
		return biz.MeetingCreateRateDecision{}, fmt.Errorf("meeting create rate limit count has invalid type")
	}
	remainingMillis, ok := result[1].(int64)
	if !ok {
		return biz.MeetingCreateRateDecision{}, fmt.Errorf("meeting create rate limit TTL has invalid type")
	}
	if remainingMillis <= 0 {
		remainingMillis = ttlMillis
	}
	return biz.MeetingCreateRateDecision{
		Allowed: count <= int64(limit), RetryAfter: time.Duration(remainingMillis) * time.Millisecond,
	}, nil
}

func fixedRateWindow(now time.Time, window time.Duration) (time.Time, time.Duration) {
	now = now.UTC()
	start := now.Truncate(window)
	remaining := start.Add(window).Sub(now)
	if remaining <= 0 {
		remaining = window
	}
	return start, remaining
}
