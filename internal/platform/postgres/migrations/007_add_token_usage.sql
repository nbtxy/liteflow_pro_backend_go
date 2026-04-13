-- +goose Up
CREATE TABLE token_usage (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    message_id      UUID REFERENCES messages(id) ON DELETE SET NULL,
    provider        VARCHAR(50) NOT NULL,
    model           VARCHAR(50) NOT NULL,
    input_tokens    INTEGER NOT NULL,
    output_tokens   INTEGER NOT NULL,
    purpose         VARCHAR(30) NOT NULL,
    channel         VARCHAR(20) DEFAULT 'web',
    duration_ms     INTEGER,
    created_at      TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_token_usage_user_time ON token_usage(user_id, created_at);
CREATE INDEX idx_token_usage_purpose ON token_usage(user_id, purpose, created_at);
CREATE INDEX idx_token_usage_model ON token_usage(user_id, model, created_at);

-- +goose Down
DROP TABLE IF EXISTS token_usage;
