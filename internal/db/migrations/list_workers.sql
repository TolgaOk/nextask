SELECT id, pid, hostname, workdir, status, started_at, last_heartbeat, stopped_at
FROM workers
WHERE ($1::text IS NULL OR status = $1)
  AND ($2::timestamptz IS NULL OR started_at >= $2)
ORDER BY started_at DESC
LIMIT $3 OFFSET $4
