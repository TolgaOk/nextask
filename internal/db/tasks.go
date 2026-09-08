package db

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed queries/claim_task.sql
var claimTaskSQL string

//go:embed queries/complete_task.sql
var completeTaskSQL string

//go:embed queries/get_task.sql
var getTaskSQL string

//go:embed queries/request_cancel.sql
var requestCancelSQL string

//go:embed queries/delete_task.sql
var deleteTaskSQL string

// CreateTask inserts a new task into the queue.
func CreateTask(ctx context.Context, pool Execer, task *Task) error {
	if err := ValidateTaskID(task.ID); err != nil {
		return err
	}
	tagsJSON, err := json.Marshal(task.Tags)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO tasks (id, command, status, tags, source_type, source_config, execution_command, cleanup_timeout_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, task.ID, task.Command, task.Status, tagsJSON,
		task.SourceType, task.SourceConfig, task.ExecutionCommand, task.CleanupTimeoutMS)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "tasks_pkey" {
		return fmt.Errorf("%w: %q", ErrTaskExists, task.ID)
	}
	return wrapPgError(err)
}

// SetTaskExecution stores the prepared command and cleanup deadline. Enqueue
// calls it in the transaction that reserved the task ID, before publishing it.
func SetTaskExecution(ctx context.Context, exec Execer, taskID, command string, cleanup time.Duration) error {
	_, err := exec.Exec(ctx, `UPDATE tasks
		SET execution_command = $2, cleanup_timeout_ms = $3, source_type = 'command'
		WHERE id = $1`, taskID, command, cleanup.Milliseconds())
	return err
}

// ListFilter specifies criteria for filtering tasks.
type ListFilter struct {
	Statuses       []string
	Tags           map[string]string
	Commands       []string
	Since          time.Time
	Limit          uint64
	Offset         uint64
	StaleThreshold time.Duration
}

func staleStatusExpr(staleThreshold time.Duration) string {
	staleInterval := fmt.Sprintf("%d seconds", int(staleThreshold.Seconds()))
	return fmt.Sprintf(
		"CASE WHEN t.status = 'running' AND w.last_heartbeat < NOW() - '%s'::interval THEN 'stale' ELSE t.status END",
		staleInterval,
	)
}

func applyTaskFilters(query sq.SelectBuilder, filter ListFilter, statusExpr string) (sq.SelectBuilder, error) {
	if len(filter.Statuses) > 0 {
		query = query.Where(sq.Eq{statusExpr: filter.Statuses})
	}
	if len(filter.Tags) > 0 {
		tagsJSON, err := json.Marshal(filter.Tags)
		if err != nil {
			return query, err
		}
		query = query.Where("t.tags @> ?::jsonb", tagsJSON)
	}
	if len(filter.Commands) > 0 {
		or := sq.Or{}
		for _, cmd := range filter.Commands {
			or = append(or, sq.ILike{"t.command": "%" + cmd + "%"})
		}
		query = query.Where(or)
	}
	if !filter.Since.IsZero() {
		query = query.Where(sq.GtOrEq{"t.created_at": filter.Since})
	}
	return query, nil
}

// ListTasks retrieves tasks matching the given filter criteria.
func ListTasks(ctx context.Context, pool *pgxpool.Pool, filter ListFilter) ([]Task, error) {
	statusExpr := staleStatusExpr(filter.StaleThreshold)

	query := psql.Select("t.id", "t.command", statusExpr, "t.tags", "t.created_at").
		From("tasks t").
		LeftJoin("workers w ON t.worker_id = w.id").
		OrderBy("t.created_at DESC")

	query, err := applyTaskFilters(query, filter, statusExpr)
	if err != nil {
		return nil, err
	}

	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}

	sql, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrapPgError(err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var tagsJSON []byte
		if err := rows.Scan(&t.ID, &t.Command, &t.Status, &tagsJSON, &t.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tagsJSON, &t.Tags); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}

	return tasks, rows.Err()
}

