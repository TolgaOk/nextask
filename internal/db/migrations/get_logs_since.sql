SELECT id, task_id, stream, data, created_at
FROM task_logs
WHERE task_id = $1 AND id > $2
ORDER BY id ASC
