package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
	"github.com/cenkalti/backoff/v5"
)

const (
	watchPollInterval = time.Second
	watchCloseTimeout = 5 * time.Second
)

func interruptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}

// stateWatcher treats notifications as hints. Callers decide completion from
// stored state; polling covers lost hints and state changes without notifications.
type stateWatcher struct {
	retry    config.RetryConfig
	notifier *db.Notifier
	changes  chan struct{}
	cancel   context.CancelFunc
	closed   chan struct{}
}

func newStateWatcher(ctx context.Context, cfg config.Config, channels ...string) (*stateWatcher, error) {
	ctx, cancel := context.WithCancel(ctx)
	n, err := db.NewNotifier(ctx, cfg.DB.URL,
		db.NewBackOff(cfg.Retry.InitialInterval, cfg.Retry.MaxInterval), channels)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("listen failed: %w", err)
	}
	w := &stateWatcher{retry: cfg.Retry, notifier: n, changes: make(chan struct{}, 1), cancel: cancel, closed: make(chan struct{})}
	// Drain and coalesce hints even while a state check adds subscriptions.
	// Otherwise a full notification queue could block Notifier.Add.
	go func() {
		defer close(w.closed)
		defer close(w.changes)
		for {
			select {
			case _, ok := <-n.C:
				if !ok {
					return
				}
				select {
				case w.changes <- struct{}{}:
				default:
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return w, nil
}

func (w *stateWatcher) Close() {
	w.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), watchCloseTimeout)
	defer cancel()
	if err := w.notifier.Close(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "warning: listener cleanup: %v\n", err)
	}
	select {
	case <-w.closed:
	case <-ctx.Done():
	}
}

// Run checks immediately, then after hints or on a timer. Completion and its
// result are separate: a finished task may itself have a nonzero exit code.
func (w *stateWatcher) Run(ctx context.Context, check func(context.Context) (bool, error)) error {
	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()
	listenerClosed := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := db.RetryValue(ctx, func() (bool, error) { return check(ctx) },
			backoff.WithBackOff(db.NewBackOff(w.retry.InitialInterval, w.retry.MaxInterval)),
			backoff.WithMaxElapsedTime(0),
			backoff.WithNotify(func(err error, delay time.Duration) {
				fmt.Fprintf(os.Stderr, "watch: %s, retry in %v\n", db.HumanError(err), delay)
			}))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || done {
			return err
		}
		if listenerClosed {
			return fmt.Errorf("notification listener closed before completion")
		}
		select {
		case _, ok := <-w.changes:
			listenerClosed = !ok
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
