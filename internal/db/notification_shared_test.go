package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/goleak"
)

func TestNotificationChannelsAndBackpressure(t *testing.T) {
	for _, kind := range []string{"listener", "notifier"} {
		t.Run(kind, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			pool, err := Connect(ctx, getTestDBURL(t))
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			channel := `quoted" channel; ` + kind
			var notifications <-chan *pgconn.Notification
			var closeStream func(context.Context) error
			if kind == "listener" {
				listener, err := Listen(ctx, getTestDBURL(t), NewBackOff(time.Millisecond, time.Second), channel)
				if err != nil {
					t.Fatal(err)
				}
				notifications, closeStream = listener.C, listener.Close
			} else {
				notifier, err := NewNotifier(ctx, getTestDBURL(t), NewBackOff(time.Millisecond, time.Second), []string{channel}, nil)
				if err != nil {
					t.Fatal(err)
				}
				notifications, closeStream = notifier.C, notifier.Close
			}
			defer closeStream(context.Background())
			if _, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, "literal"); err != nil {
				t.Fatal(err)
			}
			select {
			case notification := <-notifications:
				if notification == nil || notification.Channel != channel || notification.Payload != "literal" {
					t.Fatalf("channel was not treated literally: %+v", notification)
				}
			case <-ctx.Done():
				t.Fatal("notification not delivered")
			}
			// Shutdown must release the connection even when output is not consumed.
			for range cap(notifications) + 2 {
				if _, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, "buffered"); err != nil {
					t.Fatal(err)
				}
			}
			closeCtx, stop := context.WithTimeout(ctx, time.Second)
			defer stop()
			if err := closeStream(closeCtx); err != nil {
				t.Fatalf("close blocked on output: %v", err)
			}
			for range notifications {
			}
		})
	}
}

func TestNotifierReconnectSubscriptions(t *testing.T) {
	defer goleak.VerifyNone(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	pool, err := Connect(ctx, getTestDBURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	base, removed, added, extra := "shared_base", `shared_removed"channel`, `shared_added"channel`, `batch_extra"channel`
	notifier, err := NewNotifier(ctx, getTestDBURL(t), NewBackOff(10*time.Millisecond, time.Second), []string{base, removed}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer notifier.Close(context.Background())
	if err := notifier.Add(ctx, added, base, added, extra); err != nil {
		t.Fatal(err)
	}
	pid := waitForSubscriptionQuery(t, ctx, pool, "LISTEN "+pgx.Identifier{extra}.Sanitize())
	notifier.Remove(removed)
	waitForSubscriptionQuery(t, ctx, pool, "UNLISTEN "+pgx.Identifier{removed}.Sanitize())
	if err := terminateBackend(ctx, pool, pid); err != nil {
		t.Fatal(err)
	}
	// Probe until the reconnected listener delivers from its dynamically added
	// channel. Delivery means all LISTEN statements in connect have completed.
	probe := time.NewTicker(20 * time.Millisecond)
	defer probe.Stop()
ready:
	for {
		select {
		case <-ctx.Done():
			t.Fatal("dynamic subscription was not restored")
		case <-probe.C:
			if _, err := pool.Exec(ctx, "SELECT pg_notify($1, 'probe')", added); err != nil {
				t.Fatal(err)
			}
		case notification := <-notifier.C:
			if notification == nil {
				t.Fatal("notifier closed during reconnect")
			}
			if notification.Channel == added && notification.Payload == "probe" {
				break ready
			}
		}
	}
	if _, err := pool.Exec(ctx, "SELECT pg_notify($1, 'removed')", removed); err != nil {
		t.Fatal(err)
	}
	remaining := map[string]bool{base: true, extra: true}
	for channel := range remaining {
		if _, err := pool.Exec(ctx, "SELECT pg_notify($1, 'after')", channel); err != nil {
			t.Fatal(err)
		}
	}
	for len(remaining) > 0 {
		select {
		case <-ctx.Done():
			t.Fatalf("subscriptions were not restored: %v", remaining)
		case notification := <-notifier.C:
			if notification == nil || notification.Channel == removed {
				t.Fatalf("removed subscription returned: %+v", notification)
			}
			if notification.Payload == "after" {
				delete(remaining, notification.Channel)
			}
		}
	}
}

func waitForSubscriptionQuery(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string) int32 {
	t.Helper()
	for {
		var pid int32
		err := pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity
			WHERE pid != pg_backend_pid() AND datname = current_database()
			AND query = $1 AND state = 'idle' AND wait_event = 'ClientRead'
			ORDER BY backend_start DESC LIMIT 1`, query).Scan(&pid)
		if err == nil {
			return pid
		}
		if err != pgx.ErrNoRows {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("subscription query was not observed: %s", query)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
