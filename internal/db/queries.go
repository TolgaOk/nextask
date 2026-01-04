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

func CreateTask(ctx context.Context, pool *pgxpool.Pool, task *Task) error {
	tagsJSON, err := json.Marshal(task.Tags)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO tasks (id, command, status, tags, source_type, source_config, init_type, init_config)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, task.ID, task.Command, task.Status, tagsJSON,
		task.SourceType, task.SourceConfig, task.InitType, task.InitConfig)

	return err
}

type ListFilter struct {
	Statuses []string
	Tags     map[string]string
	Commands []string
	Since    time.Time
	Limit    uint64
}

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
		return nil, err
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
		&t.SourceType, &t.SourceConfig, &t.InitType, &t.InitConfig,
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

func ClaimTask(ctx context.Context, pool *pgxpool.Pool, workerID string, workerInfo *WorkerInfo) (*Task, error) {
	workerInfoJSON, err := json.Marshal(workerInfo)
	if err != nil {
		return nil, err
	}

	sql, err := migrations.FS.ReadFile("claim_task.sql")
	if err != nil {
		return nil, fmt.Errorf("failed to read claim_task.sql: %w", err)
	}

	row := pool.QueryRow(ctx, string(sql), StatusRunning, workerID, workerInfoJSON)
	return scanTask(row)
}

func CompleteTask(ctx context.Context, pool *pgxpool.Pool, taskID string, status TaskStatus, exitCode int) error {
	sql, err := migrations.FS.ReadFile("complete_task.sql")
	if err != nil {
		return fmt.Errorf("failed to read complete_task.sql: %w", err)
	}
	_, err = pool.Exec(ctx, string(sql), status, exitCode, taskID)
	return err
}

func InsertLog(ctx context.Context, pool *pgxpool.Pool, taskID, stream, data string) error {
	sql, err := migrations.FS.ReadFile("insert_log.sql")
	if err != nil {
		return fmt.Errorf("failed to read insert_log.sql: %w", err)
	}
	_, err = pool.Exec(ctx, string(sql), taskID, stream, data)
	return err
}
