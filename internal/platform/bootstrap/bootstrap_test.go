package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesEnvironmentPlaceholder(t *testing.T) {
	t.Setenv("TEST_DATABASE_DSN", "postgres://tiehu:test@127.0.0.1:5432/tiehu_core")
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("server: {}\ndatabase:\n  driver: postgres\n  dsn: ${TEST_DATABASE_DSN:}\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	bc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := bc.GetDatabase().GetDsn(); got != "postgres://tiehu:test@127.0.0.1:5432/tiehu_core" {
		t.Fatalf("database dsn = %q", got)
	}
}

func TestLoadServiceConfigs(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "core", path: filepath.Join("..", "..", "..", "configs", "core.yaml")},
		{name: "vision", path: filepath.Join("..", "..", "..", "configs", "vision.yaml")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bc, err := Load(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if bc.GetRedis() == nil || bc.GetRedis().GetKeyPrefix() == "" {
				t.Fatal("redis config was not loaded")
			}
			if tt.name == "core" && bc.GetDatabase() == nil {
				t.Fatal("core database config was not loaded")
			}
			if tt.name == "core" && bc.GetUtools() == nil {
				t.Fatal("utools config was not loaded")
			}
			if tt.name == "vision" && bc.GetAsr() == nil {
				t.Fatal("asr config was not loaded")
			}
		})
	}
}
