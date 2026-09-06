// Package config handles configuration loading from files and environment variables.
//
// Configuration is loaded with the following precedence (highest wins):
//
//	CLI flags > env vars > project files > user files > defaults
//
// Within each scope, the Nextask file overrides [nextask] in the shared tasktools
// file. Other tools' sections are ignored. Database connections come only from
// NEXTASK_DB_URL.
//
// Config file format (same for both global and local):
//
//	[integrations.git]
//	remote = "~/.nextask/source.git"
//
//	[worker]
//	workdir = "/tmp/nextask"
//
// Environment variables:
//   - NEXTASK_DB_URL
//   - NEXTASK_GIT_REMOTE (NEXTASK_SOURCE_REMOTE remains an alias)
//   - NEXTASK_WORKER_WORKDIR
package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// DBConfig holds the database connection supplied by the environment.
type DBConfig struct {
	URL string `toml:"-"`
}

// SourceConfig holds source snapshotting configuration.
type SourceConfig struct {
	Remote string `toml:"remote"`
}

// WorkerConfig holds worker configuration.
type WorkerConfig struct {
	Workdir           string        `toml:"workdir"`
	HeartbeatInterval time.Duration `toml:"heartbeat_interval"`
	StaleThreshold    int           `toml:"stale_threshold"`
	LogFlushLines     int           `toml:"log_flush_lines"`
	LogFlushInterval  time.Duration `toml:"log_flush_interval"`
	LogBufferSize     int           `toml:"log_buffer_size"`
}

// RetryConfig holds retry/backoff configuration for DB operations.
type RetryConfig struct {
	InitialInterval time.Duration `toml:"initial_interval"`
	MaxInterval     time.Duration `toml:"max_interval"`
}

// DefaultHeartbeatInterval is the default heartbeat interval if not configured.
const DefaultHeartbeatInterval = 1 * time.Minute

// DefaultStaleThreshold is the number of missed heartbeats before a task is marked stale.
const DefaultStaleThreshold = 3

const DefaultWorkdir = "/tmp/nextask"
const DefaultLogFlushLines = 100
const DefaultLogFlushInterval = 500 * time.Millisecond
const DefaultLogBufferSize = 10000

// StaleDuration returns the duration after which a task is considered stale.
func (w WorkerConfig) StaleDuration() time.Duration {
	return w.HeartbeatInterval * time.Duration(w.StaleThreshold)
}

// Config holds the complete nextask configuration.
type Config struct {
	DB           DBConfig                  `toml:"-"`
	Source       SourceConfig              `toml:"source"`
	Worker       WorkerConfig              `toml:"worker"`
	Retry        RetryConfig               `toml:"retry"`
	Integrations map[string]map[string]any `toml:"integrations"`

	// LoadedFiles tracks which config files were loaded (not serialized to TOML).
	LoadedFiles []string `toml:"-"`
	sources     map[string]string
}

// LocalFileName is the name of the per-project config file.
const LocalFileName = ".nextask.toml"

// GlobalPath returns the path to the global config file.
func GlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nextask", "global.toml"), nil
}

// LocalPath returns the path to the per-project config file in the current directory.
func LocalPath() string {
	return LocalFileName
}

// SharedLocalFileName is the optional shared project config file.
const SharedLocalFileName = ".tasktools.toml"

// SharedGlobalPath returns the optional shared user config path.
func SharedGlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tasktools", "config.toml"), nil
}

type configFile struct {
	path   string
	shared bool
}

// Load layers shared and standalone user/project files, then environment values.
func Load() (*Config, error) {
	global, _ := GlobalPath()
	shared, _ := SharedGlobalPath()
	return loadFiles([]configFile{
		{shared, true}, {global, false},
		{SharedLocalFileName, true}, {LocalPath(), false},
	})
}

