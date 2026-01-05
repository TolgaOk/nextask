package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// BashInitConfig specifies a shell script to run for initialization.
type BashInitConfig struct {
	Script string `json:"script"`
}

// BashInitializer runs a shell script to set up the task environment.
type BashInitializer struct{}

func (BashInitializer) Type() string { return "bash" }

func (b BashInitializer) Run(ctx context.Context, rawConfig json.RawMessage, taskDir string, log Logger) error {
	var cfg BashInitConfig
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return fmt.Errorf("invalid bash init config: %w", err)
	}

	if cfg.Script == "" {
		return fmt.Errorf("bash init config: script is required")
	}

	log.Log("info", fmt.Sprintf("Running %s", cfg.Script))

	cmd := exec.CommandContext(ctx, "sh", cfg.Script)
	cmd.Dir = taskDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Log("stdout", scanner.Text())
		}
		done <- struct{}{}
	}()
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Log("stderr", scanner.Text())
		}
		done <- struct{}{}
	}()

	<-done
	<-done

	return cmd.Wait()
}
