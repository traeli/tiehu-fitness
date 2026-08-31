package bootstrap

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	"github.com/go-kratos/kratos/v3"
	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
	"github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/transport"
	_ "go.uber.org/automaxprocs"
)

// Load reads one service's bootstrap configuration.
func Load(path string) (*conf.Bootstrap, error) {
	c := config.New(
		config.WithSource(
			file.NewSource(path),
			// Load process environment so placeholders such as
			// ${CORE_DATABASE_DSN:} in YAML can be resolved.
			env.NewSource(),
		),
	)
	defer c.Close()
	if err := c.Load(); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		return nil, fmt.Errorf("scan config: %w", err)
	}
	if bc.Server == nil {
		return nil, fmt.Errorf("config %s does not define server", path)
	}
	return &bc, nil
}

// NewLogger creates the common structured logger used by every service.
func NewLogger(serviceName, instanceID string) *slog.Logger {
	logger := log.NewLogger(
		slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: true,
			Level:     slog.LevelInfo,
		}),
	).With(
		slog.String("service.id", instanceID),
		slog.String("service.name", serviceName),
	)
	log.SetDefault(logger)
	return logger
}

// NewApp wires the standard Kratos lifecycle around HTTP and gRPC transports.
func NewApp(name, version, instanceID string, logger *slog.Logger, servers ...transport.Server) *kratos.App {
	return kratos.New(
		kratos.ID(instanceID),
		kratos.Name(name),
		kratos.Version(version),
		kratos.Logger(logger),
		kratos.Server(servers...),
	)
}
