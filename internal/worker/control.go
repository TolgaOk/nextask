package worker

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// workerControl handles stop notifications throughout the worker's lifetime,
// including while database calls, retries, and task setup block the main loop.
type workerControl struct {
	events chan *pgconn.Notification
	done   chan struct{}
}

func watchWorker(ctx context.Context, stop context.CancelFunc, notifications <-chan *pgconn.Notification, stopChannel string) *workerControl {
	control := &workerControl{events: make(chan *pgconn.Notification, 16), done: make(chan struct{})}
	go func() {
		defer close(control.done)
		defer close(control.events)
		for {
			select {
			case <-ctx.Done():
				return
			case notification, ok := <-notifications:
				if !ok {
					stop()
					return
				}
				if notification.Channel == stopChannel {
					fmt.Println("Received stop signal, shutting down...")
					stop()
					return
				}
				// Wake/cancel notifications are hints backed by database state.
				// A full queue already wakes the consumer; never let it block stop.
				select {
				case control.events <- notification:
				default:
				}
			}
		}
	}()
	return control
}
