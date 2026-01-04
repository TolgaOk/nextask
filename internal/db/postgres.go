package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db/migrations"
)

// Connect establishes a connection pool to the PostgreSQL database.
func Connect(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping: %w", err)
	}
	return pool, nil
}

// Migrate runs database migrations to create required tables.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrationFiles := []string{"001_init.sql"}
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
