package notifiertest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

func notifierTestDBURL(t *testing.T) string {
	url := os.Getenv("TEST_DB_URL")
	if url == "" {
		t.Skip("TEST_DB_URL not set")
	}
	return url
}

func notifierTestPool(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()
	pool, err := db.Connect(ctx, notifierTestDBURL(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func TestNotifier_ReceiveNotification(t *testing.T) {
	pool := notifierTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	n, err := db.NewNotifier(ctx, notifierTestDBURL(t), db.NewBackOff(1*time.Second, 5*time.Second), []string{"test_ch1"})
	if err != nil {
		t.Fatalf("NewNotifier() error = %v", err)
	}
	defer n.Close(context.Background())

	db.Notify(ctx, pool, "test_ch1", db.WorkerWakeEvent{})

	select {
	case notif := <-n.C:
		if notif.Channel != "test_ch1" {
			t.Errorf("channel = %s, want test_ch1", notif.Channel)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestNotifier_MultipleChannels(t *testing.T) {
	pool := notifierTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	n, err := db.NewNotifier(ctx, notifierTestDBURL(t), db.NewBackOff(1*time.Second, 5*time.Second), []string{"multi_a", "multi_b"})
	if err != nil {
		t.Fatalf("NewNotifier() error = %v", err)
	}
	defer n.Close(context.Background())

	db.Notify(ctx, pool, "multi_a", db.WorkerWakeEvent{})
	db.Notify(ctx, pool, "multi_b", db.WorkerWakeEvent{})

	got := map[string]bool{}
	for range 2 {
		select {
		case notif := <-n.C:
			got[notif.Channel] = true
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for notification")
		}
	}

	if !got["multi_a"] || !got["multi_b"] {
		t.Errorf("got channels %v, want both multi_a and multi_b", got)
	}
}

func TestNotifier_Add(t *testing.T) {
	pool := notifierTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	n, err := db.NewNotifier(ctx, notifierTestDBURL(t), db.NewBackOff(1*time.Second, 5*time.Second), []string{"base_ch"})
	if err != nil {
		t.Fatalf("NewNotifier() error = %v", err)
	}
	defer n.Close(context.Background())

	if err := n.Add(ctx, "added_ch"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	db.Notify(ctx, pool, "added_ch", db.WorkerWakeEvent{})

	select {
	case notif := <-n.C:
		if notif.Channel != "added_ch" {
			t.Errorf("channel = %s, want added_ch", notif.Channel)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for notification on added channel")
	}
}

func TestNotifier_AddDuplicate(t *testing.T) {
	pool := notifierTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	n, err := db.NewNotifier(ctx, notifierTestDBURL(t), db.NewBackOff(1*time.Second, 5*time.Second), []string{"dup_ch"})
	if err != nil {
		t.Fatalf("NewNotifier() error = %v", err)
	}
	defer n.Close(context.Background())

	if err := n.Add(ctx, "dup_ch"); err != nil {
		t.Fatalf("Add() duplicate should not error, got: %v", err)
	}

	db.Notify(ctx, pool, "dup_ch", db.WorkerWakeEvent{})

	count := 0
	timeout := time.After(2 * time.Second)
	for {
		select {
		case <-n.C:
			count++
		case <-timeout:
			if count != 1 {
				t.Errorf("got %d notifications, want 1 (no duplicates)", count)
			}
			return
		}
	}
}

func TestNotifier_Remove(t *testing.T) {
	pool := notifierTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	n, err := db.NewNotifier(ctx, notifierTestDBURL(t), db.NewBackOff(1*time.Second, 5*time.Second), []string{"keep_ch", "remove_ch"})
	if err != nil {
		t.Fatalf("NewNotifier() error = %v", err)
	}
	defer n.Close(context.Background())

	n.Remove("remove_ch")
	time.Sleep(1 * time.Second)

	db.Notify(ctx, pool, "remove_ch", db.WorkerWakeEvent{})
	db.Notify(ctx, pool, "keep_ch", db.WorkerWakeEvent{})

	select {
	case notif := <-n.C:
		if notif.Channel != "keep_ch" {
			t.Errorf("got notification on %s, expected only keep_ch", notif.Channel)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for notification on keep_ch")
	}

	select {
	case notif := <-n.C:
		t.Errorf("unexpected notification on %s after remove", notif.Channel)
	case <-time.After(1 * time.Second):
	}
}

func TestNotifier_Close(t *testing.T) {
	ctx := context.Background()

	n, err := db.NewNotifier(ctx, notifierTestDBURL(t), db.NewBackOff(1*time.Second, 5*time.Second), []string{"close_ch"})
	if err != nil {
		t.Fatalf("NewNotifier() error = %v", err)
	}

	if err := n.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, ok := <-n.C
	if ok {
		t.Error("C should be closed after Close()")
	}

	// Double close should not panic
	n.Close(context.Background())
}

func TestNotifier_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	n, err := db.NewNotifier(ctx, notifierTestDBURL(t), db.NewBackOff(1*time.Second, 5*time.Second), []string{"cancel_ch"})
	if err != nil {
		t.Fatalf("NewNotifier() error = %v", err)
	}

	cancel()

	// C should eventually close
	select {
	case _, ok := <-n.C:
		if ok {
			// Got a notification before close, that's fine, drain
			for range n.C {
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("notifier did not shut down after context cancel")
	}
}
