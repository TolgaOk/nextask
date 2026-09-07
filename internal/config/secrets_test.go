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
		for _, key := range []string{"db.url", "DB.URL"} {
			if shared {
				key = "nextask." + key
			}
			for i, value := range []string{`"postgres://user:file-secret@host/db"`, `"postgres://host/db?password=file-secret"`, `42`, `false`, `["file-secret"]`} {
				t.Run(fmt.Sprintf("shared=%t/%s/value%d", shared, key, i), func(t *testing.T) {
					clearConfigEnv(t)
					t.Setenv("NEXTASK_DB_URL", "postgres://env:env-secret@host/db")
					file := configFixture(t, t.TempDir(), "config.toml", key+" = "+value)
					_, err := loadFiles([]configFile{{file, shared}})
					if err == nil || !strings.Contains(err.Error(), file) {
						t.Fatalf("unsafe config accepted: %v", err)
					}
					if strings.Contains(err.Error(), "file-secret") || strings.Contains(err.Error(), "env-secret") {
						t.Fatal("config error exposed a credential")
					}
				})
			}
		}
	}
}

func TestDatabaseURLTemplates(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("MY_PASSWORD", "some:p@ssword?&")
	for _, shared := range []bool{false, true} {
		key := "db.url"
		if shared {
			key = "nextask." + key
		}
		file := configFixture(t, t.TempDir(), "config.toml", key+` = "postgres://nextask:${MY_PASSWORD}@db:5432/nextask?sslmode=require"`)
		cfg, err := loadFiles([]configFile{{file, shared}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(cfg.DB.URL, "some%3Ap%40ssword%3F&") || !strings.Contains(cfg.DB.Endpoint, "${MY_PASSWORD}") {
			t.Fatal("resolved URL or template was lost")
		}
		var encoded bytes.Buffer
		if err := toml.NewEncoder(&encoded).Encode(cfg); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(encoded.String(), "some:") || strings.Contains(encoded.String(), "some%3A") || !strings.Contains(encoded.String(), "${MY_PASSWORD}") {
			t.Fatal("serialization exposed credentials or lost their reference")
		}
	}
	t.Setenv("MY_PASSWORD", "")
	file := configFixture(t, t.TempDir(), "config.toml", `db.url = "postgres://nextask:${MY_PASSWORD}@db/nextask"`)
	if _, err := LoadFrom(file); err == nil || !strings.Contains(err.Error(), "MY_PASSWORD") {
		t.Fatalf("missing variable was not named: %v", err)
	}
	t.Setenv("MY_URL", "postgres://nextask:secret@db/nextask")
	file = configFixture(t, t.TempDir(), "config.toml", `db.url = "${MY_URL}"`)
	cfg, err := LoadFrom(file)
	if err != nil || cfg.DB.URL != "postgres://nextask:secret@db/nextask" {
		t.Fatalf("whole URL reference failed: %v", err)
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
endpoint = "https://${S3_ACCESS_KEY}:${S3_SECRET_KEY}@fsn1.your-objectstorage.com"
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
		if strings.Contains(encoded.String(), "env-secret") {
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
	t.Setenv("NEXTASK_SOURCE_REMOTE", "https://user:alias-secret@host/repo.git")
	t.Setenv("NEXTASK_GIT_REMOTE", "https://user:remote-secret@host/repo.git")
	t.Setenv("NEXTASK_GIT_URL", "https://user:canonical-secret@host/repo.git")
	cfg, err := LoadFrom("/nonexistent/nextask-test.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Integrations["git"]["remote"] != "${NEXTASK_GIT_URL}" {
		t.Fatal("canonical environment URL did not win")
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(cfg); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"alias-secret", "remote-secret", "canonical-secret"} {
		if strings.Contains(encoded.String(), secret) {
			t.Fatal("environment credentials persisted in config")
		}
	}
}
