UPDATE tasks
SET status = $1, exit_code = $2, finished_at = NOW()
WHERE id = $3
