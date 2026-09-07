package db

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	connectTimeout = 10 * time.Second
	waitTimeout    = 500 * time.Millisecond
)

// Notifier provides auto-reconnecting LISTEN on multiple PostgreSQL channels
// with dynamic Add/Remove support. A single pgx.Conn handles all channels.
// Only the run goroutine touches the connection — no mutex required.
type Notifier struct {
	stderr  io.Writer
	dbURL   string
	backoff *backoff.ExponentialBackOff

	cancel context.CancelFunc
	conn   *pgx.Conn
	C      chan *pgconn.Notification
	exited chan struct{}
	once   sync.Once

	channels map[string]bool
	addCh    chan addRequest
	removeCh chan string
}

type addRequest struct {
	channels []string
	result   chan error
}

// NewNotifier creates a notifier listening on the given channels with auto-reconnect.
// Use Add and Remove to dynamically subscribe/unsubscribe channels after creation.
// Diagnostics go to stderr (nil uses process stderr); the writer must support concurrent writes.
func NewNotifier(ctx context.Context, dbURL string, b *backoff.ExponentialBackOff, channels []string, stderr io.Writer) (*Notifier, error) {
	return newNotifier(ctx, dbURL, b, channels, 16, stderr)
}

func newNotifier(ctx context.Context, dbURL string, b *backoff.ExponentialBackOff, channels []string, bufferSize int, stderr io.Writer) (*Notifier, error) {
	if stderr == nil {
		stderr = os.Stderr
	}
	innerCtx, cancel := context.WithCancel(ctx)

	chMap := make(map[string]bool, len(channels))
	for _, ch := range channels {
		chMap[ch] = true
	}

	n := &Notifier{
		stderr:   stderr,
		dbURL:    dbURL,
		backoff:  b,
		cancel:   cancel,
		C:        make(chan *pgconn.Notification, bufferSize),
		exited:   make(chan struct{}),
		channels: chMap,
		addCh:    make(chan addRequest, 16),
		removeCh: make(chan string, 16),
	}

	if err := n.connect(innerCtx); err != nil {
		cancel()
		return nil, err
	}

	go n.run(innerCtx)

	return n, nil
}

// Add subscribes to channels in one batch. Blocks until all LISTENs are confirmed or ctx expires.
func (n *Notifier) Add(ctx context.Context, channels ...string) error {
	if len(channels) == 0 {
		return nil
	}
	req := addRequest{
		channels: append([]string(nil), channels...),
		result:   make(chan error, 1),
	}
	select {
	case n.addCh <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-n.exited:
		return fmt.Errorf("notifier closed")
	}
	select {
	case err := <-req.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-n.exited:
		return fmt.Errorf("notifier closed")
	}
}

// Remove unsubscribes from a channel. Fire-and-forget.
func (n *Notifier) Remove(channel string) {
	select {
	case n.removeCh <- channel:
	default:
	}
}

// Close stops the notifier and waits for cleanup.
func (n *Notifier) Close(ctx context.Context) error {
	n.once.Do(n.cancel)

	select {
	case <-n.exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (n *Notifier) connect(ctx context.Context) error {
	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	conn, err := pgx.Connect(connCtx, n.dbURL)
	if err != nil {
		return err
	}

	for ch := range n.channels {
		if _, err := conn.Exec(connCtx, "LISTEN "+pgx.Identifier{ch}.Sanitize()); err != nil {
			conn.Close(context.Background())
			return err
		}
	}

	n.conn = conn
	return nil
}

func (n *Notifier) reconnect(ctx context.Context) error {
	n.backoff.Reset()

	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := n.connect(ctx)
		if err == nil {
			return nil
		}

		if !IsTransient(err) {
			return err
		}

		wait := n.backoff.NextBackOff()
		if wait == backoff.Stop {
			return err
		}

		fmt.Fprintf(n.stderr, "notifier reconnect: %s, retry in %v\n", HumanError(err), wait)

		timer.Reset(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (n *Notifier) processRequests(ctx context.Context) {
	for {
		select {
		case req := <-n.addCh:
			req.result <- n.addChannels(ctx, req.channels)
		case ch := <-n.removeCh:
			if !n.channels[ch] {
				continue
			}
			delete(n.channels, ch)
			if n.conn != nil {
				execCtx, cancel := context.WithTimeout(ctx, connectTimeout)
				n.conn.Exec(execCtx, "UNLISTEN "+pgx.Identifier{ch}.Sanitize())
				cancel()
			}
		default:
			return
		}
	}
}

func (n *Notifier) addChannels(ctx context.Context, channels []string) error {
	execCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	for _, channel := range channels {
		if n.channels[channel] {
			continue
		}
		if _, err := n.conn.Exec(execCtx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
			return err
		}
		n.channels[channel] = true
	}
	return nil
}

func (n *Notifier) failPendingAdds(err error) {
	for {
		select {
		case req := <-n.addCh:
			req.result <- err
		default:
			return
		}
	}
}

func (n *Notifier) run(ctx context.Context) {
	defer close(n.exited)
	defer close(n.C)
	defer func() {
		if n.conn != nil {
			n.conn.Close(context.Background())
		}
	}()

	for {
		if ctx.Err() != nil {
			n.failPendingAdds(ctx.Err())
			return
		}

		n.processRequests(ctx)

		waitCtx, waitCancel := context.WithTimeout(ctx, waitTimeout)
		notif, err := n.conn.WaitForNotification(waitCtx)
		waitCancel()

		if err == nil {
			select {
			case n.C <- notif:
			case <-ctx.Done():
				n.failPendingAdds(ctx.Err())
				return
			}
			continue
		}

		if ctx.Err() != nil {
			n.failPendingAdds(ctx.Err())
			return
		}

		// Normal timeout — loop back to check requests
		if waitCtx.Err() == context.DeadlineExceeded {
			continue
		}

		// Connection lost
		n.conn.Close(context.Background())
		n.conn = nil

		n.failPendingAdds(fmt.Errorf("connection lost"))

		if err := n.reconnect(ctx); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(n.stderr, "notifier gave up: %s\n", HumanError(err))
			}
			n.failPendingAdds(err)
			return
		}
	}
}
