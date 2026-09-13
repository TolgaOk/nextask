package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/storage"
	"github.com/TolgaOk/nextask/internal/storage/storagetest"
)

func TestS3FetchCLIWithoutDatabase(t *testing.T) {
	for _, key := range []string{"NEXTASK_DB_URL", "NEXTASK_SOURCE_REMOTE", "NEXTASK_GIT_URL", "NEXTASK_GIT_REMOTE", "NEXTASK_S3_ENDPOINT", "FETCH_DB_PASSWORD"} {
		t.Setenv(key, "")
	}
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("FETCH_ACCESS", "test-access")
	t.Setenv("FETCH_SECRET", "test-secret")
	server := storagetest.New()
	defer server.Close()
	store, err := storage.NewS3(storage.Config{Bucket: "bucket", Region: "fsn1", Retries: 1}, strings.Replace(server.URL, "://", "://test-access:test-secret@", 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"outputs/result.txt", "reports/final.txt", "comma,a.txt", "other.txt"} {
		if err := store.Put(context.Background(), "prefix/source/"+name, strings.NewReader(name), int64(len(name)), "", "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	contents := fmt.Sprintf(`[db]
url = "postgres://user:${FETCH_DB_PASSWORD}@127.0.0.1:1/unused"
[integrations.s3]
endpoint = %q
region = "fsn1"
remote = "s3://bucket/prefix"
include = ["never-download-this-pattern"]
exclude = ["**"]
`, strings.Replace(server.URL, "://", "://${FETCH_ACCESS}:${FETCH_SECRET}@", 1))
	if err := os.WriteFile(config.LocalFileName, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		cmd := NewRootCommand("test")
		cmd.SetArgs(args)
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		return out.String(), err
	}
	dest := filepath.Join(t.TempDir(), "fetched")
	out, err := run("s3", "fetch", "source", "--to", dest, "--dry-run", "--include", "outputs/**", "--include", "reports/**", "--exclude", "reports/**")
	if err != nil || out != "outputs/result.txt\n" {
		t.Fatalf("dry-run: %q %v", out, err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("dry-run created destination")
	}
	out, err = run("s3", "fetch", "source", "--to", dest)
	if err != nil || out != "comma,a.txt\nother.txt\noutputs/result.txt\nreports/final.txt\n" {
		t.Fatalf("download: %q %v", out, err)
	}
	for _, name := range []string{"outputs/result.txt", "reports/final.txt"} {
		b, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(b) != name {
			t.Fatalf("file: %q %v", b, err)
		}
	}
	if _, err := run("s3", "fetch", "source", "--to", dest); err == nil || !strings.Contains(err.Error(), "--overwrite") {
		t.Fatalf("conflict: %v", err)
	}
	if _, err := run("s3", "fetch", "source", "--to", dest, "--overwrite", "--include", "comma,a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("s3", "fetch", "missing", "--to", dest); err == nil || !strings.Contains(err.Error(), "no artifacts") {
		t.Fatalf("missing: %v", err)
	}
	if _, err := run("list"); err == nil || !strings.Contains(err.Error(), "FETCH_DB_PASSWORD") {
		t.Fatalf("DB command stopped requiring its secrets: %v", err)
	}
	t.Setenv("FETCH_SECRET", "")
	if _, err := run("s3", "fetch", "source", "--to", dest); err == nil || !strings.Contains(err.Error(), "FETCH_SECRET") {
		t.Fatalf("missing S3 secret: %v", err)
	}
	t.Setenv("FETCH_SECRET", "test-secret")
	t.Setenv("FETCH_ACCESS", "invalid-access")
	if _, err := run("s3", "fetch", "source", "--to", dest); err == nil {
		t.Fatal("access denial accepted")
	}
}

func TestS3FetchArgumentsBeforeConfig(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"id"},
		{"id", "extra", "--to", "dir"},
		{"../escape", "--to", "dir"},
		{"id", "--to", "dir", "--include", "["},
		{"id", "--to", "dir", "--timeout", "0s"},
		{"id", "--to", "dir", "--timeout", "-1s"},
		{"id", "--to", "dir", "--timeout", "2d"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			loaded := false
			cmd := newRootCommand("test", func() (*config.Config, error) { loaded = true; return &config.Config{}, nil })
			cmd.SetArgs(append([]string{"s3", "fetch"}, args...))
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); err == nil || loaded {
				t.Fatalf("invalid arguments reached config: %v loaded=%t", err, loaded)
			}
		})
	}
}
