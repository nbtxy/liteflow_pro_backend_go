-- +goose Up
CREATE TABLE artifacts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id      UUID REFERENCES messages(id) ON DELETE SET NULL,
    file_path       VARCHAR(500) NOT NULL,
    type            VARCHAR(20) NOT NULL,
    title           VARCHAR(200),
    file_size       BIGINT,
    version         INTEGER DEFAULT 1,
    parent_id       UUID REFERENCES artifacts(id) ON DELETE SET NULL,
    metadata        JSONB DEFAULT '{}',
    file_deleted    BOOLEAN DEFAULT FALSE,
    created_at      TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_artifacts_conversation ON artifacts(conversation_id, created_at);
CREATE INDEX idx_artifacts_path ON artifacts(conversation_id, file_path);

-- +goose Down
DROP TABLE IF EXISTS artifacts;
