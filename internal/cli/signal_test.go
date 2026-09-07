package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestInterruptContextCleanup(t *testing.T) {
	for _, mode := range []string{"completion", "parent-cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "deadline" {
				var deadlineCancel context.CancelFunc
				parent, deadlineCancel = context.WithTimeout(parent, 10*time.Millisecond)
				defer deadlineCancel()
			}
			ctx, stop := interruptContext(parent)
			defer stop()
			want := context.Canceled
			switch mode {
			case "completion":
				stop()
			case "parent-cancel":
				cancel()
			case "deadline":
				want = context.DeadlineExceeded
			}
			select {
			case <-ctx.Done():
				if !errors.Is(ctx.Err(), want) {
					t.Fatalf("context error = %v, want %v", ctx.Err(), want)
				}
			case <-time.After(time.Second):
				t.Fatal("interrupt context did not close")
			}
		})
	}
}
