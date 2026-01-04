package worker

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db"
)

type Logger interface {
	Log(stream, data string)
}

type DBLogger struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	taskID string
}

func NewDBLogger(ctx context.Context, pool *pgxpool.Pool, taskID string) *DBLogger {
	return &DBLogger{
		ctx:    ctx,
		pool:   pool,
		taskID: taskID,
	}
}

func (l *DBLogger) Log(stream, data string) {
	db.InsertLog(l.ctx, l.pool, l.taskID, stream, data)
}
