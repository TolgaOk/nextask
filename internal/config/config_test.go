package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFrom_FileNotExist(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := LoadFrom("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.DB.URL != "" {
		t.Errorf("expected empty DB.URL, got %q", cfg.DB.URL)
	}
}

func TestLoadFrom_ValidConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[source]
remote = "/path/to/remote.git"

[worker]
workdir = "/custom/workdir"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.Source.Remote != "/path/to/remote.git" {
		t.Errorf("expected Source.Remote = %q, got %q", "/path/to/remote.git", cfg.Source.Remote)
	}
	if cfg.Worker.Workdir != "/custom/workdir" {
		t.Errorf("expected Worker.Workdir = %q, got %q", "/custom/workdir", cfg.Worker.Workdir)
	}
}

func TestLoadFrom_EnvOverrides(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[source]
remote = "/file/remote.git"

[worker]
workdir = "/file/workdir"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Set env vars
	t.Setenv("NEXTASK_DB_URL", "postgres://env@localhost/envdb")
	t.Setenv("NEXTASK_SOURCE_REMOTE", "/env/remote.git")
	t.Setenv("NEXTASK_WORKER_WORKDIR", "/env/workdir")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Env should override file
	if cfg.DB.URL != "postgres://env@localhost/envdb" {
		t.Errorf("expected DB.URL = %q, got %q", "postgres://env@localhost/envdb", cfg.DB.URL)
	}
	if cfg.Source.Remote != "${NEXTASK_SOURCE_REMOTE}" {
		t.Errorf("expected Source.Remote = %q, got %q", "/env/remote.git", cfg.Source.Remote)
	}
	if cfg.Worker.Workdir != "/env/workdir" {
		t.Errorf("expected Worker.Workdir = %q, got %q", "/env/workdir", cfg.Worker.Workdir)
	}
}

func TestLoadFrom_EnvWithNoFile(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("NEXTASK_DB_URL", "postgres://env@localhost/envdb")

	cfg, err := LoadFrom("/nonexistent/config.toml")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.DB.URL != "postgres://env@localhost/envdb" {
		t.Errorf("expected DB.URL = %q, got %q", "postgres://env@localhost/envdb", cfg.DB.URL)
	}
}

