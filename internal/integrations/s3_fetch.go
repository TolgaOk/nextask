package integrations

import (
	"context"
	"fmt"
	"github.com/TolgaOk/nextask/internal/storage"
	"io"
)

// FetchS3 reuses connection settings without requiring any upload policy or DB.
func FetchS3(ctx context.Context, configured Options, taskID string, options storage.FetchOptions, out io.Writer) error {
	if err := options.Validate(); err != nil {
		return err
	}
	resolved, err := (S3{}).Options().Resolve(configured)
	if err != nil {
		return fmt.Errorf("s3: %w", err)
	}
	cfg, err := s3ConnectionConfig(resolved)
	if err != nil {
		return fmt.Errorf("s3: %w", err)
	}
	store, err := newS3Store(cfg)
	if err != nil {
		return err
	}
	return storage.Fetch(ctx, store, cfg.Prefix, taskID, options, out)
}
