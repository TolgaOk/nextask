SELECT t.id, t.command,
    CASE
        WHEN t.status = 'running' AND w.last_heartbeat < NOW() - $2::interval THEN 'stale'
        ELSE t.status
    END AS status,
    t.source_type, t.source_config,
    t.tags, t.worker_id, t.worker_info, t.exit_code,
    t.created_at, t.started_at, t.finished_at
FROM tasks t
LEFT JOIN workers w ON t.worker_id = w.id
WHERE t.id = $1
