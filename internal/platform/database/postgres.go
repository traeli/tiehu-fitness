package database

import (
	"context"
	"fmt"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/internal/conf"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const defaultPingTimeout = 5 * time.Second

// OpenPostgres creates a GORM PostgreSQL connection pool from service config.
// Each service synchronizes only its owned models after opening the pool.
func OpenPostgres(c *conf.Database) (*gorm.DB, error) {
	if c == nil {
		return nil, fmt.Errorf("database config is required")
	}
	if c.GetDriver() != "" && c.GetDriver() != "postgres" {
		return nil, fmt.Errorf("unsupported database driver %q", c.GetDriver())
	}
	if c.GetDsn() == "" {
		return nil, fmt.Errorf("database dsn is required")
	}

	db, err := gorm.Open(postgres.Open(c.GetDsn()), &gorm.Config{
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql database: %w", err)
	}
	if c.GetMaxIdleConns() > 0 {
		sqlDB.SetMaxIdleConns(int(c.GetMaxIdleConns()))
	}
	if c.GetMaxOpenConns() > 0 {
		sqlDB.SetMaxOpenConns(int(c.GetMaxOpenConns()))
	}
	if c.GetConnMaxLifetime() != nil {
		sqlDB.SetConnMaxLifetime(c.GetConnMaxLifetime().AsDuration())
	}
	if c.GetConnMaxIdleTime() != nil {
		sqlDB.SetConnMaxIdleTime(c.GetConnMaxIdleTime().AsDuration())
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultPingTimeout)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

// Close releases the underlying database/sql connection pool.
func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
