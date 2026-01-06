SELECT id, command, status,
    source_type, source_config, init_type, init_config,
    tags, worker_id, worker_info, exit_code,
    created_at, started_at, finished_at
FROM tasks
WHERE id = $1
