-- +goose Up
ALTER TABLE scheduled_tasks
    DROP CONSTRAINT IF EXISTS scheduled_tasks_conversation_id_fkey,
    ADD CONSTRAINT scheduled_tasks_conversation_id_fkey
        FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE;

ALTER TABLE scheduled_tasks
    ADD COLUMN IF NOT EXISTS source_message_id UUID;

-- +goose Down
ALTER TABLE scheduled_tasks
    DROP CONSTRAINT IF EXISTS scheduled_tasks_conversation_id_fkey,
    ADD CONSTRAINT scheduled_tasks_conversation_id_fkey
        FOREIGN KEY (conversation_id) REFERENCES conversations(id);

ALTER TABLE scheduled_tasks
    DROP COLUMN IF EXISTS source_message_id;
