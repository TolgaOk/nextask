SELECT * FROM (
    SELECT id, task_id, seq, stream, data, created_at
    FROM task_logs
    WHERE task_id = $1 AND ($2 = '' OR stream = $2)
    ORDER BY
        CASE WHEN $4 THEN seq END DESC NULLS LAST,
        CASE WHEN $4 THEN id END DESC,
        CASE WHEN NOT $4 THEN seq END ASC NULLS LAST,
        CASE WHEN NOT $4 THEN id END ASC
    LIMIT CASE WHEN $3 > 0 THEN $3 ELSE NULL END
) sub
ORDER BY seq ASC NULLS LAST, id ASC
