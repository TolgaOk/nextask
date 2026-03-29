ALTER TABLE task_logs ADD COLUMN IF NOT EXISTS seq INT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_logs_task_seq ON task_logs(task_id, seq) WHERE seq IS NOT NULL;
