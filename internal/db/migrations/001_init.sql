CREATE TABLE IF NOT EXISTS tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    command TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',

    -- Source snapshot (for reproducibility)
    source_ref TEXT,

    -- Init configuration
    init_type TEXT,
    init_config TEXT,

    -- Metadata
    labels JSONB NOT NULL DEFAULT '{}',
    tags JSONB NOT NULL DEFAULT '{}',

    -- Worker info
    worker_id TEXT,
    exit_code INTEGER,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_created_at ON tasks(created_at);
