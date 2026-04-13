-- +goose Up
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone       VARCHAR(20) UNIQUE NOT NULL,
    name        VARCHAR(100),
    settings    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ DEFAULT now(),
    updated_at  TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE sms_codes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone       VARCHAR(20) NOT NULL,
    code        VARCHAR(6) NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used        BOOLEAN DEFAULT false,
    created_at  TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_sms_codes_phone ON sms_codes(phone, used, expires_at);

ALTER TABLE conversations ADD COLUMN user_id UUID REFERENCES users(id);
CREATE INDEX idx_conversations_user ON conversations(user_id, updated_at DESC);

ALTER TABLE conversations ADD COLUMN archived BOOLEAN DEFAULT FALSE;
CREATE INDEX idx_conversations_archived ON conversations(user_id, archived, updated_at DESC);

-- +goose Down
ALTER TABLE conversations DROP COLUMN IF EXISTS archived;
DROP INDEX IF EXISTS idx_conversations_archived;
ALTER TABLE conversations DROP COLUMN IF EXISTS user_id;
DROP INDEX IF EXISTS idx_conversations_user;
DROP TABLE IF EXISTS sms_codes;
DROP TABLE IF EXISTS users;
