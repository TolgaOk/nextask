package db

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// HumanError returns a human-readable error message.
func HumanError(err error) string {
	if err == nil {
		return ""
	}

	// Context errors
	if errors.Is(err, context.Canceled) {
		return "operation cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "operation timed out"
	}

	// PostgreSQL error codes
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code := pgErr.Code
		switch {
		case strings.HasPrefix(code, "08"):
			return "DB connection lost"
		case strings.HasPrefix(code, "57"):
			return "DB unavailable (restarting)"
		case strings.HasPrefix(code, "53"):
			return "DB resource exhausted"
		case strings.HasPrefix(code, "40"):
			return "DB transaction conflict"
		case strings.HasPrefix(code, "23"):
			return "DB constraint violation"
		case strings.HasPrefix(code, "42"):
			return "DB query error"
		default:
			return pgErr.Message
		}
	}

	// Network-level errors - simplify
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "connection refused") {
		return "DB connection refused"
	}
	if strings.Contains(s, "connection reset") || strings.Contains(s, "broken pipe") {
		return "DB connection lost"
	}
	if strings.Contains(s, "timeout") {
		return "DB connection timeout"
	}
	if strings.Contains(s, "no such host") {
		return "DB host not found"
	}
	if strings.Contains(s, "failed to connect") {
		return "DB connection failed"
	}

	// Fallback - return first line only
	if idx := strings.Index(err.Error(), "\n"); idx > 0 {
		return err.Error()[:idx]
	}
	return err.Error()
}

// IsTransient returns true if the error is temporary and should be retried.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}

	// Context errors are not transient - caller wants to stop
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// PostgreSQL error codes
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code := pgErr.Code
		switch {
		// Class 08 - Connection Exception
		case strings.HasPrefix(code, "08"):
			return true
		// Class 57 - Operator Intervention (db restart, crash recovery)
		case strings.HasPrefix(code, "57"):
			return true
		// Class 53 - Insufficient Resources (temporary)
		case strings.HasPrefix(code, "53"):
			return true
		// 40001 - serialization_failure, 40P01 - deadlock_detected
		case strings.HasPrefix(code, "40"):
			return true
		}
		// All other PG errors are not transient
		return false
	}

	// Network-level errors
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "no such host")
}

// NewBackOff creates an exponential backoff with the given initial and max intervals.
func NewBackOff(initial, max time.Duration) *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = initial
	b.MaxInterval = max
	b.RandomizationFactor = 0.5
	b.Multiplier = 2.0
	return b
}

// Retry executes fn, retrying on transient errors.
// Non-transient errors stop retrying immediately.
func Retry(ctx context.Context, fn func() error, opts ...backoff.RetryOption) error {
	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		err := fn()
		if err != nil && !IsTransient(err) {
			return struct{}{}, backoff.Permanent(err)
		}
		return struct{}{}, err
	}, opts...)
	return err
}

// RetryValue executes fn, retrying on transient errors.
// Returns the value from fn on success.
func RetryValue[T any](ctx context.Context, fn func() (T, error), opts ...backoff.RetryOption) (T, error) {
	return backoff.Retry(ctx, func() (T, error) {
		val, err := fn()
		if err != nil && !IsTransient(err) {
			return val, backoff.Permanent(err)
		}
		return val, err
	}, opts...)
}
