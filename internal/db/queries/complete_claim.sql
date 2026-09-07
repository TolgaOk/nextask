WITH original AS MATERIALIZED (
    SELECT id, status, exit_code, finished_at
    FROM tasks
    WHERE id = $1 AND worker_id = $2 AND created_at = $3 AND started_at = $4
    FOR UPDATE
), updated AS (
    UPDATE tasks t
    SET status = $5, exit_code = $6, finished_at = $7
    FROM original o
    WHERE t.id = o.id AND o.status = 'running'
    RETURNING t.id
)
SELECT EXISTS (SELECT FROM updated)
    OR EXISTS (
        SELECT FROM original
        WHERE status = $5 AND exit_code = $6 AND finished_at = $7
    )
