package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDatabaseErrorCauses(t *testing.T) {
	for _, tc := range []struct {
		code string
		kind error
	}{
		{"3D000", ErrDBNotExist},
		{"28P01", ErrAuthFailed},
		{"28000", ErrAuthFailed},
		{"42P01", ErrNotInitialized},
		{"42703", ErrNotInitialized},
	} {
		t.Run(tc.code, func(t *testing.T) {
			cause := &pgconn.PgError{Code: tc.code, Message: "private-query-value"}
			err := wrapPgError(fmt.Errorf("connect: %w", cause))
			var got *pgconn.PgError
			if !errors.Is(err, tc.kind) || !errors.As(err, &got) || got != cause {
				t.Fatalf("lost error identity: %v", err)
			}
			if err.Error() != tc.kind.Error() || strings.Contains(HumanError(err), cause.Message) {
				t.Fatalf("unsafe diagnostic: %v", err)
			}
		})
	}
}

func TestNetworkErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name               string
		cause              error
		message            string
		refused, transient bool
	}{
		{"refused", syscall.ECONNREFUSED, "DB connection refused", true, true},
		{"reset", syscall.ECONNRESET, "DB connection lost", false, true},
		{"pipe", syscall.EPIPE, "DB connection lost", false, true},
		{"timeout", os.ErrDeadlineExceeded, "DB connection timeout", false, true},
		{"dns", &net.DNSError{IsNotFound: true}, "DB host not found", false, true},
		{"cancelled", context.Canceled, "operation cancelled", false, false},
		{"deadline", context.DeadlineExceeded, "operation timed out", false, false},
		{"permission", syscall.EACCES, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := &net.OpError{Op: "dial", Net: "tcp", Err: tc.cause}
			err := wrapPgError(fmt.Errorf("connect: %w", cause))
			var got *net.OpError
			if !errors.As(err, &got) || got != cause || !errors.Is(err, tc.cause) {
				t.Fatalf("lost network cause: %v", err)
			}
			if errors.Is(err, ErrConnRefused) != tc.refused || IsTransient(err) != tc.transient {
				t.Fatalf("wrong classification: %v", err)
			}
			if tc.message != "" && HumanError(err) != tc.message {
				t.Fatalf("diagnostic = %q, want %q", HumanError(err), tc.message)
			}
		})
	}
}

type failedTaskRow struct{ err error }

func (r failedTaskRow) Scan(...any) error { return r.err }

func TestScanTaskErrorIdentity(t *testing.T) {
	wrapped := fmt.Errorf("query: %w", pgx.ErrNoRows)
	if task, err := scanTask(failedTaskRow{wrapped}); task != nil || err != nil {
		t.Fatalf("wrapped missing row = %v, %v", task, err)
	}
	impostor := errors.New(pgx.ErrNoRows.Error())
	if _, err := scanTask(failedTaskRow{impostor}); !errors.Is(err, impostor) {
		t.Fatalf("discarded unrelated error: %v", err)
	}
}
