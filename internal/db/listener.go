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

const (
	connectTimeout = 10 * time.Second
)

// Listener provides auto-reconnecting LISTEN on a PostgreSQL channel.
type Listener struct {
	dbURL   string
	channel string
	backoff *backoff.ExponentialBackOff

	cancel context.CancelFunc
	conn   *pgx.Conn
	C      chan *pgconn.Notification
	exited chan struct{}
	once   sync.Once
}

// Listen creates a listener with auto-reconnect on connection failure.
func Listen(ctx context.Context, dbURL string, b *backoff.ExponentialBackOff, channel string) (*Listener, error) {
	innerCtx, cancel := context.WithCancel(ctx)

	l := &Listener{
		dbURL:   dbURL,
		channel: channel,
		backoff: b,
		cancel:  cancel,
		C:       make(chan *pgconn.Notification, 1),
		exited:  make(chan struct{}),
	}

	if err := l.connect(innerCtx); err != nil {
		cancel()
		return nil, err
	}

	go l.run(innerCtx)

	return l, nil
}

// Close stops the listener and waits for cleanup.
// Pass a context with timeout to bound the wait.
func (l *Listener) Close(ctx context.Context) error {
	l.once.Do(l.cancel)

	select {
	case <-l.exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Listener) connect(ctx context.Context) error {
	connCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	conn, err := pgx.Connect(connCtx, l.dbURL)
	if err != nil {
		return err
	}

	if _, err := conn.Exec(connCtx, "LISTEN \""+l.channel+"\""); err != nil {
		conn.Close(context.Background())
		return err
	}

	l.conn = conn
	return nil
}

func (l *Listener) reconnect(ctx context.Context) error {
	l.backoff.Reset()

	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := l.connect(ctx)
		if err == nil {
			return nil
		}

		if !IsTransient(err) {
			return err
		}

		wait := l.backoff.NextBackOff()
		if wait == backoff.Stop {
			return err
		}

		fmt.Fprintf(os.Stderr, "listener reconnect: %s, retry in %v\n", HumanError(err), wait)

		timer.Reset(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (l *Listener) run(ctx context.Context) {
	defer close(l.exited)
	defer close(l.C)
	defer func() {
		if l.conn != nil {
			l.conn.Close(context.Background())
		}
	}()

	for {
		if ctx.Err() != nil {
			return
		}

		notif, err := l.conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			l.conn.Close(context.Background())
			l.conn = nil

			if err := l.reconnect(ctx); err != nil {
				if ctx.Err() == nil {
					fmt.Fprintf(os.Stderr, "listener gave up: %s\n", HumanError(err))
				}
				return
			}
			continue
		}

		select {
		case l.C <- notif:
		case <-ctx.Done():
			return
		}
	}
}
