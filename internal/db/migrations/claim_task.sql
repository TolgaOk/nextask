UPDATE tasks
SET status = $1, worker_id = $2, worker_info = $3, started_at = NOW()
WHERE id = (
    SELECT id FROM tasks
    WHERE status = 'pending'
    AND ($4::jsonb IS NULL OR tags @> $4)
    ORDER BY created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, command, status, source_type, source_config, tags, worker_id, worker_info, exit_code, created_at, started_at, finished_at
