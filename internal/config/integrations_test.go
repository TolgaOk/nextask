package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationConfigLayers(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("NEXTASK_GIT_REMOTE", "")
	dir := t.TempDir()
	user := configFixture(t, dir, "user.toml", `[integrations.git]
remote = "user-remote"
`)
	shared := configFixture(t, dir, "shared.toml", `[nextask.integrations.git]
remote = "project-remote"
`)
	local := configFixture(t, dir, "local.toml", `[worker]
workdir = "/tmp/nextask"
`)
	cfg, err := loadFiles([]configFile{{user, false}, {shared, true}, {local, false}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Integrations["git"]["remote"] != "project-remote" {
		t.Fatalf("lost shared override: %v", cfg.Integrations)
	}
	if cfg.SourceFor("integrations.git.remote") != shared+" [nextask.integrations.git.remote]" {
		t.Fatal("incorrect setting origins")
	}
	if err := os.WriteFile(local, []byte("[source]\nremote = 'legacy-project'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadFiles([]configFile{{user, false}, {shared, true}, {local, false}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Integrations["git"]["remote"] != "legacy-project" {
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
	for _, contents := range []string{"[enqueue]\nwith='git'", "[enqueue]\nwith=['git']", "[integrations.git]\nremote=42"} {
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

func TestSharedConfigCannotEnableIntegrations(t *testing.T) {
	clearConfigEnv(t)
	path := configFixture(t, t.TempDir(), "shared.toml", "[nextask.enqueue]\nwith=['git']\n")
	if _, err := loadFiles([]configFile{{path, true}}); err == nil {
		t.Fatal("shared config enabled an integration")
	}
}
