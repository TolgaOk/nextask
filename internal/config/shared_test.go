package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"NEXTASK_DB_URL", "NEXTASK_SOURCE_REMOTE", "NEXTASK_GIT_REMOTE", "NEXTASK_GIT_URL", "NEXTASK_S3_ENDPOINT", "NEXTASK_WORKER_WORKDIR"} {
		t.Setenv(name, "")
	}
}

func configFixture(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSharedPrecedence(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	files := []configFile{
		{configFixture(t, dir, "user-shared.toml", `[nextask.worker]
workdir = "/shared-user"
heartbeat_interval = "2m"
[nextask.source]
remote = "origin"
[gsnap]
db_url = 17
`), true},
		{configFixture(t, dir, "user-nextask.toml", `[worker]
workdir = "/standalone-user"
log_flush_lines = 12
`), false},
		{configFixture(t, dir, "project-shared.toml", `[nextask.worker]
workdir = "/shared-project"
log_buffer_size = 20
`), true},
		{configFixture(t, dir, "project-nextask.toml", `[worker]
workdir = "/standalone-project"
`), false},
	}
	for i, want := range []string{"/shared-user", "/standalone-user", "/shared-project", "/standalone-project"} {
		c, err := loadFiles(files[:i+1])
		if err != nil {
			t.Fatal(err)
		}
		if c.Worker.Workdir != want || !strings.HasPrefix(c.SourceFor("worker.workdir"), files[i].path+" [") {
			t.Fatalf("layer %d: incorrect precedence or origin", i)
		}
		if c.Worker.HeartbeatInterval != 2*time.Minute || c.Source.Remote != "origin" {
			t.Fatal("partial layer lost inherited settings")
		}
	}
	t.Setenv("NEXTASK_DB_URL", "postgres://environment/db")
	t.Setenv("NEXTASK_WORKER_WORKDIR", "/environment")
	c, err := loadFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	if c.DB.URL != "postgres://environment/db" || c.SourceFor("db.url") != "env:NEXTASK_DB_URL" || c.Worker.Workdir != "/environment" {
		t.Fatal("environment value or origin lost")
	}
	if c.Worker.LogFlushLines != 12 || c.SourceFor("worker.log_flush_lines") != files[1].path+" [worker.log_flush_lines]" {
		t.Fatal("standalone setting lost")
	}
	if c.SourceFor("worker.log_flush_interval") != "default" {
		t.Fatal("default source missing")
	}
	wantFiles := []string{files[0].path, files[1].path, files[2].path, files[3].path}
	if !reflect.DeepEqual(c.LoadedFiles, wantFiles) {
		t.Fatalf("loaded files = %v", c.LoadedFiles)
	}
}

func TestSharedOptionalAndInvalid(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.toml")
	c, err := loadFiles([]configFile{{missing, true}})
	if err != nil || len(c.LoadedFiles) != 0 || c.Worker.Workdir != DefaultWorkdir {
		t.Fatalf("missing shared config: %+v, %v", c, err)
	}
	for _, contents := range []string{
		"invalid = [[[", "defaults = 2", "nextask = 2",
		"[defaults]\ndb_url = 42", "[nextask.db]\nurl = false",
		"[nextask.worker]\nheartbeat_interval = 'not a duration'",
		"[nextask.worker]\nlog_buffer_size = -1",
		"[nextask.retry]\ninitial_interval = '2s'\nmax_interval = '1s'",
	} {
		path := configFixture(t, dir, "invalid.toml", contents)
		if _, err := loadFiles([]configFile{{path, true}}); err == nil {
			t.Errorf("invalid settings accepted: %q", contents)
		}
	}
	path := configFixture(t, dir, "other-tool.toml", "[gsnap]\ndb_url = 42\n[taskfiles]\nbackend = []")
	if _, err := loadFiles([]configFile{{path, true}}); err != nil {
		t.Fatalf("another tool's schema was interpreted: %v", err)
	}
}

func TestSharedDefaultsAndExplicitEmpty(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	global := configFixture(t, dir, "global.toml", "[source]\nremote = 'origin'")
	shared := configFixture(t, dir, "shared.toml", "[nextask.source]\nremote = ''\n[nextask.worker]\nlog_flush_lines = 0")
	c, err := loadFiles([]configFile{{global, false}, {shared, true}})
	if err != nil {
		t.Fatal(err)
	}
	if c.DB.URL != "" || c.Source.Remote != "" {
		t.Fatal("explicit empty settings did not override")
	}
	if c.Worker.LogFlushLines != DefaultLogFlushLines || c.SourceFor("worker.log_flush_lines") != "default" {
		t.Fatal("zero should preserve default selection")
	}
}

func TestSharedGlobalPath(t *testing.T) {
	path, err := SharedGlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "tasktools", "config.toml"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestCredentialRedaction(t *testing.T) {
	for _, tc := range []struct {
		input   string
		db      bool
		visible string
	}{
		{"postgres://alice:password-secret@localhost/db?password=query-secret&sslmode=require", true, "localhost/db"},
		{"postgres://alice:p%40ss-secret@localhost/db", true, "localhost/db"},
		{"host=localhost password='quoted secret' user=alice", true, "[redacted connection string]"},
		{"postgres://alice:password-secret@localhost/db?bad=%ZZ&token=query-secret", true, "localhost/db"},
		{"postgres://alice:password-secret@host%ZZ/db", true, "[redacted URL]"},
		{"https://token-secret@host/repo?access_token=query-secret#fragment-secret", false, "host/repo"},
		{"git@host:repo.git", false, "git@host:repo.git"},
		{"/tmp/source.git", false, "/tmp/source.git"},
	} {
		output := redactURL(tc.input, tc.db)
		for _, secret := range []string{"alice", "password-secret", "p%40ss-secret", "query-secret", "quoted secret", "token-secret", "fragment-secret"} {
			if strings.Contains(output, secret) {
				t.Errorf("credential %q leaked in %q", secret, output)
			}
		}
		if !strings.Contains(output, tc.visible) {
			t.Errorf("diagnostic lost expected text %q: %q", tc.visible, output)
		}
	}
	c := &Config{DB: DBConfig{URL: "postgres://user:password-secret@host/db"}}
	_ = c.Settings()
	if c.DB.URL != "postgres://user:password-secret@host/db" {
		t.Fatal("display redaction changed the actual connection")
	}
}
