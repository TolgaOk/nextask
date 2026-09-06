// Package storage uploads selected task files to object storage.
package storage

import "time"

// Config contains resolved, non-secret upload settings.
type Config struct {
	Endpoint, Region, Bucket, Prefix, Root        string
	Include, Exclude, FinalInclude                []string
	Interval, FinalTimeout, UploadTimeout, MinAge time.Duration
	FinalSync                                     bool
	Concurrency, Retries                          int
	MaxFileSize                                   int64
	OnFinalError, Symlinks                        string
}
