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

func applyTaskFilters(query sq.SelectBuilder, filter ListFilter, statusExpr string) sq.SelectBuilder {
	if len(filter.Statuses) > 0 {
		query = query.Where(sq.Eq{statusExpr: filter.Statuses})
	}
	for k, v := range filter.Tags {
		query = query.Where("t.tags @> ?::jsonb", fmt.Sprintf(`{"%s": "%s"}`, k, v))
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
	return query
}

// ListTasks retrieves tasks matching the given filter criteria.
func ListTasks(ctx context.Context, pool *pgxpool.Pool, filter ListFilter) ([]Task, error) {
	statusExpr := staleStatusExpr(filter.StaleThreshold)

	query := psql.Select("t.id", "t.command", statusExpr, "t.tags", "t.created_at").
		From("tasks t").
		LeftJoin("workers w ON t.worker_id = w.id").
		OrderBy("t.created_at DESC")

	query = applyTaskFilters(query, filter, statusExpr)

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

	query = applyTaskFilters(query, filter, statusExpr)

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

// LogEntry represents a single log line for batch insertion.
type LogEntry struct {
	Seq    int
	Stream string
	Data   string
}

// InsertLogBatch inserts multiple log lines in a single query and returns the max inserted ID.
// Uses ON CONFLICT to make retries idempotent — duplicate (task_id, seq) pairs are silently skipped.
func InsertLogBatch(ctx context.Context, pool *pgxpool.Pool, taskID string, entries []LogEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	query := "INSERT INTO task_logs (task_id, seq, stream, data) VALUES "
	args := make([]any, 0, 1+len(entries)*3)
	args = append(args, taskID) // $1

	for i, e := range entries {
		if i > 0 {
			query += ", "
		}
		seqIdx := 2 + i*3
		streamIdx := 3 + i*3
		dataIdx := 4 + i*3
		query += fmt.Sprintf("($1, $%d, $%d, $%d)", seqIdx, streamIdx, dataIdx)
		args = append(args, e.Seq, e.Stream, e.Data)
	}
	query += " ON CONFLICT (task_id, seq) WHERE seq IS NOT NULL DO NOTHING"
	query += " RETURNING id"

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var maxID int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		if id > maxID {
			maxID = id
		}
	}
	return maxID, rows.Err()
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
		if err := rows.Scan(&log.ID, &log.TaskID, &log.Seq, &log.Stream, &log.Data, &log.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

// GetTask retrieves a single task by ID.
// staleThreshold is the duration after which a running task with no worker heartbeat is considered stale.
func GetTask(ctx context.Context, pool *pgxpool.Pool, taskID string, staleThreshold time.Duration) (*Task, error) {
	sql, err := migrations.FS.ReadFile("get_task.sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read get_task.sql: %w", err)
	}
	interval := fmt.Sprintf("%d seconds", int(staleThreshold.Seconds()))
	row := pool.QueryRow(ctx, string(sql), taskID, interval)
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
		if err := rows.Scan(&log.ID, &log.TaskID, &log.Seq, &log.Stream, &log.Data, &log.CreatedAt); err != nil {
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

// WorkerListFilter specifies criteria for filtering workers.
type WorkerListFilter struct {
	Status *WorkerStatus
	Since  time.Time
	Limit  uint64
	Offset uint64
}

func (f WorkerListFilter) args() (*string, *time.Time, uint64, uint64) {
	var statusArg *string
	if f.Status != nil {
		s := string(*f.Status)
		statusArg = &s
	}
	var sinceArg *time.Time
	if !f.Since.IsZero() {
		sinceArg = &f.Since
	}
	limit := f.Limit
	if limit == 0 {
		limit = 1000
	}
	return statusArg, sinceArg, limit, f.Offset
}

// ListWorkers retrieves workers matching the given filter.
func ListWorkers(ctx context.Context, pool *pgxpool.Pool, filter WorkerListFilter) ([]WorkerRecord, error) {
	sql, err := migrations.FS.ReadFile("list_workers.sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read list_workers.sql: %w", err)
	}

	statusArg, sinceArg, limit, offset := filter.args()

	rows, err := pool.Query(ctx, string(sql), statusArg, sinceArg, limit, offset)
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

// CountWorkers returns the total count of workers matching the filter.
func CountWorkers(ctx context.Context, pool *pgxpool.Pool, filter WorkerListFilter) (int, error) {
	statusArg, sinceArg, _, _ := filter.args()

	var count int
	err := pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM workers WHERE ($1::text IS NULL OR status = $1) AND ($2::timestamptz IS NULL OR started_at >= $2)",
		statusArg, sinceArg,
	).Scan(&count)
	return count, err
}
