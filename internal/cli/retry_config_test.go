package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/db"
)

func TestWorkerUsesRetryConfigurationCLI(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `CREATE FUNCTION test_claim_outage() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'temporary claim outage' USING ERRCODE = '08006'; END $$;
		CREATE TRIGGER test_claim BEFORE UPDATE ON tasks
		FOR EACH ROW EXECUTE FUNCTION test_claim_outage()`)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DROP TRIGGER test_claim ON tasks; DROP FUNCTION test_claim_outage()")
	if err := db.CreateTask(ctx, pool, &db.Task{ID: "retry-config", Command: "true", Status: db.StatusPending, SourceType: "noop"}); err != nil {
		t.Fatal(err)
	}
	binary := buildTestCLI(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".nextask.toml"), []byte("[retry]\ninitial_interval='10ms'\nmax_interval='20ms'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "worker", "--timeout", "300ms", "--workdir", t.TempDir())
	cmd.Dir, cmd.Env = dir, append(isolatedCLIEnv(t), "NEXTASK_DB_URL="+getTestDBURL(t), "GORACE=atexit_sleep_ms=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("worker: %v\n%s", err, out)
	}
	matches := regexp.MustCompile(`retry in ([^)]+)`).FindAllStringSubmatch(string(out), -1)
	if len(matches) < 2 {
		t.Fatalf("configured fast retries not applied:\n%s", out)
	}
	for _, match := range matches {
		delay, err := time.ParseDuration(match[1])
		if err != nil || delay > 30*time.Millisecond || delay < 5*time.Millisecond {
			t.Errorf("retry delay %q outside configured jitter range: %v", match[1], err)
		}
	}
}
