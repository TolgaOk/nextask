UPDATE workers SET status = 'stopped', stopped_at = NOW()
WHERE id = $1
