package integrations

import (
	"context"
	"fmt"
	"math"
	"path"
	"strings"
	"time"

	"github.com/TolgaOk/nextask/internal/storage"
	"github.com/TolgaOk/nextask/internal/taskexec"
	"github.com/dustin/go-humanize"
)

// S3 prepares a runtime wrapper backed by an S3-compatible Go client.
type S3 struct{}

func (S3) Options() Schema {
	return Schema{
		"endpoint":       {Kind: String, Default: "", Check: checkS3Endpoint},
		"region":         {Kind: String, Default: ""},
		"remote":         {Kind: String, Default: "", Check: checkS3URL},
		"root":           {Kind: String, Default: "."},
		"include":        {Kind: StringList, Default: []string{}},
		"exclude":        {Kind: StringList, Default: []string{}},
		"final_include":  {Kind: StringList, Default: []string{}},
		"interval":       {Kind: String, Default: "60s"},
		"final_sync":     {Kind: Boolean, Default: true},
		"final_timeout":  {Kind: String, Default: "2m"},
		"concurrency":    {Kind: Integer, Default: int64(4)},
		"upload_timeout": {Kind: String, Default: "5m"},
		"retries":        {Kind: Integer, Default: int64(3)},
		"min_age":        {Kind: String, Default: "0s"},
		"max_file_size":  {Kind: String, Default: "unlimited"},
		"on_final_error": {Kind: String, Default: "fail"},
		"symlinks":       {Kind: String, Default: "skip"},
	}
}
func (s S3) RuntimeOptions() Schema { return s.Options() }

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
	store, err := newS3Store(cfg)
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
	c, err := s3ConnectionConfig(o)
	if err != nil {
		return c, err
	}
	c.Root = o.String("root")
	c.Include, c.Exclude, c.FinalInclude = o["include"].([]string), o["exclude"].([]string), o["final_include"].([]string)
	c.FinalSync, c.OnFinalError, c.Symlinks = o["final_sync"].(bool), o.String("on_final_error"), o.String("symlinks")
	if strings.ContainsAny(c.Root, "\x00\r\n") {
		return c, fmt.Errorf("storage paths cannot contain control characters")
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
	if err := storage.ValidatePatterns(c.Include, c.Exclude, c.FinalInclude); err != nil {
		return c, err
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
	concurrency := o["concurrency"].(int64)
	if concurrency < 1 || concurrency > 256 {
		return c, fmt.Errorf("concurrency must be between 1 and 256")
	}
	c.Concurrency = int(concurrency)
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
