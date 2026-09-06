package db

import (
	"context"
	"fmt"
	"time"

	"github.com/TolgaOk/nextask/internal/db/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TaskCompletion records a terminal outcome for one particular task claim.
// The claim timestamps come from the database; FinishedAt is fixed before retrying.
type TaskCompletion struct {
	TaskID     string     `json:"task_id"`
	WorkerID   string     `json:"worker_id"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt time.Time  `json:"finished_at"`
	Status     TaskStatus `json:"status"`
	ExitCode   int        `json:"exit_code"`
}

func (c TaskCompletion) Validate() error {
	if err := ValidateTaskID(c.TaskID); err != nil {
		return err
	}
	if c.WorkerID == "" || c.CreatedAt.IsZero() || c.StartedAt.IsZero() || c.FinishedAt.IsZero() {
		return fmt.Errorf("completion requires the original worker and task timestamps")
	}
	switch c.Status {
	case StatusCompleted:
		if c.ExitCode != 0 {
			return fmt.Errorf("completed task must have exit code zero")
		}
	case StatusFailed, StatusCancelled:
		if c.ExitCode == 0 {
			return fmt.Errorf("failed or cancelled task must have a nonzero exit code")
		}
	default:
		return fmt.Errorf("completion requires a terminal task status")
	}
	return nil
}

// CompleteClaim applies an outcome only to its original claim. It also confirms
// an identical outcome already committed, without updating its timestamp again.
// False means the task was deleted, replaced, or finished with a different result.
func CompleteClaim(ctx context.Context, pool *pgxpool.Pool, c TaskCompletion) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
	}
	sql, err := migrations.FS.ReadFile("complete_claim.sql")
	if err != nil {
		return false, err
	}
	var confirmed bool
	err = pool.QueryRow(ctx, string(sql), c.TaskID, c.WorkerID, c.CreatedAt, c.StartedAt,
		c.Status, c.ExitCode, c.FinishedAt).Scan(&confirmed)
	return confirmed, err
}