func loadFiles(files []configFile) (*Config, error) {
	cfg := &Config{}
	for _, file := range files {
		if file.path == "" {
			continue
		}
		if err := decodeFile(file, cfg); err != nil {
			return nil, err
		}
	}
	applyEnv(cfg)
	var err error
	cfg.Integrations, err = cfg.resolveIntegrations()
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadFrom reads one standalone Nextask file, then applies environment and defaults.
func LoadFrom(path string) (*Config, error) {
	return loadFiles([]configFile{{path, false}})
}

// applyEnv overrides config values with environment variables if set.
func applyEnv(cfg *Config) {
	if v := os.Getenv("NEXTASK_DB_URL"); strings.TrimSpace(v) != "" {
		cfg.DB.URL = v
		cfg.setSource("db.url", "env:NEXTASK_DB_URL")
	}
	if v := os.Getenv("NEXTASK_SOURCE_REMOTE"); v != "" {
		cfg.Source.Remote = v
		cfg.setSource("source.remote", "env:NEXTASK_SOURCE_REMOTE")
		cfg.setGitRemote(v, "env:NEXTASK_SOURCE_REMOTE")
	}
	if v := os.Getenv("NEXTASK_GIT_REMOTE"); v != "" {
		cfg.setGitRemote(v, "env:NEXTASK_GIT_REMOTE")
	}
	if v := os.Getenv("NEXTASK_WORKER_WORKDIR"); v != "" {
		cfg.Worker.Workdir = v
		cfg.setSource("worker.workdir", "env:NEXTASK_WORKER_WORKDIR")
	}
	// Preserve the existing convention that zero values select defaults.
	if cfg.Worker.Workdir == "" {
		cfg.Worker.Workdir = DefaultWorkdir
		cfg.setSource("worker.workdir", "default")
	}
	if cfg.Worker.HeartbeatInterval == 0 {
		cfg.Worker.HeartbeatInterval = DefaultHeartbeatInterval
		cfg.setSource("worker.heartbeat_interval", "default")
	}
	if cfg.Worker.StaleThreshold == 0 {
		cfg.Worker.StaleThreshold = DefaultStaleThreshold
		cfg.setSource("worker.stale_threshold", "default")
	}
	if cfg.Worker.LogFlushLines == 0 {
		cfg.Worker.LogFlushLines = DefaultLogFlushLines
		cfg.setSource("worker.log_flush_lines", "default")
	}
	if cfg.Worker.LogFlushInterval == 0 {
		cfg.Worker.LogFlushInterval = DefaultLogFlushInterval
		cfg.setSource("worker.log_flush_interval", "default")
	}
	if cfg.Worker.LogBufferSize == 0 {
		cfg.Worker.LogBufferSize = DefaultLogBufferSize
		cfg.setSource("worker.log_buffer_size", "default")
	}
	if cfg.Retry.InitialInterval == 0 {
		cfg.Retry.InitialInterval = 500 * time.Millisecond
		cfg.setSource("retry.initial_interval", "default")
	}
	if cfg.Retry.MaxInterval == 0 {
		cfg.Retry.MaxInterval = 30 * time.Second
		cfg.setSource("retry.max_interval", "default")
	}
	normalizePaths(cfg)
}

func normalizePaths(cfg *Config) {
	cfg.Source.Remote = NormalizeRemote(cfg.Source.Remote)
	if git := cfg.Integrations["git"]; git != nil {
		if remote, ok := git["remote"]; ok {
			if value, ok := remote.(string); ok {
				git["remote"] = NormalizeRemote(value)
			}
		}
	}
	cfg.Worker.Workdir = ToAbsPath(cfg.Worker.Workdir)
}

// isGitURL returns true if s looks like a git remote URL (SSH or protocol://).
func isGitURL(s string) bool {
	// HTTPS, git://, ssh:// protocols
	if strings.Contains(s, "://") {
		return true
	}
	// SCP-like SSH syntax: user@host:path
	atIdx := strings.IndexByte(s, '@')
	colonIdx := strings.IndexByte(s, ':')
	if atIdx >= 0 && colonIdx > atIdx {
		return true
	}
	return false
}

// NormalizeRemote normalizes a git remote value.
// Local paths (starting with / ~ . or ..) get expanded; URLs and remote names pass through.
func NormalizeRemote(remote string) string {
	if remote == "" {
		return remote
	}
	if isGitURL(remote) {
		return remote
	}
	// Only normalize values that look like filesystem paths
	if remote[0] == '/' || remote[0] == '~' || remote[0] == '.' {
		return ToAbsPath(remote)
	}
	// Bare name like "origin" — pass through for git to resolve
	return remote
}

// ToAbsPath expands ~ and converts to absolute path.
func ToAbsPath(path string) string {
	if path == "" {
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func (cfg *Config) setGitRemote(value, source string) {
	if cfg.Integrations == nil {
		cfg.Integrations = make(map[string]map[string]any)
	}
	if cfg.Integrations["git"] == nil {
		cfg.Integrations["git"] = make(map[string]any)
	}
	cfg.Integrations["git"]["remote"] = value
	cfg.setSource("integrations.git.remote", source)
}

// Apply deprecated source.remote at its original layer, unless the same file
// supplies the canonical integration setting.
func (cfg *Config) applySourceAlias(meta toml.MetaData, path string, prefix []string) {
	oldKey := append(append([]string{}, prefix...), "source", "remote")
	newKey := append(append([]string{}, prefix...), "integrations", "git", "remote")
	if meta.IsDefined(oldKey...) && !meta.IsDefined(newKey...) {
		cfg.setGitRemote(cfg.Source.Remote, path+" ["+strings.Join(oldKey, ".")+"]")
	}
}

func mergeIntegrationOptions(base, overlay map[string]map[string]any) map[string]map[string]any {
	if base == nil {
		base = make(map[string]map[string]any)
	}
	for name, options := range overlay {
		if base[name] == nil {
			base[name] = make(map[string]any)
		}
		for key, value := range options {
			base[name][key] = value
		}
	}
	return base
}
