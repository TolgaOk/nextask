package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"context deadline", context.DeadlineExceeded, false},
		{"generic error", errors.New("some error"), false},

		// PG connection errors (Class 08)
		{"pg connection exception", &pgconn.PgError{Code: "08000"}, true},
		{"pg connection failure", &pgconn.PgError{Code: "08006"}, true},

		// PG operator intervention (Class 57)
		{"pg admin shutdown", &pgconn.PgError{Code: "57P01"}, true},
		{"pg crash shutdown", &pgconn.PgError{Code: "57P02"}, true},

		// PG insufficient resources (Class 53)
		{"pg out of memory", &pgconn.PgError{Code: "53200"}, true},
		{"pg disk full", &pgconn.PgError{Code: "53100"}, true},

		// PG transaction rollback (Class 40)
		{"pg serialization failure", &pgconn.PgError{Code: "40001"}, true},
		{"pg deadlock", &pgconn.PgError{Code: "40P01"}, true},

		// PG non-transient errors
		{"pg syntax error", &pgconn.PgError{Code: "42601"}, false},
		{"pg undefined table", &pgconn.PgError{Code: "42P01"}, false},
		{"pg unique violation", &pgconn.PgError{Code: "23505"}, false},
		{"pg auth failed", &pgconn.PgError{Code: "28P01"}, false},

		// Network errors
		{"connection refused", errors.New("connection refused"), true},
		{"connection reset", errors.New("connection reset by peer"), true},
		{"broken pipe", errors.New("broken pipe"), true},
		{"timeout", errors.New("i/o timeout"), true},
		{"no such host", errors.New("no such host"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTransient(tt.err)
			if got != tt.want {
				t.Errorf("IsTransient(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRetry_Success(t *testing.T) {
	ctx := context.Background()
	calls := 0

	err := Retry(ctx, func() error {
		calls++
		return nil
	}, backoff.WithBackOff(NewBackOff(10*time.Millisecond, 100*time.Millisecond)))

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetry_TransientThenSuccess(t *testing.T) {
	ctx := context.Background()
	calls := 0
	transientErr := errors.New("connection refused")

	err := Retry(ctx, func() error {
		calls++
		if calls < 3 {
			return transientErr
		}
		return nil
	}, backoff.WithBackOff(NewBackOff(10*time.Millisecond, 100*time.Millisecond)))

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetry_PermanentError(t *testing.T) {
	ctx := context.Background()
	calls := 0
	permErr := errors.New("syntax error")

	err := Retry(ctx, func() error {
		calls++
		return permErr
	}, backoff.WithBackOff(NewBackOff(10*time.Millisecond, 100*time.Millisecond)))

	if !errors.Is(err, permErr) {
		t.Errorf("expected permanent error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry), got %d", calls)
	}
}

func TestRetry_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	transientErr := errors.New("connection refused")

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := Retry(ctx, func() error {
		calls++
		return transientErr
	}, backoff.WithBackOff(NewBackOff(10*time.Millisecond, 100*time.Millisecond)))

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if calls < 1 {
		t.Errorf("expected at least 1 call, got %d", calls)
	}
}

func TestRetry_MaxTries(t *testing.T) {
	ctx := context.Background()
	calls := 0
	transientErr := errors.New("connection refused")

	err := Retry(ctx, func() error {
		calls++
		return transientErr
	},
		backoff.WithBackOff(NewBackOff(10*time.Millisecond, 100*time.Millisecond)),
		backoff.WithMaxTries(3),
	)

	if err == nil {
		t.Error("expected error after max tries")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryValue_Success(t *testing.T) {
	ctx := context.Background()

	val, err := RetryValue(ctx, func() (int, error) {
		return 42, nil
	}, backoff.WithBackOff(NewBackOff(10*time.Millisecond, 100*time.Millisecond)))

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if val != 42 {
		t.Errorf("expected 42, got %d", val)
	}
}

func TestRetryValue_TransientThenSuccess(t *testing.T) {
	ctx := context.Background()
	calls := 0
	transientErr := errors.New("connection refused")

	val, err := RetryValue(ctx, func() (int, error) {
		calls++
		if calls < 2 {
			return 0, transientErr
		}
		return 42, nil
	}, backoff.WithBackOff(NewBackOff(10*time.Millisecond, 100*time.Millisecond)))

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if val != 42 {
		t.Errorf("expected 42, got %d", val)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}
