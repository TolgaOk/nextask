package db

import (
	"time"

	"github.com/google/uuid"
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
	ID         uuid.UUID
	Command    string
	Status     TaskStatus
	SourceRef  *string
	InitType   *string
	InitConfig *string
	Labels     map[string]string
	Tags       map[string]string
	WorkerID   *string
	ExitCode   *int
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}
