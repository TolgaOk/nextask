INSERT INTO workers (id, pid, hostname, workdir, status, started_at, last_heartbeat)
VALUES ($1, $2, $3, $4, 'running', NOW(), NOW())
ON CONFLICT (id) DO UPDATE SET
    pid = EXCLUDED.pid,
    hostname = EXCLUDED.hostname,
    workdir = EXCLUDED.workdir,
    status = 'running',
    started_at = NOW(),
    last_heartbeat = NOW(),
    stopped_at = NULL
