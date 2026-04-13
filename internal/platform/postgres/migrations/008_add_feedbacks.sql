-- +goose Up
CREATE TABLE feedbacks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id      UUID NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
    rating          VARCHAR(10) NOT NULL,
    reasons         JSONB DEFAULT '[]',
    comment         TEXT,
    context         JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_feedbacks_user ON feedbacks(user_id, created_at);
CREATE INDEX idx_feedbacks_rating ON feedbacks(rating, created_at);
CREATE INDEX idx_feedbacks_conversation ON feedbacks(conversation_id);

-- +goose Down
DROP TABLE IF EXISTS feedbacks;
