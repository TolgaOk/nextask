package config

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDatabaseConfigRejectedBeforeOverrides(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, key := range []string{"db.url", "nextask.db.url", "defaults.db_url", "DB.URL", "Nextask.DB.URL"} {
			for i, value := range []string{`"postgres://user:file-secret@host/db"`, `""`, `42`, `false`, `["file-secret"]`} {
				t.Run(fmt.Sprintf("shared=%t/%s/value%d", shared, key, i), func(t *testing.T) {
					clearConfigEnv(t)
					t.Setenv("NEXTASK_DB_URL", "postgres://env:env-secret@host/db")
					file := configFixture(t, t.TempDir(), "config.toml", key+" = "+value)
					_, err := loadFiles([]configFile{{file, shared}})
					if err == nil || !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "NEXTASK_DB_URL") {
						t.Fatalf("missing migration guidance: %v", err)
					}
					if strings.Contains(err.Error(), "file-secret") || strings.Contains(err.Error(), "env-secret") {
						t.Fatal("config error exposed a credential")
					}
				})
			}
		}
	}
}

func TestConfigRejectsSecretOptions(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for i, setting := range []string{
			`db.password = "file-secret"`,
			`credentials.token = "file-secret"`,
			`integrations.s3.access_key = "file-secret"`,
			`integrations.s3.secret_key = "file-secret"`,
			`integrations.s3.endpoint = "https://user:file-secret@host"`,
			`integrations.s3.remote = "s3://bucket/path?token=file-secret"`,
			`integrations.git.remote = "https://file-secret@host/repo.git"`,
			`integrations.git.remote = "https://user:file-secret@host/repo.git"`,
			`source.remote = "https://host/repo.git?token=file-secret"`,
		} {
			t.Run(fmt.Sprintf("shared=%t/setting%d", shared, i), func(t *testing.T) {
				clearConfigEnv(t)
				if shared {
					setting = "nextask." + setting
				}
				file := configFixture(t, t.TempDir(), "config.toml", setting)
				// A later file and environment cannot hide unsafe earlier values.
				override := configFixture(t, t.TempDir(), "override.toml", `[integrations.git]
remote = "origin"
`)
				t.Setenv("NEXTASK_GIT_REMOTE", "origin")
				_, err := loadFiles([]configFile{{file, shared}, {override, false}})
				if err == nil || !strings.Contains(err.Error(), file) {
					t.Fatalf("unsafe config accepted: %v", err)
				}
				if strings.Contains(err.Error(), "file-secret") {
					t.Fatal("config error echoed its secret")
				}
			})
		}
	}
}

func TestMalformedConfigDoesNotEchoValues(t *testing.T) {
	clearConfigEnv(t)
	file := configFixture(t, t.TempDir(), "config.toml", `db.url = "file-secret" file-secret`)
	_, err := LoadFrom(file)
	if err == nil || !strings.Contains(err.Error(), "line 1") || strings.Contains(err.Error(), "file-secret") {
		t.Fatalf("unsafe parse error: %v", err)
	}
}

func TestDatabaseEnvironmentAndSerialization(t *testing.T) {
	clearConfigEnv(t)
	file := configFixture(t, t.TempDir(), "config.toml", `[integrations.git]
remote = "ssh://git@host/repo.git"
[integrations.s3]
endpoint = "https://fsn1.your-objectstorage.com"
remote = "s3://bucket/project"
include = ["outputs/**"]
`)
	for _, value := range []string{"", "  ", "postgres://user:env-secret@host/db"} {
		t.Setenv("NEXTASK_DB_URL", value)
		cfg, err := LoadFrom(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(value) == "" {
			if cfg.DB.URL != "" {
				t.Fatal("blank connection was accepted")
			}
		} else if cfg.DB.URL != value || cfg.SourceFor("db.url") != "env:NEXTASK_DB_URL" {
			t.Fatal("environment connection or origin lost")
		}
		var encoded bytes.Buffer
		if err := toml.NewEncoder(&encoded).Encode(cfg); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(encoded.String(), "env-secret") || strings.Contains(encoded.String(), "[db]") {
			t.Fatal("serializing config wrote runtime credentials")
		}
	}
}

func TestIntegrationTableTypes(t *testing.T) {
	clearConfigEnv(t)
	for _, content := range []string{`integrations = "value"`, `integrations.s3 = 17`, `integrations.git = ["remote"]`} {
		file := configFixture(t, t.TempDir(), "config.toml", content)
		if _, err := LoadFrom(file); err == nil || !strings.Contains(err.Error(), "must be a table") {
			t.Fatalf("invalid table accepted: %v", err)
		}
	}
}

func TestEnvironmentRemoteCredentials(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("NEXTASK_SOURCE_REMOTE", "https://env-secret@host/repo.git")
	t.Setenv("NEXTASK_GIT_REMOTE", "origin")
	if _, err := LoadFrom("/nonexistent/nextask-test.toml"); err == nil || strings.Contains(err.Error(), "env-secret") {
		t.Fatalf("unsafe alias accepted or exposed: %v", err)
	}
}
