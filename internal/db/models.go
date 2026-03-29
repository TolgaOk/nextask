// Package db provides PostgreSQL persistence for tasks and logs.
package db

import (
	"encoding/json"
	"time"
)

// TaskStatus represents the execution state of a task.
type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
	StatusStale     TaskStatus = "stale"
)

// WorkerInfo contains metadata about the worker processing a task.
type WorkerInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	PID      int    `json:"pid"`
}

// Task represents a queued command with its execution state and metadata.
type Task struct {
	ID           string
	Command      string
	Status       TaskStatus
	SourceType   string
	SourceConfig json.RawMessage
	Tags         map[string]string
	WorkerID     *string
	WorkerInfo   *WorkerInfo
	ExitCode     *int
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

// TaskLog represents a captured stdout/stderr line from task execution.
type TaskLog struct {
	ID        int
	TaskID    string
	Seq       *int
	Stream    string
	Data      string
	CreatedAt time.Time
}

// WorkerStatus represents the state of a worker.
type WorkerStatus string

const (
	WorkerStatusRunning WorkerStatus = "running"
	WorkerStatusStopped WorkerStatus = "stopped"
)

// WorkerRecord represents a registered worker in the database.
type WorkerRecord struct {
	ID            string
	PID           int
	Hostname      string
	Workdir       string
	Status        WorkerStatus
	StartedAt     time.Time
	LastHeartbeat time.Time
	StoppedAt     *time.Time
}
