-- +goose Up
CREATE TABLE user_channels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    type            VARCHAR(20) NOT NULL,
    name            VARCHAR(100),
    config          JSONB NOT NULL,
    status          VARCHAR(20) DEFAULT 'pending',
    error_message   VARCHAR(500),
    created_at      TIMESTAMPTZ DEFAULT now(),
    updated_at      TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_user_channels_user ON user_channels(user_id, type);
CREATE INDEX idx_user_channels_status ON user_channels(status);

CREATE TABLE channel_user_mappings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id          UUID NOT NULL REFERENCES user_channels(id) ON DELETE CASCADE,
    external_user_id    VARCHAR(200) NOT NULL,
    external_name       VARCHAR(100),
    liteflow_user_id    UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ DEFAULT now()
);

CREATE UNIQUE INDEX idx_channel_user ON channel_user_mappings(channel_id, external_user_id);

ALTER TABLE conversations ADD COLUMN channel_id UUID REFERENCES user_channels(id);
ALTER TABLE conversations ADD COLUMN external_chat_id VARCHAR(200);
ALTER TABLE conversations ADD COLUMN channel_type VARCHAR(20) DEFAULT 'web';

CREATE INDEX idx_conversations_channel ON conversations(channel_id, external_chat_id);

-- +goose Down
ALTER TABLE conversations DROP COLUMN IF EXISTS channel_type;
ALTER TABLE conversations DROP COLUMN IF EXISTS external_chat_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS channel_id;
DROP TABLE IF EXISTS channel_user_mappings;
DROP TABLE IF EXISTS user_channels;
