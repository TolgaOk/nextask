package db

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const waitTimeout = 500 * time.Millisecond

// Notifier provides auto-reconnecting LISTEN on multiple PostgreSQL channels
// with dynamic Add/Remove support. A single pgx.Conn handles all channels.
// Only the run goroutine touches the connection — no mutex required.
type Notifier struct {
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
	channel string
	result  chan error
}

// NewNotifier creates a notifier listening on the given channels with auto-reconnect.
// Use Add and Remove to dynamically subscribe/unsubscribe channels after creation.
func NewNotifier(ctx context.Context, dbURL string, b *backoff.ExponentialBackOff, channels []string) (*Notifier, error) {
	innerCtx, cancel := context.WithCancel(ctx)

	chMap := make(map[string]bool, len(channels))
	for _, ch := range channels {
		chMap[ch] = true
	}

	n := &Notifier{
		dbURL:    dbURL,
		backoff:  b,
		cancel:   cancel,
		C:        make(chan *pgconn.Notification, 16),
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

// Add subscribes to a new channel. Blocks until LISTEN is confirmed or ctx expires.
func (n *Notifier) Add(ctx context.Context, channel string) error {
	req := addRequest{
		channel: channel,
		result:  make(chan error, 1),
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
		if _, err := conn.Exec(connCtx, "LISTEN \""+ch+"\""); err != nil {
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

		fmt.Fprintf(os.Stderr, "notifier reconnect: %s, retry in %v\n", HumanError(err), wait)

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
			if n.channels[req.channel] {
				req.result <- nil
				continue
			}
			if n.conn == nil {
				req.result <- fmt.Errorf("connection down")
				continue
			}
			execCtx, cancel := context.WithTimeout(ctx, connectTimeout)
			_, err := n.conn.Exec(execCtx, "LISTEN \""+req.channel+"\"")
			cancel()
			if err == nil {
				n.channels[req.channel] = true
			}
			req.result <- err
		case ch := <-n.removeCh:
			if !n.channels[ch] {
				continue
			}
			delete(n.channels, ch)
			if n.conn != nil {
				execCtx, cancel := context.WithTimeout(ctx, connectTimeout)
				n.conn.Exec(execCtx, "UNLISTEN \""+ch+"\"")
				cancel()
			}
		default:
			return
		}
	}
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
				fmt.Fprintf(os.Stderr, "notifier gave up: %s\n", HumanError(err))
			}
			n.failPendingAdds(err)
			return
		}
	}
}
