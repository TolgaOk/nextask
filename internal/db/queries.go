package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
)

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

func CreateTask(ctx context.Context, pool *pgxpool.Pool, task *Task) error {
	tagsJSON, err := json.Marshal(task.Tags)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO tasks (id, command, status, tags, source_remote, source_ref, source_commit)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, task.ID, task.Command, task.Status, tagsJSON, task.SourceRemote, task.SourceRef, task.SourceCommit)

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
