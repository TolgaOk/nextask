UPDATE workers SET last_heartbeat = NOW()
WHERE id = $1 AND status = 'running'
