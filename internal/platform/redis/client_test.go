package redis

import (
	"context"
	"crypto/tls"
	"os"
	"testing"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/internal/conf"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestOptionsFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*conf.Redis)
	}{
		{name: "valid"},
		{name: "missing address", mutate: func(c *conf.Redis) { c.Addr = "" }},
		{name: "missing address host", mutate: func(c *conf.Redis) { c.Addr = ":6379" }},
		{name: "invalid address port", mutate: func(c *conf.Redis) { c.Addr = "localhost:0" }},
		{name: "negative database", mutate: func(c *conf.Redis) { c.Db = -1 }},
		{name: "missing dial timeout", mutate: func(c *conf.Redis) { c.DialTimeout = nil }},
		{name: "excessive read timeout", mutate: func(c *conf.Redis) { c.ReadTimeout = durationpb.New(2 * time.Minute) }},
		{name: "zero pool", mutate: func(c *conf.Redis) { c.PoolSize = 0 }},
		{name: "idle exceeds pool", mutate: func(c *conf.Redis) { c.MinIdleConns = c.PoolSize + 1 }},
		{name: "missing prefix", mutate: func(c *conf.Redis) { c.KeyPrefix = "" }},
		{name: "unsafe prefix", mutate: func(c *conf.Redis) { c.KeyPrefix = "core:*:" }},
		{name: "invalid tls server name", mutate: func(c *conf.Redis) { c.TlsEnabled = true; c.TlsServerName = "redis.example.test:6379" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validRedisConfig()
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			options, prefix, err := optionsFromConfig(cfg)
			if tt.name == "valid" {
				if err != nil {
					t.Fatalf("optionsFromConfig() error = %v", err)
				}
				if options.PoolSize != 10 || prefix != "core:" {
					t.Fatalf("unexpected options: pool=%d prefix=%q", options.PoolSize, prefix)
				}
				return
			}
			if err == nil {
				t.Fatal("optionsFromConfig() expected error")
			}
		})
	}
}

func TestOptionsFromConfigEnablesTLS12(t *testing.T) {
	cfg := validRedisConfig()
	cfg.TlsEnabled = true
	options, _, err := optionsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if options.TLSConfig == nil || options.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("TLS 1.2 minimum was not configured")
	}
}

func TestClientKey(t *testing.T) {
	client := &Client{keyPrefix: "vision:"}
	key, err := client.Key("ticket:v1:abc")
	if err != nil {
		t.Fatal(err)
	}
	if key != "vision:ticket:v1:abc" {
		t.Fatalf("Key() = %q", key)
	}
	if _, err := client.Key(""); err == nil {
		t.Fatal("Key() expected empty suffix error")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenIntegration(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR is not set")
	}
	cfg := validRedisConfig()
	cfg.Addr = addr
	cfg.Username = os.Getenv("TEST_REDIS_USERNAME")
	cfg.Password = os.Getenv("TEST_REDIS_PASSWORD")
	client, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	key, err := client.Key("integration:v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Commands().Set(context.Background(), key, "ok", time.Minute).Err(); err != nil {
		t.Fatalf("set namespaced key: %v", err)
	}
	if cfg.GetUsername() != "" {
		if err := client.Commands().Set(context.Background(), "vision:integration:v1", "denied", time.Minute).Err(); err == nil {
			t.Fatal("cross-prefix write unexpectedly succeeded")
		}
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	if cfg.GetPassword() != "" {
		cfg.Password = "incorrect-password"
		if _, err := Open(context.Background(), cfg); err == nil {
			t.Fatal("Open() expected ACL authentication error")
		}
	}
}

func validRedisConfig() *conf.Redis {
	return &conf.Redis{
		Addr:          "127.0.0.1:6379",
		DialTimeout:   durationpb.New(time.Second),
		ReadTimeout:   durationpb.New(time.Second),
		WriteTimeout:  durationpb.New(time.Second),
		PoolSize:      10,
		MinIdleConns:  1,
		KeyPrefix:     "core:",
		TlsServerName: "redis.example.test",
	}
}
