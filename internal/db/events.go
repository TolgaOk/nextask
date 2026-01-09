package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// --- Channel Names ---

// Generic worker channel (broadcast to all workers)
const ToWorkersChannel = "to_workers"

// FromTaskChannel returns the channel name for events from a task (worker → consumer).
func FromTaskChannel(taskID string) string {
	return fmt.Sprintf("from_task_%s", taskID)
}

// ToTaskChannel returns the channel name for commands to a task (consumer → worker).
func ToTaskChannel(taskID string) string {
	return fmt.Sprintf("to_task_%s", taskID)
}

// ToWorkerChannel returns the channel name for commands to a worker (CLI → worker).
func ToWorkerChannel(workerID string) string {
	return fmt.Sprintf("to_worker_%s", workerID)
}

// FromWorkerChannel returns the channel name for events from a worker (worker → CLI).
func FromWorkerChannel(workerID string) string {
	return fmt.Sprintf("from_worker_%s", workerID)
}

// --- Event Types ---

// Task events

// TaskLogEvent is sent from worker to consumer when a log line is inserted.
type TaskLogEvent struct {
	ID int `json:"id"` // log row ID for incremental fetch
}

// TaskStatusEvent is sent from worker to consumer when task status changes.
type TaskStatusEvent struct {
	Status   string `json:"status"`    // completed, failed, cancelled
	ExitCode int    `json:"exit_code"`
}

// TaskCancelEvent is sent from consumer to worker to request cancellation.
type TaskCancelEvent struct{}

// Worker events

// WorkerWakeEvent signals workers that new tasks are available.
type WorkerWakeEvent struct{}

// Event type constants for JSON dispatch.
const (
	// Task events
	EventTypeLog    = "log"
	EventTypeStatus = "status"
	EventTypeCancel = "cancel"
	// Worker events
	EventTypeWake = "wake"
)

// eventWrapper wraps an event with its type for JSON serialization.
type eventWrapper struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Notify sends a typed event to a PostgreSQL NOTIFY channel.
func Notify(ctx context.Context, pool *pgxpool.Pool, channel string, event any) error {
	var eventType string
	switch event.(type) {
	// Task events
	case TaskLogEvent:
		eventType = EventTypeLog
	case TaskStatusEvent:
		eventType = EventTypeStatus
	case TaskCancelEvent:
		eventType = EventTypeCancel
	// Worker events
	case WorkerWakeEvent:
		eventType = EventTypeWake
	default:
		return fmt.Errorf("unknown event type: %T", event)
	}

	wrapper := eventWrapper{Type: eventType, Data: event}
	data, err := json.Marshal(wrapper)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	_, err = pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, string(data))
	return err
}

// ParseEvent parses a JSON payload and returns the event type and raw data.
func ParseEvent(payload string) (eventType string, data json.RawMessage, err error) {
	var wrapper struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(payload), &wrapper); err != nil {
		return "", nil, err
	}
	return wrapper.Type, wrapper.Data, nil
}
