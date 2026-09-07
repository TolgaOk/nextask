package worker

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestWorkerControlStopWithoutConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	notifications := make(chan *pgconn.Notification, 64)
	control := watchWorker(ctx, cancel, notifications, "stop", nil)
	defer func() { cancel(); <-control.done }()
	// Fill the forwarded queue while the main loop is blocked on database work.
	for range 32 {
		notifications <- &pgconn.Notification{Channel: "wake"}
	}
	notifications <- &pgconn.Notification{Channel: "stop"}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("queued wake notifications blocked worker stop")
	}
}

func TestWorkerControlForwardAndDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	notifications := make(chan *pgconn.Notification, 1)
	control := watchWorker(ctx, cancel, notifications, "stop", nil)
	defer func() { cancel(); <-control.done }()
	notification := &pgconn.Notification{Channel: "to_task_example", Payload: "cancel"}
	notifications <- notification
	select {
	case got := <-control.events:
		if got != notification {
			t.Fatal("task notification changed")
		}
	case <-time.After(time.Second):
		t.Fatal("task notification was not forwarded")
	}
	close(notifications)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("worker kept running after notifier closed")
	}
}
