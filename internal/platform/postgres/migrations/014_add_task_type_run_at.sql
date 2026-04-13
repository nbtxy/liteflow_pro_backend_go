-- +goose Up
ALTER TABLE scheduled_tasks ADD COLUMN IF NOT EXISTS task_type VARCHAR(20) DEFAULT 'recurring';
ALTER TABLE scheduled_tasks ADD COLUMN IF NOT EXISTS run_at TIMESTAMPTZ;
ALTER TABLE scheduled_tasks ALTER COLUMN cron_expression DROP NOT NULL;

ALTER TABLE scheduled_tasks ADD COLUMN IF NOT EXISTS source_message_id UUID;

ALTER TABLE scheduled_tasks DROP CONSTRAINT IF EXISTS scheduled_tasks_conversation_id_fkey;
ALTER TABLE scheduled_tasks ADD CONSTRAINT scheduled_tasks_conversation_id_fkey
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE;

ALTER TABLE scheduled_tasks ADD CONSTRAINT scheduled_tasks_source_message_id_fkey
    FOREIGN KEY (source_message_id) REFERENCES messages(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_conversation ON scheduled_tasks(conversation_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_source_message ON scheduled_tasks(source_message_id);

-- +goose Down
DROP INDEX IF EXISTS idx_scheduled_tasks_source_message;
DROP INDEX IF EXISTS idx_scheduled_tasks_conversation;
ALTER TABLE scheduled_tasks DROP CONSTRAINT IF EXISTS scheduled_tasks_source_message_id_fkey;
ALTER TABLE scheduled_tasks DROP COLUMN IF EXISTS source_message_id;
ALTER TABLE scheduled_tasks DROP COLUMN IF EXISTS run_at;
ALTER TABLE scheduled_tasks DROP COLUMN IF EXISTS task_type;
