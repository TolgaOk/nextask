package worker

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestWorkerStartupCleanup(t *testing.T) {
	for _, mode := range []string{"notifier-failure", "cleanup-denied", "cleanup-timeout"} {
		t.Run(mode, func(t *testing.T) {
			pool := setupTestDB(t)
			ctx := context.Background()
			if mode != "notifier-failure" {
				body := "RAISE EXCEPTION 'cleanup denied' USING ERRCODE = '42501';"
				if mode == "cleanup-timeout" {
					body = "PERFORM pg_advisory_xact_lock(7654341);"
				}
				_, err := pool.Exec(ctx, `CREATE FUNCTION test_unregister_failure() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN
						IF NEW.status = 'stopped' THEN `+body+` END IF;
						RETURN NEW;
					END $$;
					CREATE TRIGGER test_unregister BEFORE UPDATE ON workers
					FOR EACH ROW EXECUTE FUNCTION test_unregister_failure()`)
				if err != nil {
					t.Fatal(err)
				}
				defer pool.Exec(ctx, "DROP TRIGGER test_unregister ON workers; DROP FUNCTION test_unregister_failure()")
			}
			if mode == "cleanup-timeout" {
				gate, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer gate.Rollback(ctx)
				if _, err := gate.Exec(ctx, "SELECT pg_advisory_xact_lock(7654341)"); err != nil {
					t.Fatal(err)
				}
			}
			w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: t.TempDir(), Name: "startup-failure", Once: true})
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			listener, err := db.Listen(ctx, getTestDBURL(t), db.NewBackOff(time.Millisecond, time.Second), db.FromWorkerChannel(w.ID))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close(ctx)
			// Fail the separate listener connection after registration via the pool.
			w.dbURL = "postgres://%invalid"
			started := time.Now()
			err = w.Run(ctx)
			var parseErr *pgconn.ParseConfigError
			if err == nil || !strings.Contains(err.Error(), "failed to start notifier") || !errors.As(err, &parseErr) {
				t.Fatalf("original startup error was lost: %v", err)
			}
			wantStatus := db.WorkerStatusStopped
			if mode != "notifier-failure" {
				wantStatus = db.WorkerStatusRunning
				if !strings.Contains(err.Error(), "unregister worker startup-failure") {
					t.Fatalf("cleanup failure was hidden: %v", err)
				}
			}
			if mode == "cleanup-denied" {
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
					t.Fatalf("cleanup error identity was lost: %v", err)
				}
			}
			if mode == "cleanup-timeout" {
				if elapsed := time.Since(started); elapsed < 4*time.Second || elapsed > 8*time.Second {
					t.Fatalf("cleanup deadline was skipped or exceeded: %v", elapsed)
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("cleanup timeout was lost: %v", err)
				}
			}
			var status db.WorkerStatus
			if err := pool.QueryRow(ctx, "SELECT status FROM workers WHERE id=$1", w.ID).Scan(&status); err != nil || status != wantStatus {
				t.Fatalf("unexpected worker status after startup failure: %s %v", status, err)
			}
			if mode == "notifier-failure" {
				select {
				case notification := <-listener.C:
					if notification == nil || notification.Payload != "stopped" {
						t.Fatalf("unexpected shutdown confirmation: %v", notification)
					}
				case <-time.After(time.Second):
					t.Fatal("successful registration cleanup was not confirmed")
				}
			} else {
				select {
				case notification := <-listener.C:
					t.Fatalf("failed registration cleanup was confirmed: %v", notification)
				case <-time.After(100 * time.Millisecond):
				}
			}
		})
	}
}

func TestWorkerStartupCancellationCleansRegistration(t *testing.T) {
	pool := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Accept the listener's TCP connection but withhold the PostgreSQL handshake.
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted, acceptDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(acceptDone)
		conn, err := server.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		<-ctx.Done()
	}()
	defer func() { cancel(); server.Close(); <-acceptDone }()
	w, err := New(ctx, Config{DBURL: getTestDBURL(t), Workdir: t.TempDir(), Name: "cancel-startup", Once: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.dbURL = "postgres://nextask_test@" + server.Addr().String() + "/postgres?sslmode=disable"
	var runErr error
	done := make(chan struct{})
	go func() { runErr = w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start its listener connection")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("startup ignored cancellation")
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "failed to start notifier") {
		t.Fatalf("missing startup error: %v", runErr)
	}
	var status db.WorkerStatus
	if err := pool.QueryRow(context.Background(), "SELECT status FROM workers WHERE id=$1", w.ID).Scan(&status); err != nil || status != db.WorkerStatusStopped {
		t.Fatalf("cancelled context prevented registration cleanup: %s %v", status, err)
	}
}
