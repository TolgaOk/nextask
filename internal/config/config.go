// Package config handles configuration loading from files and environment variables.
//
// Configuration is loaded from ~/.config/nextask/global.toml with the following format:
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
// Environment variables override config file values:
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
}

// GlobalPath returns the path to the global config file.
func GlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nextask", "global.toml"), nil
}

// Load reads configuration from the global config file and applies env var overrides.
// Returns an empty Config if the file doesn't exist.
func Load() (*Config, error) {
	path, err := GlobalPath()
	if err != nil {
		return &Config{}, nil
	}
	return LoadFrom(path)
}

// LoadFrom reads configuration from a specific file path and applies env var overrides.
// Returns an empty Config if the file doesn't exist.
func LoadFrom(path string) (*Config, error) {
	cfg := &Config{}

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		applyEnv(cfg)
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	applyEnv(cfg)
	return cfg, nil
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
