package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/goleak"
)

func TestListen_ReceiveNotification(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	listener, err := Listen(ctx, dbURL, NewBackOff(100*time.Millisecond, 1*time.Second), "test_channel")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer listener.Close(context.Background())

	// Send notification from separate connection
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool connect failed: %v", err)
	}
	defer pool.Close()

	_, err = pool.Exec(ctx, "NOTIFY test_channel, 'hello'")
	if err != nil {
		t.Fatalf("NOTIFY failed: %v", err)
	}

	select {
	case notif := <-listener.C:
		if notif.Payload != "hello" {
			t.Errorf("expected payload 'hello', got %q", notif.Payload)
		}
		if notif.Channel != "test_channel" {
			t.Errorf("expected channel 'test_channel', got %q", notif.Channel)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestListen_Close(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	listener, err := Listen(ctx, dbURL, NewBackOff(100*time.Millisecond, 1*time.Second), "test_close")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	// Close should not block
	done := make(chan struct{})
	go func() {
		listener.Close(context.Background())
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked")
	}

	// Channel should be closed
	select {
	case _, ok := <-listener.C:
		if ok {
			t.Error("expected channel to be closed")
		}
	default:
		t.Error("channel not closed")
	}
}

func TestListen_ContextCancel(t *testing.T) {
	defer goleak.VerifyNone(t)

	dbURL := getTestDBURL(t)
	ctx, cancel := context.WithCancel(context.Background())

	listener, err := Listen(ctx, dbURL, NewBackOff(100*time.Millisecond, 1*time.Second), "test_cancel")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer listener.Close(context.Background())

	// Cancel context
	cancel()

	// Channel should close
	select {
	case _, ok := <-listener.C:
		if ok {
			// Might receive one more, drain it
			<-listener.C
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed after cancel")
	}
}

func TestListen_MultipleNotifications(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	listener, err := Listen(ctx, dbURL, NewBackOff(100*time.Millisecond, 1*time.Second), "test_multi")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer listener.Close(context.Background())

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool connect failed: %v", err)
	}
	defer pool.Close()

	// Send multiple notifications
	for i := 0; i < 3; i++ {
		_, err = pool.Exec(ctx, "NOTIFY test_multi, 'msg'")
		if err != nil {
			t.Fatalf("NOTIFY failed: %v", err)
		}
	}

	// Receive all
	received := 0
	timeout := time.After(2 * time.Second)
	for received < 3 {
		select {
		case <-listener.C:
			received++
		case <-timeout:
			t.Fatalf("timeout, received %d/3", received)
		}
	}
}

func TestListen_CloseIdempotent(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	listener, err := Listen(ctx, dbURL, NewBackOff(100*time.Millisecond, 1*time.Second), "test_idempotent")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	// Multiple closes should not panic
	listener.Close(context.Background())
	listener.Close(context.Background())
	listener.Close(context.Background())
}

func TestListen_CloseWithTimeout(t *testing.T) {
	defer goleak.VerifyNone(t)

	ctx := context.Background()
	dbURL := getTestDBURL(t)

	listener, err := Listen(ctx, dbURL, NewBackOff(100*time.Millisecond, 1*time.Second), "test_timeout")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}

	// Close with timeout should work
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	err = listener.Close(closeCtx)
	if err != nil {
		t.Errorf("Close with timeout failed: %v", err)
	}
}
