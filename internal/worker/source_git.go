package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nextask/nextask/internal/source"
)

type GitSourceConfig struct {
	Remote string `json:"remote"`
	Ref    string `json:"ref"`
	Commit string `json:"commit,omitempty"`
}

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

	log.Log("info", fmt.Sprintf("Fetching source from %s", cfg.Remote))

	commit, err := source.FetchSnapshot(cfg.Remote, cfg.Ref, taskDir)
	if err != nil {
		return err
	}

	log.Log("info", fmt.Sprintf("Checked out %s", commit))
	return nil
}
