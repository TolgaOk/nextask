package db

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
)

// Sentinel errors for database operations.
var (
	ErrDBNotExist     = errors.New("database does not exist")
	ErrConnRefused    = errors.New("connection refused")
	ErrAuthFailed     = errors.New("authentication failed")
	ErrNotInitialized = errors.New("database not initialized")

	errConnLost     = errors.New("connection lost")
	errConnTimeout  = errors.New("connection timeout")
	errHostNotFound = errors.New("host not found")
)

// classifiedError keeps a safe diagnostic while retaining the underlying cause.
type classifiedError struct {
	kind  error
	cause error
}

func (e *classifiedError) Error() string        { return e.kind.Error() }
func (e *classifiedError) Is(target error) bool { return target == e.kind }
func (e *classifiedError) Unwrap() error        { return e.cause }

func wrapPgError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var kind error
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "3D000":
			kind = ErrDBNotExist
		case "28P01", "28000":
			kind = ErrAuthFailed
		case "42P01", "42703":
			kind = ErrNotInitialized
		}
	} else {
		kind = classifyNetworkError(err)
	}
	if kind == nil {
		return err
	}
	return &classifiedError{kind: kind, cause: err}
}

func classifyNetworkError(err error) error {
	switch {
	case errors.Is(err, ErrConnRefused), errors.Is(err, syscall.ECONNREFUSED):
		return ErrConnRefused
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return errConnLost
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errConnTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && (dnsErr.IsNotFound || dnsErr.IsTemporary) {
		return errHostNotFound
	}
	return nil
}

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

	var classified *classifiedError
	if errors.As(err, &classified) {
		return "DB " + classified.kind.Error()
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

	if kind := classifyNetworkError(err); kind != nil {
		return "DB " + kind.Error()
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
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

	return classifyNetworkError(err) != nil
}
