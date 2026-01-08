package db

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/TolgaOk/nextask/internal/db/migrations"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors for database operations.
var (
	ErrDBNotExist     = errors.New("database does not exist")
	ErrConnRefused    = errors.New("connection refused")
	ErrAuthFailed     = errors.New("authentication failed")
	ErrNotInitialized = errors.New("database not initialized")
)

// Connect establishes a connection pool to the PostgreSQL database.
func Connect(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, wrapPgError(err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, wrapPgError(err)
	}
	return pool, nil
}

func wrapPgError(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "3D000": // invalid_catalog_name (database does not exist)
			return ErrDBNotExist
		case "28P01", "28000": // invalid_password, invalid_authorization_specification
			return ErrAuthFailed
		case "42P01": // undefined_table
			return ErrNotInitialized
		}
	}

	// Check for connection refused via net.OpError
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return ErrConnRefused
	}

	return err
}

// Migrate runs database migrations to create required tables.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrationFiles := []string{"001_init.sql", "002_workers.sql"}
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