// CountTasks returns the total count of tasks matching the filter (ignoring Limit/Offset).
func CountTasks(ctx context.Context, pool *pgxpool.Pool, filter ListFilter) (int, error) {
	statusExpr := staleStatusExpr(filter.StaleThreshold)

	query := psql.Select("COUNT(*)").
		From("tasks t").
		LeftJoin("workers w ON t.worker_id = w.id")

	query, err := applyTaskFilters(query, filter, statusExpr)
	if err != nil {
		return 0, err
	}

	sql, args, err := query.ToSql()
	if err != nil {
		return 0, err
	}
	var count int
	err = pool.QueryRow(ctx, sql, args...).Scan(&count)
	return count, err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanTask(row scannable) (*Task, error) {
	var t Task
	var tagsJSON, wiJSON []byte
	err := row.Scan(
		&t.ID, &t.Command, &t.ExecutionCommand, &t.CleanupTimeoutMS, &t.Status,
		&t.SourceType, &t.SourceConfig,
		&tagsJSON, &t.WorkerID, &wiJSON, &t.ExitCode,
		&t.CreatedAt, &t.StartedAt, &t.FinishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(tagsJSON, &t.Tags); err != nil {
		return nil, err
	}
	if len(wiJSON) > 0 {
		if err := json.Unmarshal(wiJSON, &t.WorkerInfo); err != nil {
			return nil, err
		}
	}
	return &t, nil
}

// ClaimTask atomically claims the next pending task for a worker.
// If tagFilter is non-empty, only tasks matching all specified tags are claimed.
func ClaimTask(ctx context.Context, pool *pgxpool.Pool, workerID string, workerInfo *WorkerInfo, tagFilter map[string]string) (*Task, error) {
	workerInfoJSON, err := json.Marshal(workerInfo)
	if err != nil {
		return nil, err
	}

	var tagFilterJSON []byte
	if len(tagFilter) > 0 {
		tagFilterJSON, err = json.Marshal(tagFilter)
		if err != nil {
			return nil, err
		}
	}

	row := pool.QueryRow(ctx, claimTaskSQL, StatusRunning, workerID, workerInfoJSON, tagFilterJSON)
	return scanTask(row)
}

// CompleteTask marks a task as completed or failed with its exit code.
func CompleteTask(ctx context.Context, pool *pgxpool.Pool, taskID string, status TaskStatus, exitCode int) error {
	_, err := pool.Exec(ctx, completeTaskSQL, status, exitCode, taskID)
	return err
}

// GetTask retrieves a single task by ID.
// staleThreshold is the duration after which a running task with no worker heartbeat is considered stale.
func GetTask(ctx context.Context, pool *pgxpool.Pool, taskID string, staleThreshold time.Duration) (*Task, error) {
	interval := fmt.Sprintf("%d seconds", int(staleThreshold.Seconds()))
	row := pool.QueryRow(ctx, getTaskSQL, taskID, interval)
	return scanTask(row)
}

// RequestCancel requests cancellation of a task.
// For pending tasks: directly sets status to cancelled.
// For running tasks: sets cancel_requested_at (worker handles actual cancellation).
// Returns the original status (nil if task not found).
func RequestCancel(ctx context.Context, pool *pgxpool.Pool, taskID string) (*TaskStatus, error) {
	var originalStatus *TaskStatus
	err := pool.QueryRow(ctx, requestCancelSQL, taskID).Scan(&originalStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return originalStatus, nil
}

// CancelRequested reads the durable request independently of notification delivery.
func CancelRequested(ctx context.Context, pool *pgxpool.Pool, taskID string) (bool, error) {
	var requested bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT FROM tasks WHERE id = $1 AND cancel_requested_at IS NOT NULL
	)`, taskID).Scan(&requested)
	return requested, err
}

// DeleteTask removes a task and its logs from the database.
// Returns true if the task was deleted, false if it didn't exist.
func DeleteTask(ctx context.Context, pool *pgxpool.Pool, taskID string) (bool, error) {
	var deletedID *string
	err := pool.QueryRow(ctx, deleteTaskSQL, taskID).Scan(&deletedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return deletedID != nil, nil
}
