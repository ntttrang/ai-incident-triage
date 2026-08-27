package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // registers the PostgreSQL driver for golang-migrate
	_ "github.com/golang-migrate/migrate/v4/source/file"       // registers the file source for golang-migrate
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ntttrang/ai-incident-triage/internal/platform/config"
)

// NewPool creates a configured pgx connection pool.
func NewPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL())
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}
	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.MinConns = cfg.DBMinConns
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}

// RunMigrations applies all pending SQL migrations. A migrations directory with
// no .up.sql files yet is not an error — the service boots and reports version 0.
func RunMigrations(cfg *config.Config, log *slog.Logger) error {
	if !hasMigrations(cfg.MigrationsPath) {
		log.Info("no migrations present, skipping migrate")
		return nil
	}

	m, err := migrate.New(cfg.MigrationsPath, cfg.DatabaseURL())
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("migration version: %w", err)
	}
	log.Info("migrations applied", "version", version, "dirty", dirty)
	return nil
}

// hasMigrations reports whether the file source contains at least one .up.sql
// migration. golang-migrate's file source fails on an empty directory, and a
// freshly bootstrapped repo legitimately has no migrations yet.
func hasMigrations(sourceURL string) bool {
	if !strings.HasPrefix(sourceURL, "file://") {
		return true
	}
	dir := strings.TrimPrefix(sourceURL, "file://")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			return true
		}
	}
	return false
}
