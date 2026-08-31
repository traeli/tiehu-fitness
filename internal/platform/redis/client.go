// Package redis provides the business-neutral Redis connection shared by the
// deployable services. Domain repositories remain responsible for key schemas
// and commands.
package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	maxRedisTimeout = time.Minute
	maxRedisPool    = int32(10_000)
	maxRedisDB      = int32(1_024)
)

var keyPrefixPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*:$`)

// Client owns one service's Redis connection pool and configured key prefix.
type Client struct {
	raw       *redis.Client
	keyPrefix string
}

// Open validates config, creates a Redis pool, and verifies connectivity.
func Open(ctx context.Context, cfg *conf.Redis) (*Client, error) {
	if ctx == nil {
		return nil, fmt.Errorf("redis open context is required")
	}
	options, prefix, err := optionsFromConfig(cfg)
	if err != nil {
		return nil, err
	}

	raw := redis.NewClient(options)
	pingCtx, cancel := context.WithTimeout(ctx, options.DialTimeout)
	defer cancel()
	if err := raw.Ping(pingCtx).Err(); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Client{raw: raw, keyPrefix: prefix}, nil
}

// Key scopes a non-empty domain suffix to the service's configured namespace.
func (c *Client) Key(suffix string) (string, error) {
	if c == nil || c.keyPrefix == "" {
		return "", fmt.Errorf("redis client is not initialized")
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return "", fmt.Errorf("redis key suffix is required")
	}
	if strings.HasPrefix(suffix, ":") {
		return "", fmt.Errorf("redis key suffix must not start with a separator")
	}
	return c.keyPrefix + suffix, nil
}

// Commands exposes the Redis command interface to data-layer adapters. Callers
// must build every domain key through Key; Redis ACL key patterns are the
// second isolation boundary.
func (c *Client) Commands() redis.Cmdable {
	if c == nil {
		return nil
	}
	return c.raw
}

// Close releases the Redis connection pool.
func (c *Client) Close() error {
	if c == nil || c.raw == nil {
		return nil
	}
	return c.raw.Close()
}

func optionsFromConfig(cfg *conf.Redis) (*redis.Options, string, error) {
	if cfg == nil {
		return nil, "", fmt.Errorf("redis config is required")
	}
	host, port, err := net.SplitHostPort(cfg.GetAddr())
	if err != nil {
		return nil, "", fmt.Errorf("redis addr must be host:port: %w", err)
	}
	if host == "" {
		return nil, "", fmt.Errorf("redis addr host is required")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return nil, "", fmt.Errorf("redis addr port must be between 1 and 65535")
	}
	if cfg.GetDb() < 0 || cfg.GetDb() > maxRedisDB {
		return nil, "", fmt.Errorf("redis db must be between 0 and %d", maxRedisDB)
	}
	dialTimeout, err := requiredDuration("redis dial_timeout", cfg.GetDialTimeout())
	if err != nil {
		return nil, "", err
	}
	readTimeout, err := requiredDuration("redis read_timeout", cfg.GetReadTimeout())
	if err != nil {
		return nil, "", err
	}
	writeTimeout, err := requiredDuration("redis write_timeout", cfg.GetWriteTimeout())
	if err != nil {
		return nil, "", err
	}
	if cfg.GetPoolSize() <= 0 || cfg.GetPoolSize() > maxRedisPool {
		return nil, "", fmt.Errorf("redis pool_size must be between 1 and %d", maxRedisPool)
	}
	if cfg.GetMinIdleConns() < 0 || cfg.GetMinIdleConns() > cfg.GetPoolSize() {
		return nil, "", fmt.Errorf("redis min_idle_conns must be between 0 and pool_size")
	}
	prefix := cfg.GetKeyPrefix()
	if !keyPrefixPattern.MatchString(prefix) {
		return nil, "", fmt.Errorf("redis key_prefix must match %s", keyPrefixPattern.String())
	}

	options := &redis.Options{
		Addr:         cfg.GetAddr(),
		Username:     cfg.GetUsername(),
		Password:     cfg.GetPassword(),
		DB:           int(cfg.GetDb()),
		DialTimeout:  dialTimeout,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		PoolSize:     int(cfg.GetPoolSize()),
		MinIdleConns: int(cfg.GetMinIdleConns()),
	}
	if cfg.GetTlsEnabled() {
		serverName := cfg.GetTlsServerName()
		if serverName == "" {
			serverName = host
		}
		if strings.ContainsAny(serverName, " \t\r\n/:") {
			return nil, "", fmt.Errorf("redis tls_server_name is invalid")
		}
		options.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		}
	}
	return options, prefix, nil
}

func requiredDuration(name string, value *durationpb.Duration) (time.Duration, error) {
	if value == nil {
		return 0, fmt.Errorf("%s is required", name)
	}
	duration := value.AsDuration()
	if duration <= 0 || duration > maxRedisTimeout {
		return 0, fmt.Errorf("%s must be greater than zero and at most %s", name, maxRedisTimeout)
	}
	return duration, nil
}
