WITH original AS (
    SELECT id, status FROM tasks WHERE id = $1
),
updated AS (
    UPDATE tasks
    SET
        status = CASE WHEN status = 'pending' THEN 'cancelled' ELSE status END,
        finished_at = CASE WHEN status = 'pending' THEN NOW() ELSE finished_at END,
        cancel_requested_at = CASE WHEN status = 'running' THEN NOW() ELSE cancel_requested_at END
    WHERE id = $1
      AND status IN ('pending', 'running')
      AND cancel_requested_at IS NULL
)
SELECT status FROM original
