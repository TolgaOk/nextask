package db

import (
	"context"

	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Listener provides auto-reconnecting LISTEN on a single PostgreSQL channel.
// Connection ownership, recovery, and shutdown are shared with Notifier.
type Listener struct {
	C        chan *pgconn.Notification
	notifier *Notifier
}

// Listen creates a listener with auto-reconnect on connection failure.
func Listen(ctx context.Context, dbURL string, b *backoff.ExponentialBackOff, channel string) (*Listener, error) {
	notifier, err := newNotifier(ctx, dbURL, b, []string{channel}, 1, nil)
	if err != nil {
		return nil, err
	}
	return &Listener{C: notifier.C, notifier: notifier}, nil
}

// Close stops the listener and waits for cleanup.
// Pass a context with timeout to bound the wait.
func (l *Listener) Close(ctx context.Context) error {
	return l.notifier.Close(ctx)
}
