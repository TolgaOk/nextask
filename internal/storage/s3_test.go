package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/storage/storagetest"
)

func TestS3Transport(t *testing.T) {
	server := storagetest.New()
	defer server.Close()
	cfg := testConfig()
	cfg.Endpoint, cfg.Bucket, cfg.Retries = strings.Replace(server.URL, "://", "://test-access:test-secret@", 1), "bucket", 1
	store, err := NewS3(cfg, cfg.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	data := []byte("artifact with content\n")
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	server.FailPuts(1)
	if err := store.Put(context.Background(), "task/file", bytes.NewReader(data), int64(len(data)), digest, "text/plain"); err != nil {
		t.Fatal(err)
	}
	object, err := store.Stat(context.Background(), "task/file")
	if err != nil {
		t.Fatal(err)
	}
	if object.SHA256 != digest || object.Size != int64(len(data)) {
		t.Fatalf("object metadata: %+v", object)
	}
	remote, ok := server.Object("bucket/task/file")
	if !ok || !bytes.Equal(remote.Data, data) {
		t.Fatalf("upload corrupt: %+v", remote)
	}
	if puts, attempts := server.Counts(); puts != 1 || attempts != 2 {
		t.Fatalf("retry counts: %d/%d", puts, attempts)
	}
	server.DelayPuts(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := store.Put(ctx, "task/timeout", bytes.NewReader(data), int64(len(data)), digest, "text/plain"); err == nil {
		t.Fatal("deadline ignored")
	}
	if _, ok := server.Object("bucket/task/timeout"); ok {
		t.Fatal("incomplete upload published")
	}
}

func TestS3TransportRejectsInvalidEndpoint(t *testing.T) {
	for _, value := range []string{"", "https://host", "https://access:secret@host%ZZ"} {
		if _, err := NewS3(testConfig(), value); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid transport endpoint accepted or exposed: %v", err)
		}
	}
}
