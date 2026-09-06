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
	merged := mergeIntegrationOptions(map[string]map[string]any{"tool": {"one": "kept", "two": "old"}}, map[string]map[string]any{"tool": {"two": "new"}})
	if merged["tool"]["one"] != "kept" || merged["tool"]["two"] != "new" {
		t.Fatalf("partial override lost values: %v", merged)
	}
}

func TestSharedConfigCannotEnableIntegrations(t *testing.T) {
	clearConfigEnv(t)
	path := configFixture(t, t.TempDir(), "shared.toml", "[nextask.enqueue]\nwith=['git']\n")
	if _, err := loadFiles([]configFile{{path, true}}); err == nil {
		t.Fatal("shared config enabled an integration")
	}
}

func TestTypedIntegrationLayers(t *testing.T) {
	clearConfigEnv(t)
	user := configFixture(t, t.TempDir(), "user.toml", `[integrations.s3]
endpoint = "https://fsn1.your-objectstorage.com"
remote = "s3://bucket/project"
include = ["old/**"]
concurrency = 2
final_sync = false
`)
	shared := configFixture(t, t.TempDir(), "shared.toml", `[nextask.integrations.s3]
include = ["new/**", "report.json"]
interval = "30s"
`)
	cfg, err := loadFiles([]configFile{{user, false}, {shared, true}})
	if err != nil {
		t.Fatal(err)
	}
	o := cfg.Integrations["s3"]
	if o["concurrency"] != int64(2) || o["final_sync"] != false || o["interval"] != "30s" || len(o["include"].([]string)) != 2 || o["include"].([]string)[0] != "new/**" {
		t.Fatalf("wrong merge: %v", o)
	}
	if cfg.SourceFor("integrations.s3.include") != shared+" [nextask.integrations.s3.include]" {
		t.Fatal("lost override source")
	}
}
