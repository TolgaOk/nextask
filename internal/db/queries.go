package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db/migrations"
)

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// CreateTask inserts a new task into the queue.
func CreateTask(ctx context.Context, pool *pgxpool.Pool, task *Task) error {
	tagsJSON, err := json.Marshal(task.Tags)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO tasks (id, command, status, tags, source_type, source_config)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, task.ID, task.Command, task.Status, tagsJSON,
		task.SourceType, task.SourceConfig)

	return wrapPgError(err)
}

// ListFilter specifies criteria for filtering tasks.
type ListFilter struct {
	Statuses []string
	Tags     map[string]string
	Commands []string
	Since    time.Time
	Limit    uint64
}

// ListTasks retrieves tasks matching the given filter criteria.
func ListTasks(ctx context.Context, pool *pgxpool.Pool, filter ListFilter) ([]Task, error) {
	query := psql.Select("id", "command", "status", "tags", "created_at").
		From("tasks").
		OrderBy("created_at DESC")

	if len(filter.Statuses) > 0 {
		query = query.Where(sq.Eq{"status": filter.Statuses})
	}

	for k, v := range filter.Tags {
		query = query.Where("tags @> ?::jsonb", fmt.Sprintf(`{"%s": "%s"}`, k, v))
	}

	for _, cmd := range filter.Commands {
		query = query.Where("command ILIKE ?", "%"+cmd+"%")
	}

	if !filter.Since.IsZero() {
		query = query.Where(sq.GtOrEq{"created_at": filter.Since})
	}

	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
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

type scannable interface {
	Scan(dest ...any) error
}

func scanTask(row scannable) (*Task, error) {
	var t Task
	var tagsJSON, wiJSON []byte
	err := row.Scan(
		&t.ID, &t.Command, &t.Status,
		&t.SourceType, &t.SourceConfig,
		&tagsJSON, &t.WorkerID, &wiJSON, &t.ExitCode,
		&t.CreatedAt, &t.StartedAt, &t.FinishedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
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

	sql, err := migrations.FS.ReadFile("claim_task.sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read claim_task.sql: %w", err)
	}

	row := pool.QueryRow(ctx, string(sql), StatusRunning, workerID, workerInfoJSON, tagFilterJSON)
	return scanTask(row)
}

// CompleteTask marks a task as completed or failed with its exit code.
func CompleteTask(ctx context.Context, pool *pgxpool.Pool, taskID string, status TaskStatus, exitCode int) error {
	sql, err := migrations.FS.ReadFile("complete_task.sql")
	if err != nil {
		return fmt.Errorf("failed to read complete_task.sql: %w", err)
	}
	_, err = pool.Exec(ctx, string(sql), status, exitCode, taskID)
	return err
}

// InsertLog stores a log line from task execution and returns the inserted log ID.
func InsertLog(ctx context.Context, pool *pgxpool.Pool, taskID, stream, data string) (int, error) {
	sql, err := migrations.FS.ReadFile("insert_log.sql")
	if err != nil {
		return 0, fmt.Errorf("failed to read insert_log.sql: %w", err)
	}
	var id int
	err = pool.QueryRow(ctx, string(sql), taskID, stream, data).Scan(&id)
	return id, err
}

// GetLogsSince retrieves logs for a task with ID greater than lastLogID.
func GetLogsSince(ctx context.Context, pool *pgxpool.Pool, taskID string, lastLogID int) ([]TaskLog, error) {
	sql, err := migrations.FS.ReadFile("get_logs_since.sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read get_logs_since.sql: %w", err)
	}

	rows, err := pool.Query(ctx, string(sql), taskID, lastLogID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []TaskLog
	for rows.Next() {
		var log TaskLog
		if err := rows.Scan(&log.ID, &log.TaskID, &log.Stream, &log.Data, &log.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

// GetTask retrieves a single task by ID.
func GetTask(ctx context.Context, pool *pgxpool.Pool, taskID string) (*Task, error) {
	sql, err := migrations.FS.ReadFile("get_task.sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read get_task.sql: %w", err)
	}
	row := pool.QueryRow(ctx, string(sql), taskID)
	return scanTask(row)
}

// RequestCancel requests cancellation of a task.
// For pending tasks: directly sets status to cancelled.
// For running tasks: sets cancel_requested_at (worker handles actual cancellation).
// Returns the original status (nil if task not found).
func RequestCancel(ctx context.Context, pool *pgxpool.Pool, taskID string) (*TaskStatus, error) {
	sql, err := migrations.FS.ReadFile("request_cancel.sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read request_cancel.sql: %w", err)
	}

	var originalStatus *TaskStatus
	err = pool.QueryRow(ctx, string(sql), taskID).Scan(&originalStatus)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, err
	}

	return originalStatus, nil
}

// DeleteTask removes a task and its logs from the database.
// Returns true if the task was deleted, false if it didn't exist.
func DeleteTask(ctx context.Context, pool *pgxpool.Pool, taskID string) (bool, error) {
	sql, err := migrations.FS.ReadFile("delete_task.sql")
	if err != nil {
		return false, fmt.Errorf("failed to read delete_task.sql: %w", err)
	}

	var deletedID *string
	err = pool.QueryRow(ctx, string(sql), taskID).Scan(&deletedID)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return false, nil
		}
		return false, err
	}
	return deletedID != nil, nil
}

