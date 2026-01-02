package db

import (
	"time"
)

type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
)

type Task struct {
	ID           string
	Command      string
	Status       TaskStatus
	SourceRemote *string
	SourceRef    *string
	SourceCommit *string
	Tags         map[string]string
	WorkerID     *string
	ExitCode     *int
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

type TaskLog struct {
	ID        int
	TaskID    string
	Stream    string
	Data      string
	CreatedAt time.Time
}