func TestLoadFrom_PartialConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `[worker]
log_flush_lines = 12
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.Worker.LogFlushLines != 12 {
		t.Fatal("partial config setting was lost")
	}
	if cfg.Source.Remote != "" {
		t.Errorf("expected empty Source.Remote, got %q", cfg.Source.Remote)
	}
	if cfg.Worker.Workdir != DefaultWorkdir {
		t.Errorf("expected default Worker.Workdir, got %q", cfg.Worker.Workdir)
	}
}

func TestLoadFrom_InvalidTOML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `invalid toml [[[`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestGlobalPath(t *testing.T) {
	clearConfigEnv(t)
	path, err := GlobalPath()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".config", "nextask", "global.toml")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestToAbsPath(t *testing.T) {
	clearConfigEnv(t)
	home, _ := os.UserHomeDir()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"absolute", "/absolute/path", "/absolute/path"},
		{"tilde", "~/some/path", filepath.Join(home, "some/path")},
		{"tilde only", "~/", home},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToAbsPath(tt.input)
			if result != tt.expected {
				t.Errorf("ToAbsPath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDecodeFile_TracksLoadedFiles(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte("[worker]\nlog_flush_lines = 12\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	if err := decodeTestFile(path, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.LoadedFiles) != 1 || cfg.LoadedFiles[0] != path {
		t.Errorf("expected LoadedFiles = [%q], got %v", path, cfg.LoadedFiles)
	}
}

func TestDecodeFile_SkipsMissing(t *testing.T) {
	clearConfigEnv(t)
	cfg := &Config{}
	if err := decodeTestFile("/nonexistent/file.toml", cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.LoadedFiles) != 0 {
		t.Errorf("expected no LoadedFiles, got %v", cfg.LoadedFiles)
	}
}

func TestLocalOverridesGlobal(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()

	globalPath := filepath.Join(dir, "global.toml")
	globalContent := `
[source]
remote = "/global/remote.git"

[worker]
workdir = "/global/workdir"
`
	if err := os.WriteFile(globalPath, []byte(globalContent), 0644); err != nil {
		t.Fatal(err)
	}

	localPath := filepath.Join(dir, ".nextask.toml")
	localContent := `
[worker]
log_flush_lines = 12
`
	if err := os.WriteFile(localPath, []byte(localContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate layered loading: global then local
	cfg := &Config{}
	if err := decodeTestFile(globalPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := decodeTestFile(localPath, cfg); err != nil {
		t.Fatal(err)
	}
	applyEnv(cfg)

	// Local overrides a non-secret setting.
	if cfg.Worker.LogFlushLines != 12 {
		t.Fatal("local log setting did not win")
	}
	// Global values preserved where local doesn't override
	if cfg.Source.Remote != "/global/remote.git" {
		t.Errorf("expected global Source.Remote, got %q", cfg.Source.Remote)
	}
	if cfg.Worker.Workdir != "/global/workdir" {
		t.Errorf("expected global Worker.Workdir, got %q", cfg.Worker.Workdir)
	}
	// Both files tracked
	if len(cfg.LoadedFiles) != 2 {
		t.Errorf("expected 2 LoadedFiles, got %d", len(cfg.LoadedFiles))
	}
}

func TestLocalOnly_NoGlobal(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()

	localPath := filepath.Join(dir, ".nextask.toml")
	localContent := `
[worker]
log_flush_lines = 12
`
	if err := os.WriteFile(localPath, []byte(localContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	// Global doesn't exist — no error
	if err := decodeTestFile(filepath.Join(dir, "global.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	if err := decodeTestFile(localPath, cfg); err != nil {
		t.Fatal(err)
	}
	applyEnv(cfg)

	if cfg.Worker.LogFlushLines != 12 {
		t.Fatal("local setting was lost")
	}
	if len(cfg.LoadedFiles) != 1 {
		t.Errorf("expected 1 LoadedFile, got %d", len(cfg.LoadedFiles))
	}
}

func TestEnvOverridesLocal(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()

	localPath := filepath.Join(dir, ".nextask.toml")
	if err := os.WriteFile(localPath, []byte("[worker]\nlog_flush_lines = 12\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NEXTASK_DB_URL", "postgres://env@localhost/envdb")

	cfg := &Config{}
	if err := decodeTestFile(localPath, cfg); err != nil {
		t.Fatal(err)
	}
	applyEnv(cfg)

	if cfg.DB.Endpoint != "${NEXTASK_DB_URL}" {
		t.Errorf("expected environment reference, got %q", cfg.DB.Endpoint)
	}
}

func TestInvalidLocalTOML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, ".nextask.toml")

	if err := os.WriteFile(path, []byte(`invalid [[[`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	err := decodeTestFile(path, cfg)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestLoadFrom_TildeExpansion(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[source]
remote = "~/.nextask/source.git"

[worker]
workdir = "~/nextask-work"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	home, _ := os.UserHomeDir()

	expectedRemote := filepath.Join(home, ".nextask/source.git")
	if cfg.Source.Remote != expectedRemote {
		t.Errorf("Source.Remote = %q, want %q", cfg.Source.Remote, expectedRemote)
	}

	expectedWorkdir := filepath.Join(home, "nextask-work")
	if cfg.Worker.Workdir != expectedWorkdir {
		t.Errorf("Worker.Workdir = %q, want %q", cfg.Worker.Workdir, expectedWorkdir)
	}
}

func decodeTestFile(path string, cfg *Config) error {
	return decodeFile(configFile{path: path}, cfg)
}
