package db

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed queries/insert_log.sql
var insertLogSQL string

//go:embed queries/get_logs_since.sql
var getLogsSinceSQL string

//go:embed queries/get_logs.sql
var getLogsSQL string

// InsertLog stores a log line from task execution and returns the inserted log ID.
func InsertLog(ctx context.Context, pool *pgxpool.Pool, taskID, stream, data string) (int, error) {
	var id int
	err := pool.QueryRow(ctx, insertLogSQL, taskID, stream, data).Scan(&id)
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
	rows, err := pool.Query(ctx, getLogsSinceSQL, taskID, lastLogID)
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

// GetLogs retrieves logs for a task, optionally filtered by stream.
// If limit > 0, returns at most limit lines. If tail is true, returns the last lines.
func GetLogs(ctx context.Context, pool *pgxpool.Pool, taskID, stream string, limit int, tail bool) ([]TaskLog, error) {
	rows, err := pool.Query(ctx, getLogsSQL, taskID, stream, limit, tail)
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
