package db

import (
	"context"
	"testing"

	"go.uber.org/goleak"
)

func TestConnectFailureClosesPool(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pool, err := Connect(ctx, "postgres://test@127.0.0.1:1/test?sslmode=disable")
	if pool != nil {
		pool.Close()
		t.Fatal("failed connection returned a pool")
	}
	if err == nil {
		t.Fatal("cancelled connection succeeded")
	}
}
