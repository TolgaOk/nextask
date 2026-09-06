package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/taskexec"
)

type storedFile struct {
	Object
	data []byte
}
type memoryStore struct {
	mu                 sync.Mutex
	files              map[string]storedFile
	puts, active, peak int
	delay              time.Duration
	fail               bool
}

func (s *memoryStore) Stat(_ context.Context, key string) (Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if file, ok := s.files[key]; ok {
		return file.Object, nil
	}
	return Object{}, ErrNotFound
}
func (s *memoryStore) Put(ctx context.Context, key string, reader io.Reader, size int64, digest, _ string) error {
	s.mu.Lock()
	s.active++
	if s.active > s.peak {
		s.peak = s.active
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.delay):
	}
	if s.fail {
		return errors.New("storage unavailable")
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("wrong upload size")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	s.files[key] = storedFile{Object{size, digest}, data}
	return nil
}
func testConfig() Config {
	return Config{Root: ".", Prefix: "project", Include: []string{"outputs/**"}, FinalInclude: []string{"reports/**"}, Exclude: []string{"**/*.tmp"}, Interval: time.Minute, FinalSync: true, FinalTimeout: time.Second, UploadTimeout: time.Second, Concurrency: 2, Symlinks: "skip", OnFinalError: "fail"}
}
func writeFile(t *testing.T, root, name, contents string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
func newTestSyncer(t *testing.T) (*Syncer, *memoryStore) {
	t.Helper()
	store := &memoryStore{files: map[string]storedFile{}}
	return &Syncer{Config: testConfig(), Store: store, TaskID: "task", TaskDir: t.TempDir(), Log: t.Logf}, store
}
func TestSyncSelectionChangesAndRetention(t *testing.T) {
	s, store := newTestSyncer(t)
	for name, value := range map[string]string{"outputs/latest.json": "one", "outputs/file with ' quotes": "data", "outputs/scratch.tmp": "temp", "reports/final.txt": "report", "source.txt": "source", "outputs/.git/config": "private", "outputs/.nextask/log/out.txt": "internal"} {
		writeFile(t, s.TaskDir, name, value)
	}
	external := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(external, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(s.TaskDir, "outputs/link")); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if store.puts != 2 {
		t.Fatalf("unexpected files: %v", store.files)
	}
	if err := s.Sync(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if store.puts != 2 {
		t.Fatal("unchanged files were uploaded")
	}
	file := filepath.Join(s.TaskDir, "outputs/latest.json")
	before, _ := os.Stat(file)
	writeFile(t, s.TaskDir, "outputs/latest.json", "two")
	if err := os.Chtimes(file, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if store.puts != 3 || string(store.files["project/task/outputs/latest.json"].data) != "two" {
		t.Fatal("same-size edit was missed")
	}
	s.Config.MinAge = time.Hour
	if err := s.Sync(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if store.puts != 4 || string(store.files["project/task/reports/final.txt"].data) != "report" {
		t.Fatal("final-only selection missing")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.files["project/task/outputs/latest.json"]; !ok {
		t.Fatal("remote artifact deleted")
	}
}
func TestSyncLimitsRootsAndLinks(t *testing.T) {
	s, store := newTestSyncer(t)
	writeFile(t, s.TaskDir, "outputs/small", "ok")
	writeFile(t, s.TaskDir, "outputs/large", "large")
	s.Config.MaxFileSize, s.Config.MinAge = 2, time.Hour
	if err := s.Sync(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if store.puts != 0 {
		t.Fatal("periodic limits ignored")
	}
	if err := s.Sync(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if store.puts != 1 {
		t.Fatal("final limits wrong")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(s.TaskDir, "link")); err != nil {
		t.Fatal(err)
	}
	s.Config.Root = "link"
	if err := s.Sync(context.Background(), true); err == nil {
		t.Fatal("symlink upload root accepted")
	}
	s.Config.Root = ".git"
	writeFile(t, s.TaskDir, ".git/config", "secret")
	if err := s.Sync(context.Background(), true); err == nil {
		t.Fatal("internal upload root accepted")
	}
	s.Config.Root, s.Config.Symlinks = ".", "error"
	if err := os.Symlink("small", filepath.Join(s.TaskDir, "outputs/link")); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background(), true); err == nil {
		t.Fatal("symlink policy ignored")
	}
}
func TestSyncConcurrencyAndDeadline(t *testing.T) {
	s, store := newTestSyncer(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		writeFile(t, s.TaskDir, "outputs/"+name, name)
	}
	store.delay = 20 * time.Millisecond
	if err := s.Sync(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if store.peak != 2 {
		t.Fatalf("concurrency = %d", store.peak)
	}
	store.delay = time.Hour
	s.Config.UploadTimeout = 20 * time.Millisecond
	writeFile(t, s.TaskDir, "outputs/a", "new")
	start := time.Now()
	if err := s.Sync(context.Background(), false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline = %v", err)
	}
	if time.Since(start) > time.Second || store.active != 0 {
		t.Fatal("upload outlived its deadline")
	}
}

func TestRunFinalizationAndExit(t *testing.T) {
	for _, tc := range []struct {
		name, command, policy string
		fail                  bool
		code                  int
	}{
		{"success", "mkdir -p outputs; echo done > outputs/file", "fail", false, 0},
		{"command-failure", "mkdir -p outputs; echo done > outputs/file; exit 17", "fail", false, 17},
		{"upload-failure", "mkdir -p outputs; echo done > outputs/file", "fail", true, 1},
		{"warning", "mkdir -p outputs; echo done > outputs/file", "warn", true, 0},
		{"both-fail", "mkdir -p outputs; echo done > outputs/file; exit 17", "fail", true, 17},
		{"background-child", "mkdir -p outputs; (sleep 0.1; echo done > outputs/file) &", "fail", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			c := testConfig()
			c.Interval = 0
			c.OnFinalError = tc.policy
			store := &memoryStore{files: map[string]storedFile{}, fail: tc.fail}
			var out, stderr bytes.Buffer
			result := Run(context.Background(), c, store, "task", taskexec.Command{Text: tc.command, Stdout: &out, Stderr: &stderr})
			if result.Code != tc.code {
				t.Fatalf("exit %d: %s", result.Code, stderr.String())
			}
			if !tc.fail && string(store.files["project/task/outputs/file"].data) != "done\n" {
				t.Fatalf("missing final file: %s", stderr.String())
			}
		})
	}
}

func TestRunFinalDeadline(t *testing.T) {
	t.Chdir(t.TempDir())
	c := testConfig()
	c.Interval = 0
	c.FinalTimeout = 20 * time.Millisecond
	c.UploadTimeout = time.Hour
	store := &memoryStore{files: map[string]storedFile{}, delay: time.Hour}
	var out, stderr bytes.Buffer
	start := time.Now()
	result := Run(context.Background(), c, store, "task", taskexec.Command{Text: "mkdir -p outputs; echo done > outputs/file", Stdout: &out, Stderr: &stderr})
	if result.Code != 1 || time.Since(start) > time.Second || store.active != 0 || store.puts != 0 {
		t.Fatalf("final upload exceeded its deadline: result=%v elapsed=%s log=%s", result, time.Since(start), &stderr)
	}
}
