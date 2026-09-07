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
	t.Setenv("CUSTOM_ACCESS", "test-access")
	t.Setenv("CUSTOM_SECRET", "test-secret")
	server := storagetest.New()
	defer server.Close()
	cfg := testConfig()
	cfg.Endpoint, cfg.Bucket, cfg.Retries = strings.Replace(server.URL, "://", "://${CUSTOM_ACCESS}:${CUSTOM_SECRET}@", 1), "bucket", 1
	store, err := NewS3(cfg)
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
func TestS3RequiresWorkerCredentials(t *testing.T) {
	for _, tc := range []struct {
		access, secret string
		missing        []string
	}{
		{"", "", []string{"CUSTOM_ACCESS"}},
		{"test-access", "", []string{"CUSTOM_SECRET"}},
		{"", "test-secret", []string{"CUSTOM_ACCESS"}},
		{" ", "test-secret", []string{"CUSTOM_ACCESS"}},
	} {
		t.Setenv("CUSTOM_ACCESS", tc.access)
		t.Setenv("CUSTOM_SECRET", tc.secret)
		cfg := testConfig()
		cfg.Endpoint = "https://${CUSTOM_ACCESS}:${CUSTOM_SECRET}@storage.invalid"
		_, err := NewS3(cfg)
		if err == nil {
			t.Fatal("missing credentials accepted")
		}
		for _, name := range []string{"CUSTOM_ACCESS", "CUSTOM_SECRET"} {
			want := false
			for _, missing := range tc.missing {
				want = want || name == missing
			}
			if strings.Contains(err.Error(), name) != want {
				t.Fatalf("incorrect missing variable diagnostic: %v", err)
			}
		}
		if strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "test-access") {
			t.Fatal("credential value exposed in error")
		}
	}
}
