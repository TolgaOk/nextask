package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIntegrationConfigLayers(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("NEXTASK_GIT_REMOTE", "")
	dir := t.TempDir()
	user := configFixture(t, dir, "user.toml", `[enqueue]
with = ["git"]
[integrations.git]
remote = "user-remote"
`)
	shared := configFixture(t, dir, "shared.toml", `[nextask.integrations.git]
remote = "project-remote"
`)
	local := configFixture(t, dir, "local.toml", `[enqueue]
with = []
`)
	cfg, err := loadFiles([]configFile{{user, false}, {shared, true}, {local, false}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Enqueue.With) != 0 {
		t.Fatal("explicit empty list did not replace default list")
	}
	if cfg.Integrations["git"]["remote"] != "project-remote" {
		t.Fatalf("lost shared override: %v", cfg.Integrations)
	}
	if cfg.SourceFor("enqueue.with") != local+" [enqueue.with]" || cfg.SourceFor("integrations.git.remote") != shared+" [nextask.integrations.git.remote]" {
		t.Fatal("incorrect setting origins")
	}
	if err := os.WriteFile(local, []byte("[source]\nremote = 'legacy-project'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadFiles([]configFile{{user, false}, {shared, true}, {local, false}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Enqueue.With, []string{"git"}) || cfg.Integrations["git"]["remote"] != "legacy-project" {
		t.Fatal("legacy alias lost layer precedence")
	}
	t.Setenv("NEXTASK_SOURCE_REMOTE", "legacy-env")
	t.Setenv("NEXTASK_GIT_REMOTE", "canonical-env")
	cfg, err = loadFiles([]configFile{{user, false}, {shared, true}, {local, false}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Integrations["git"]["remote"] != "canonical-env" || cfg.SourceFor("integrations.git.remote") != "env:NEXTASK_GIT_REMOTE" {
		t.Fatal("environment override failed")
	}
}

func TestIntegrationConfigMalformed(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("NEXTASK_GIT_REMOTE", "")
	for _, contents := range []string{"[enqueue]\nwith='git'", "[integrations.git]\nremote=42"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFrom(path); err == nil {
			t.Errorf("malformed config accepted: %s", contents)
		}
	}
}

func TestIntegrationOptionsMergeByKey(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("NEXTASK_GIT_REMOTE", "")
	dir := t.TempDir()
	user := configFixture(t, dir, "user.toml", `[integrations.git]
remote = "global"
future_option = "old"
`)
	project := configFixture(t, dir, "project.toml", `[nextask.integrations.git]
future_option = "new"
`)
	cfg, err := loadFiles([]configFile{{user, false}, {project, true}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Integrations["git"]["remote"] != "global" || cfg.Integrations["git"]["future_option"] != "new" {
		t.Fatalf("partial override lost values: %v", cfg.Integrations)
	}
}
