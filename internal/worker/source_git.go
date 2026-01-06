package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/TolgaOk/nextask/internal/source"
)

// GitSourceConfig specifies parameters for fetching source from a git repository.
type GitSourceConfig struct {
	Remote string `json:"remote"`
	Ref    string `json:"ref"`
	Commit string `json:"commit,omitempty"`
}

// GitSource fetches source code from a git repository.
type GitSource struct{}

func (GitSource) Type() string { return "git" }

func (g GitSource) Fetch(ctx context.Context, rawConfig json.RawMessage, taskDir string, log Logger) error {
	var cfg GitSourceConfig
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return fmt.Errorf("invalid git source config: %w", err)
	}

	if cfg.Remote == "" {
		return fmt.Errorf("git source config: remote is required")
	}
	if cfg.Ref == "" {
		return fmt.Errorf("git source config: ref is required")
	}

	log.Log("nextask", fmt.Sprintf("[info] fetching source from %s", cfg.Remote))

	commit, err := source.FetchSnapshot(cfg.Remote, cfg.Ref, taskDir)
	if err != nil {
		return err
	}

	log.Log("nextask", fmt.Sprintf("[info] checked out %s", commit))
	return nil
}
