package db

import (
	"context"
	_ "embed"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed queries/register_worker.sql
var registerWorkerSQL string

//go:embed queries/unregister_worker.sql
var unregisterWorkerSQL string

//go:embed queries/update_heartbeat.sql
var updateHeartbeatSQL string

// RegisterWorker adds a worker to the registry.
func RegisterWorker(ctx context.Context, pool *pgxpool.Pool, id string, pid int, hostname, workdir string) error {
	_, err := pool.Exec(ctx, registerWorkerSQL, id, pid, hostname, workdir)
	return err
}

// UnregisterWorker marks a worker as stopped.
func UnregisterWorker(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, unregisterWorkerSQL, id)
	return err
}

// UpdateHeartbeat updates the last_heartbeat timestamp for a worker.
func UpdateHeartbeat(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, updateHeartbeatSQL, id)
	return err
}

// WorkerListFilter specifies criteria for filtering workers.
type WorkerListFilter struct {
	ID     string
	Status *WorkerStatus
	Since  time.Time
	Limit  uint64
	Offset uint64
	// StaleThreshold enables heartbeat-based status when positive.
	// Zero leaves the stored status intact for registry operations.
	StaleThreshold time.Duration
}

// workerListQuery shares status classification and filters between rows and counts.
func workerListQuery(filter WorkerListFilter) sq.SelectBuilder {
	workers := sq.Select("id", "pid", "hostname", "workdir", "started_at", "last_heartbeat", "stopped_at").
		From("workers")
	if filter.StaleThreshold > 0 {
		workers = workers.Column(`CASE
			WHEN status = 'running' AND last_heartbeat < NOW() - (? * INTERVAL '1 second')
			THEN 'stale' ELSE status END AS status`, filter.StaleThreshold.Seconds())
	} else {
		workers = workers.Column("status")
	}

	query := psql.Select().FromSelect(workers, "w")
	if filter.ID != "" {
		query = query.Where(sq.Eq{"id": filter.ID})
	}
	if filter.Status != nil {
		query = query.Where(sq.Eq{"status": *filter.Status})
	}
	if !filter.Since.IsZero() {
		query = query.Where(sq.GtOrEq{"started_at": filter.Since})
	}
	return query
}

// ListWorkers retrieves workers matching the given filter.
func ListWorkers(ctx context.Context, pool *pgxpool.Pool, filter WorkerListFilter) ([]WorkerRecord, error) {
	limit := filter.Limit
	if limit == 0 {
		limit = 1000
	}
	query := workerListQuery(filter).
		Columns("id", "pid", "hostname", "workdir", "status", "started_at", "last_heartbeat", "stopped_at").
		OrderBy("started_at DESC", "id").
		Limit(limit).Offset(filter.Offset)
	sql, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, sql, args...)
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

// CountWorkers returns the total count of workers matching the filter, ignoring Limit/Offset.
func CountWorkers(ctx context.Context, pool *pgxpool.Pool, filter WorkerListFilter) (int, error) {
	sql, args, err := workerListQuery(filter).Column("COUNT(*)").ToSql()
	if err != nil {
		return 0, err
	}
	var count int
	err = pool.QueryRow(ctx, sql, args...).Scan(&count)
	return count, err
}
