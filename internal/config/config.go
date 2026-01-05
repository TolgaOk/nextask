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
	Workdir string `toml:"workdir"`
}

// Config holds the complete nextask configuration.
type Config struct {
	DB     DBConfig     `toml:"db"`
	Source SourceConfig `toml:"source"`
	Worker WorkerConfig `toml:"worker"`
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
}
