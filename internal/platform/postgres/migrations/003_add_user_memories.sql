-- +goose Up
CREATE TABLE user_memories (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID NOT NULL,
    category                VARCHAR(50) NOT NULL,
    content                 TEXT NOT NULL,
    source_conversation_id  UUID REFERENCES conversations(id) ON DELETE SET NULL,
    confidence              FLOAT DEFAULT 1.0,
    is_active               BOOLEAN DEFAULT TRUE,
    created_at              TIMESTAMPTZ DEFAULT now(),
    updated_at              TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_memories_user ON user_memories(user_id, is_active);
CREATE INDEX idx_memories_category ON user_memories(user_id, category);

ALTER TABLE conversations ADD COLUMN memory_extracted BOOLEAN DEFAULT FALSE;

-- +goose Down
ALTER TABLE conversations DROP COLUMN IF EXISTS memory_extracted;
DROP TABLE IF EXISTS user_memories;
