package worker

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db"
)

func getTestDBURL(t *testing.T) string {
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("TEST_DB_URL not set, skipping database tests")
	}
	return url
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()
	pool, err := db.Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	pool.Exec(ctx, "DROP TABLE IF EXISTS task_logs")
	pool.Exec(ctx, "DROP TABLE IF EXISTS tasks")

	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("failed to migrate: %v", err)
	}

	return pool
}

type testLogger struct {
	logs []string
}

func (l *testLogger) Log(stream, data string) {
	l.logs = append(l.logs, stream+": "+data)
}