// GetLogs retrieves logs for a task, optionally filtered by stream.
// If limit > 0, returns at most limit lines. If tail is true, returns the last lines.
func GetLogs(ctx context.Context, pool *pgxpool.Pool, taskID, stream string, limit int, tail bool) ([]TaskLog, error) {
	sql, err := migrations.FS.ReadFile("get_logs.sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read get_logs.sql: %w", err)
	}

	rows, err := pool.Query(ctx, string(sql), taskID, stream, limit, tail)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []TaskLog
	for rows.Next() {
		var log TaskLog
		if err := rows.Scan(&log.ID, &log.TaskID, &log.Stream, &log.Data, &log.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}

	return logs, rows.Err()
}

// RegisterWorker adds a worker to the registry.
func RegisterWorker(ctx context.Context, pool *pgxpool.Pool, id string, pid int, hostname, workdir string) error {
	sql, err := migrations.FS.ReadFile("register_worker.sql")
	if err != nil {
		return fmt.Errorf("failed to read register_worker.sql: %w", err)
	}
	_, err = pool.Exec(ctx, string(sql), id, pid, hostname, workdir)
	return err
}

// UnregisterWorker marks a worker as stopped.
func UnregisterWorker(ctx context.Context, pool *pgxpool.Pool, id string) error {
	sql, err := migrations.FS.ReadFile("unregister_worker.sql")
	if err != nil {
		return fmt.Errorf("failed to read unregister_worker.sql: %w", err)
	}
	_, err = pool.Exec(ctx, string(sql), id)
	return err
}

// UpdateHeartbeat updates the last_heartbeat timestamp for a worker.
func UpdateHeartbeat(ctx context.Context, pool *pgxpool.Pool, id string) error {
	sql, err := migrations.FS.ReadFile("update_heartbeat.sql")
	if err != nil {
		return fmt.Errorf("failed to read update_heartbeat.sql: %w", err)
	}
	_, err = pool.Exec(ctx, string(sql), id)
	return err
}

// ListWorkers retrieves all workers, optionally filtered by status.
func ListWorkers(ctx context.Context, pool *pgxpool.Pool, status *WorkerStatus) ([]WorkerRecord, error) {
	sql, err := migrations.FS.ReadFile("list_workers.sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read list_workers.sql: %w", err)
	}

	var statusArg *string
	if status != nil {
		s := string(*status)
		statusArg = &s
	}

	rows, err := pool.Query(ctx, string(sql), statusArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []WorkerRecord
	for rows.Next() {
		var w WorkerRecord
		if err := rows.Scan(&w.ID, &w.PID, &w.Hostname, &w.Workdir, &w.Status, &w.StartedAt, &w.LastHeartbeat, &w.StoppedAt); err != nil {
			return nil, err
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}
