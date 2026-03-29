// Package config handles configuration loading from files and environment variables.
//
// Configuration is loaded with the following precedence (highest wins):
//
//	CLI flags > env vars > .nextask.toml (project-local) > ~/.config/nextask/global.toml > defaults
//
// Config file format (same for both global and local):
//
//	[db]
//	url = "postgres://user@localhost:5432/nextask"
//
//	[source]
//	remote = "~/.nextask/source.git"
//
//	[worker]
//	workdir = "/tmp/nextask"
//
// Environment variables:
//   - NEXTASK_DB_URL
//   - NEXTASK_SOURCE_REMOTE
//   - NEXTASK_WORKER_WORKDIR
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// DBConfig holds database configuration.
type DBConfig struct {
	URL string `toml:"url"`
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

const DefaultLogFlushLines = 100
const DefaultLogFlushInterval = 500 * time.Millisecond
const DefaultLogBufferSize = 10000

// StaleDuration returns the duration after which a task is considered stale.
func (w WorkerConfig) StaleDuration() time.Duration {
	return w.HeartbeatInterval * time.Duration(w.StaleThreshold)
}

// Config holds the complete nextask configuration.
type Config struct {
	DB     DBConfig     `toml:"db"`
	Source SourceConfig `toml:"source"`
	Worker WorkerConfig `toml:"worker"`
	Retry  RetryConfig  `toml:"retry"`

	// LoadedFiles tracks which config files were loaded (not serialized to TOML).
	LoadedFiles []string `toml:"-"`
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

// Load reads configuration from the global and local config files, then applies
// env var overrides. Local config values override global values.
// Returns an empty Config if neither file exists.
func Load() (*Config, error) {
	cfg := &Config{}

	// Layer 1: global config
	globalPath, err := GlobalPath()
	if err == nil {
		if err := decodeIfExists(globalPath, cfg); err != nil {
			return nil, err
		}
	}

	// Layer 2: local config (overrides global)
	if err := decodeIfExists(LocalPath(), cfg); err != nil {
		return nil, err
	}

	// Layer 3: env vars (override both)
	applyEnv(cfg)
	return cfg, nil
}

// LoadFrom reads configuration from a specific file path and applies env var overrides.
// Returns an empty Config if the file doesn't exist.
func LoadFrom(path string) (*Config, error) {
	cfg := &Config{}

	if err := decodeIfExists(path, cfg); err != nil {
		return nil, err
	}

	applyEnv(cfg)
	return cfg, nil
}

// decodeIfExists decodes a TOML file into cfg if the file exists.
// It appends to cfg.LoadedFiles on success.
func decodeIfExists(path string, cfg *Config) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	cfg.LoadedFiles = append(cfg.LoadedFiles, path)
	return nil
}

// applyEnv overrides config values with environment variables if set.
func applyEnv(cfg *Config) {
	if v := os.Getenv("NEXTASK_DB_URL"); v != "" {
		cfg.DB.URL = v
	}
	if v := os.Getenv("NEXTASK_SOURCE_REMOTE"); v != "" {
		cfg.Source.Remote = v
	}
	if v := os.Getenv("NEXTASK_WORKER_WORKDIR"); v != "" {
		cfg.Worker.Workdir = v
	}
	// Apply defaults
	if cfg.Worker.HeartbeatInterval == 0 {
		cfg.Worker.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if cfg.Worker.StaleThreshold == 0 {
		cfg.Worker.StaleThreshold = DefaultStaleThreshold
	}
	if cfg.Worker.LogFlushLines == 0 {
		cfg.Worker.LogFlushLines = DefaultLogFlushLines
	}
	if cfg.Worker.LogFlushInterval == 0 {
		cfg.Worker.LogFlushInterval = DefaultLogFlushInterval
	}
	if cfg.Worker.LogBufferSize == 0 {
		cfg.Worker.LogBufferSize = DefaultLogBufferSize
	}
	if cfg.Retry.InitialInterval == 0 {
		cfg.Retry.InitialInterval = 500 * time.Millisecond
	}
	if cfg.Retry.MaxInterval == 0 {
		cfg.Retry.MaxInterval = 30 * time.Second
	}
	normalizePaths(cfg)
}

func normalizePaths(cfg *Config) {
	cfg.Source.Remote = NormalizeRemote(cfg.Source.Remote)
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
