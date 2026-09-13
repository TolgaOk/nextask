package db

import (
	"context"
	"fmt"

	"github.com/TolgaOk/nextask/internal/db/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect establishes a connection pool to the PostgreSQL database.
// Pool size is capped at 4 connections to keep total usage low when many workers
// share the same database (each worker also opens 2-3 dedicated LISTEN connections).
func Connect(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, wrapPgError(err)
	}
	config.MaxConns = 4

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, wrapPgError(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, wrapPgError(err)
	}
	return pool, nil
}

// Migrate runs database migrations to create required tables.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrationFiles := []string{"001_init.sql", "002_workers.sql", "003_log_seq.sql", "004_execution_command.sql", "005_cleanup_timeout.sql"}
	for _, file := range migrationFiles {
		sql, err := migrations.FS.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", file, err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("failed to run migration %s: %w", file, err)
		}
	}
	return nil
}
