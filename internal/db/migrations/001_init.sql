CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    command TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',

    -- Source: type discriminator + flexible config
    source_type TEXT NOT NULL DEFAULT 'noop',
    source_config JSONB,

    -- Initializer: type discriminator + flexible config
    init_type TEXT NOT NULL DEFAULT 'noop',
    init_config JSONB,

    -- Metadata
    tags JSONB NOT NULL DEFAULT '{}',

    -- Worker info
    worker_id TEXT,
    worker_info JSONB,
    exit_code INTEGER,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    cancel_requested_at TIMESTAMPTZ
);

-- Add column if table already exists (idempotent)
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_created_at ON tasks(created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_tags ON tasks USING GIN(tags);

-- Partial index for efficient pending task claiming (FOR UPDATE SKIP LOCKED)
CREATE INDEX IF NOT EXISTS idx_tasks_pending_fifo
ON tasks(created_at)
WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS task_logs (
    id SERIAL PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    stream TEXT NOT NULL,
    data TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_logs_task_id ON task_logs(task_id);
