package db

import (
	"encoding/json"
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

type WorkerInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	PID      int    `json:"pid"`
}

type Task struct {
	ID           string
	Command      string
	Status       TaskStatus
	SourceType   string
	SourceConfig json.RawMessage
	InitType     string
	InitConfig   json.RawMessage
	Tags         map[string]string
	WorkerID     *string
	WorkerInfo   *WorkerInfo
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
