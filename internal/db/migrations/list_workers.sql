SELECT id, pid, hostname, workdir, status, started_at, last_heartbeat, stopped_at
FROM workers
WHERE ($1::text IS NULL OR status = $1)
ORDER BY started_at DESC
