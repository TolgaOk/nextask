package db

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestWorkerStatusBoundary(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// NOW() stays fixed in a transaction, allowing exact boundary checks without sleeps.
	_, err = tx.Exec(ctx, `INSERT INTO workers (id, pid, hostname, workdir, status, last_heartbeat)
		VALUES
		('recent', 1, 'host', '/tmp', 'running', NOW() - INTERVAL '1.499999 seconds'),
		('boundary', 2, 'host', '/tmp', 'running', NOW() - INTERVAL '1.5 seconds'),
		('expired', 3, 'host', '/tmp', 'running', NOW() - INTERVAL '1.500001 seconds'),
		('stopped', 4, 'host', '/tmp', 'stopped', NOW() - INTERVAL '1 day'),
		('future', 5, 'host', '/tmp', 'running', NOW() + INTERVAL '1 second')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		threshold time.Duration
		expired   WorkerStatus
	}{
		{"fractional", 1500 * time.Millisecond, WorkerStatusStale},
		{"longer", 3 * time.Second, WorkerStatusRunning},
		{"stored-status", 0, WorkerStatusRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := workerListQuery(WorkerListFilter{StaleThreshold: tc.threshold}).Columns("id", "status").ToSql()
			if err != nil {
				t.Fatal(err)
			}
			rows, err := tx.Query(ctx, sql, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			got := map[string]WorkerStatus{}
			for rows.Next() {
				var id string
				var status WorkerStatus
				if err := rows.Scan(&id, &status); err != nil {
					t.Fatal(err)
				}
				got[id] = status
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			want := map[string]WorkerStatus{
				"recent": WorkerStatusRunning, "boundary": WorkerStatusRunning,
				"expired": tc.expired, "stopped": WorkerStatusStopped, "future": WorkerStatusRunning,
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("statuses = %v, want %v", got, want)
			}
		})
	}
}

func TestWorkerStatusHeartbeatRecovery(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	if err := RegisterWorker(ctx, pool, "revived", 1, "host", "/tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE workers SET last_heartbeat = NOW() - INTERVAL '1 hour' WHERE id = 'revived'"); err != nil {
		t.Fatal(err)
	}
	check := func(want WorkerStatus) {
		t.Helper()
		for _, status := range []WorkerStatus{WorkerStatusRunning, WorkerStatusStale, WorkerStatusStopped} {
			filter := WorkerListFilter{Statuses: []WorkerStatus{status}, StaleThreshold: time.Minute, Limit: 5}
			rows, err := ListWorkers(ctx, pool, filter)
			if err != nil {
				t.Fatal(err)
			}
			expected := 0
			if status == want {
				expected = 1
			}
			if len(rows) != expected {
				t.Fatalf("%s rows = %v, want %d", status, rows, expected)
			}
			if len(rows) > 0 && (rows[0].ID != "revived" || rows[0].Status != want) {
				t.Errorf("unexpected worker: %+v", rows[0])
			}
			filter.Offset = 100 // Counts must ignore pagination.
			count, err := CountWorkers(ctx, pool, filter)
			if err != nil || count != expected {
				t.Errorf("%s count = %d, want %d: %v", status, count, expected, err)
			}
		}
	}
	check(WorkerStatusStale)
	var stored WorkerStatus
	if err := pool.QueryRow(ctx, "SELECT status FROM workers WHERE id = 'revived'").Scan(&stored); err != nil || stored != WorkerStatusRunning {
		t.Fatalf("derived status changed stored state: %s, %v", stored, err)
	}
	if err := UpdateHeartbeat(ctx, pool, "revived"); err != nil {
		t.Fatal(err)
	}
	check(WorkerStatusRunning)
	if err := UnregisterWorker(ctx, pool, "revived"); err != nil {
		t.Fatal(err)
	}
	check(WorkerStatusStopped)
}
