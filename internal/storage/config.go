// Package storage uploads selected task files to object storage.
package storage

import "time"

// Config contains non-secret upload settings. Endpoint retains environment
// references until the storage client is constructed on the worker.
type Config struct {
	Endpoint, Region, Bucket, Prefix, Root        string
	Include, Exclude, FinalInclude                []string
	Interval, FinalTimeout, UploadTimeout, MinAge time.Duration
	FinalSync                                     bool
	Concurrency, Retries                          int
	MaxFileSize                                   int64
	OnFinalError, Symlinks                        string
}
