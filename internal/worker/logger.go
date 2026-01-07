package worker

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/TolgaOk/nextask/internal/db"
)

// Logger defines the interface for capturing task output.
type Logger interface {
	Log(stream, data string)
}

// DBLogger writes log lines to the database.
type DBLogger struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	taskID string
}

// NewDBLogger creates a logger that persists output to the database.
func NewDBLogger(ctx context.Context, pool *pgxpool.Pool, taskID string) *DBLogger {
	return &DBLogger{
		ctx:    ctx,
		pool:   pool,
		taskID: taskID,
	}
}

// Log writes a line to the specified stream (stdout/stderr) and notifies listeners.
func (l *DBLogger) Log(stream, data string) {
	id, err := db.InsertLog(l.ctx, l.pool, l.taskID, stream, data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to insert log: %v\n", err)
		return
	}

	channel := db.FromTaskChannel(l.taskID)
	if err := db.Notify(l.ctx, l.pool, channel, db.TaskLogEvent{ID: id}); err != nil {
		fmt.Fprintf(os.Stderr, "failed to notify log: %v\n", err)
	}
}
