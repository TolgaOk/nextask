package integrations

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/TolgaOk/nextask/internal/storage"
	"github.com/TolgaOk/nextask/internal/taskexec"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/dustin/go-humanize"
	"github.com/minio/minio-go/v7/pkg/s3utils"
)

// S3 prepares a runtime wrapper backed by an S3-compatible Go client.
type S3 struct{}

func (S3) Options() Schema {
	return Schema{
		"endpoint":       {String, ""},
		"region":         {String, ""},
		"remote":         {String, ""},
		"root":           {String, "."},
		"include":        {StringList, []string{}},
		"exclude":        {StringList, []string{}},
		"final_include":  {StringList, []string{}},
		"interval":       {String, "60s"},
		"final_sync":     {Boolean, true},
		"final_timeout":  {String, "2m"},
		"concurrency":    {Integer, int64(4)},
		"upload_timeout": {String, "5m"},
		"retries":        {Integer, int64(3)},
		"min_age":        {String, "0s"},
		"max_file_size":  {String, "unlimited"},
		"on_final_error": {String, "fail"},
		"symlinks":       {String, "skip"},
	}
}
func (S3) Validate(options Options) error { _, err := s3Config(options); return err }
func (S3) Prepare(_ context.Context, task Task, options Options) (Task, error) {
	cfg, err := s3Config(options)
	if err != nil {
		return Task{}, err
	}
	task.Command, err = runtimeCommand("s3", task, options)
	if cfg.FinalSync {
		task.CleanupTimeout += cfg.FinalTimeout
	}
	return task, err
}
func (S3) Run(ctx context.Context, task Task, options Options, streams IO) *taskexec.Result {
	cfg, err := s3Config(options)
	if err != nil {
		return &taskexec.Result{Code: 1, Err: err}
	}
	store, err := storage.NewS3(cfg)
	if err != nil {
		return &taskexec.Result{Code: 1, Err: err}
	}
	return storage.Run(ctx, cfg, store, task.ID, taskexec.Command{
		Text: task.Command, CleanupTimeout: task.CleanupTimeout,
		Stdin: streams.In, Stdout: streams.Out, Stderr: streams.Err,
	})
}

func s3Config(raw Options) (storage.Config, error) {
	o, err := (S3{}).Options().Resolve(raw)
	if err != nil {
		return storage.Config{}, err
	}
	c := storage.Config{
		Endpoint: o.String("endpoint"), Region: o.String("region"), Root: o.String("root"),
		Include: o["include"].([]string), Exclude: o["exclude"].([]string), FinalInclude: o["final_include"].([]string),
		FinalSync: o["final_sync"].(bool), OnFinalError: o.String("on_final_error"), Symlinks: o.String("symlinks"),
	}
	endpoint, err := url.Parse(c.Endpoint)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return c, fmt.Errorf("endpoint must be an http(s) service URL without credentials, query, or path")
	}
	remote, err := url.Parse(o.String("remote"))
	if err != nil || remote.Scheme != "s3" || remote.Hostname() == "" || remote.Port() != "" || remote.User != nil || remote.RawQuery != "" || remote.Fragment != "" {
		return c, fmt.Errorf("remote must be s3://bucket/base-prefix")
	}
	c.Bucket, c.Prefix = remote.Host, strings.Trim(remote.Path, "/")
	if err := s3utils.CheckValidBucketNameStrict(c.Bucket); err != nil {
		return c, fmt.Errorf("invalid destination bucket: %w", err)
	}
	for _, segment := range strings.Split(c.Prefix, "/") {
		if segment == "." || segment == ".." {
			return c, fmt.Errorf("remote prefix cannot contain . or .. segments")
		}
	}
	if strings.ContainsAny(c.Prefix+c.Region+c.Root, "\x00\r\n") {
		return c, fmt.Errorf("storage paths and region cannot contain control characters")
	}
	if c.Root == "" || path.IsAbs(c.Root) || strings.Contains(c.Root, "\\") {
		return c, fmt.Errorf("root must be relative to the task directory")
	}
	for _, segment := range strings.Split(c.Root, "/") {
		if segment == ".." || segment == ".git" || segment == ".nextask" {
			return c, fmt.Errorf("root must stay inside task data")
		}
	}
	c.Root = path.Clean(c.Root)
	for _, patterns := range [][]string{c.Include, c.Exclude, c.FinalInclude} {
		for _, pattern := range patterns {
			if pattern == "" || path.IsAbs(pattern) || strings.ContainsRune(pattern, 0) || !doublestar.ValidatePattern(pattern) {
				return c, fmt.Errorf("invalid relative file pattern %q", pattern)
			}
			for _, part := range strings.Split(pattern, "/") {
				if part == ".." {
					return c, fmt.Errorf("file patterns cannot traverse parent directories")
				}
			}
		}
	}
	if len(c.Include)+len(c.FinalInclude) == 0 {
		return c, fmt.Errorf("include or final_include must select files explicitly")
	}
	for _, setting := range []struct {
		key    string
		target *time.Duration
		zero   bool
	}{
		{"interval", &c.Interval, true}, {"final_timeout", &c.FinalTimeout, false}, {"upload_timeout", &c.UploadTimeout, false}, {"min_age", &c.MinAge, true},
	} {
		value, err := time.ParseDuration(o.String(setting.key))
		if err != nil || value < 0 || (!setting.zero && value == 0) || value > 24*time.Hour {
			return c, fmt.Errorf("%s must be a valid duration up to 24h", setting.key)
		}
		*setting.target = value
	}
	if !c.FinalSync && (c.Interval == 0 || len(c.Include) == 0) {
		return c, fmt.Errorf("at least one upload pass must be enabled with selected files")
	}
	concurrency, retries := o["concurrency"].(int64), o["retries"].(int64)
	if concurrency < 1 || concurrency > 256 {
		return c, fmt.Errorf("concurrency must be between 1 and 256")
	}
	if retries < 0 || retries > 100 {
		return c, fmt.Errorf("retries must be between 0 and 100")
	}
	c.Concurrency, c.Retries = int(concurrency), int(retries)
	if value := o.String("max_file_size"); value != "unlimited" {
		size, err := humanize.ParseBytes(value)
		if err != nil || size == 0 || size > math.MaxInt64 {
			return c, fmt.Errorf("max_file_size must be unlimited or a positive size such as 2GiB")
		}
		c.MaxFileSize = int64(size)
	}
	if c.OnFinalError != "fail" && c.OnFinalError != "warn" {
		return c, fmt.Errorf("on_final_error must be fail or warn")
	}
	if c.Symlinks != "skip" && c.Symlinks != "error" {
		return c, fmt.Errorf("symlinks must be skip or error")
	}
	return c, nil
}
