package worker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// Ignore known background goroutines from dependencies
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
}

func getTestDBURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("TEST_DB_URL not set, skipping database tests")
	}
	return url
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

type testLogger struct {
	logs []string
}

func (l *testLogger) Log(_ context.Context, stream, data string) {
	l.logs = append(l.logs, stream+": "+data)
}

// waitForTaskStart waits until execution has begun after the cancel subscription
// is established. A fixed sleep races the notifier's polling interval.
func waitForTaskStart(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var started bool
		err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM task_logs WHERE task_id = $1 AND stream = 'nextask' AND data LIKE '[info] running:%')", id).Scan(&started)
		if err != nil {
			t.Fatalf("wait for task startup: %v", err)
		}
		if started {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("task did not start")
		case <-ticker.C:
		}
	}
}
