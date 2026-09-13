package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWorkerListStatusCLI(t *testing.T) {
	pool := setupTestDB(t)
	binary := buildTestCLI(t)
	dir := t.TempDir()
	env := append(isolatedCLIEnv(t), "NEXTASK_DB_URL="+getTestDBURL(t), "GORACE=atexit_sleep_ms=0")
	ctx := context.Background()
	now := time.Now().UTC()
	var allIDs []string
	statuses := map[string]string{}
	for _, group := range []struct {
		status string
		count  int
	}{{"stale", 7}, {"running", 6}, {"stopped", 2}} {
		for i := 0; i < group.count; i++ {
			id := fmt.Sprintf("%s-%d", group.status, i)
			stored := group.status
			heartbeat := now.Add(-10 * time.Minute)
			if stored == "stale" {
				stored = "running"
			} else if stored == "running" {
				heartbeat = now.Add(time.Hour)
				if i == 0 {
					heartbeat = now.Add(-2 * time.Minute)
				}
			}
			_, err := pool.Exec(ctx, `INSERT INTO workers
				(id, pid, hostname, workdir, status, started_at, last_heartbeat)
				VALUES ($1, 1234, 'testhost', '/tmp/test', $2, $3, $4)`,
				id, stored, now.Add(-time.Duration(len(allIDs))*time.Minute), heartbeat)
			if err != nil {
				t.Fatal(err)
			}
			allIDs = append(allIDs, id)
			statuses[id] = group.status
		}
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(binary, append([]string{"worker", "list"}, args...)...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	checkJSON := func(t *testing.T, want []string, args ...string) {
		t.Helper()
		out, err := run(append(args, "--json")...)
		if err != nil {
			t.Fatalf("worker list %v: %v\n%s", args, err, out)
		}
		var rows []map[string]string
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row["id"])
			if row["status"] != statuses[row["id"]] {
				t.Errorf("%s status = %s, want %s", row["id"], row["status"], statuses[row["id"]])
			}
		}
		if !reflect.DeepEqual(ids, want) {
			t.Errorf("IDs = %v, want %v", ids, want)
		}
	}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"all", nil, allIDs},
		{"multiple", []string{"--status", "running,stopped"}, allIDs[7:]},
		{"repeated", []string{"--status", "stale", "--status", "running"}, allIDs[:13]},
		{"running-limit", []string{"--status", "running", "--limit", "5"}, allIDs[7:12]},
		{"running-offset", []string{"--status", "running", "--limit", "5", "--offset", "5"}, allIDs[12:13]},
		{"stale-limit", []string{"--status", "stale", "--limit", "5"}, allIDs[:5]},
		{"stale-offset", []string{"--status", "stale", "--limit", "5", "--offset", "5"}, allIDs[5:7]},
		{"stopped", []string{"--status", "stopped"}, allIDs[13:]},
		{"since", []string{"--status", "running", "--since", "10m"}, allIDs[7:10]},
		{"empty", []string{"--status", "running", "--offset", "6"}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) { checkJSON(t, tc.want, tc.args...) })
	}
	t.Run("table-count", func(t *testing.T) {
		out, err := run("--status", "running", "--limit", "5")
		if err != nil || !strings.Contains(out, "5/6 (") || strings.Contains(out, "stale") {
			t.Fatalf("wrong filtered table/count: %v\n%s", err, out)
		}
	})
	t.Run("multiple-count", func(t *testing.T) {
		out, err := run("--status", "running,stopped", "--limit", "5")
		if err != nil || !strings.Contains(out, "5/8 (") || strings.Contains(out, "stale") {
			t.Fatalf("wrong combined count: %v\n%s", err, out)
		}
	})
	t.Run("csv", func(t *testing.T) {
		out, err := run("--status", "stale", "--csv", "--limit", "5")
		if err != nil {
			t.Fatalf("stale CSV: %v\n%s", err, out)
		}
		rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
		if err != nil || len(rows) != 6 {
			t.Fatalf("CSV rows: %v\n%s", err, out)
		}
		for i, row := range rows[1:] {
			if row[0] != allIDs[i] || row[3] != "stale" {
				t.Errorf("unexpected CSV row: %v", row)
			}
		}
	})
	t.Run("invalid-status", func(t *testing.T) {
		out, err := run("--status", "missing")
		if err == nil || !strings.Contains(out, "unknown status: missing") || !strings.Contains(out, "stale") {
			t.Fatalf("missing status diagnostic: %v\n%s", err, out)
		}
	})
	t.Run("help", func(t *testing.T) {
		out, err := run("--help")
		if err != nil || !strings.Contains(out, "running, stopped, stale") {
			t.Fatalf("missing stale in help: %v\n%s", err, out)
		}
	})
	t.Run("configured-threshold", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(dir, ".nextask.toml"), []byte("[worker]\nheartbeat_interval = '20s'\nstale_threshold = 3\n"), 0600); err != nil {
			t.Fatal(err)
		}
		statuses["running-0"] = "stale"
		checkJSON(t, allIDs[:8], "--status", "stale")
		checkJSON(t, allIDs[8:13], "--status", "running")
	})
}
