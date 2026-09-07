package cli

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

func getTestDBURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("TEST_DB_URL not set, skipping database tests")
	}
	return url
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		DB: config.DBConfig{
			URL: getTestDBURL(t),
		},
	}
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	t.Cleanup(pool.Close)

	for _, table := range []string{"task_logs", "tasks", "workers"} {
		if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	return pool
}
