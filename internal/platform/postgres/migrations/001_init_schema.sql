-- +goose Up
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone       VARCHAR(20) UNIQUE NOT NULL,
    name        VARCHAR(100),
    settings    JSONB DEFAULT '{}',
    is_admin    BOOLEAN DEFAULT false,
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

CREATE TABLE user_channels (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID NOT NULL REFERENCES users(id),
    type                    VARCHAR(20) NOT NULL,
    name                    VARCHAR(100),
    display_name            VARCHAR(100),
    authorized_account_id   VARCHAR(255),
    config                  JSONB NOT NULL,
    status                  VARCHAR(20) DEFAULT 'pending',
    error_message           VARCHAR(500),
    created_at              TIMESTAMPTZ DEFAULT now(),
    updated_at              TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_user_channels_user ON user_channels(user_id, type);
CREATE INDEX idx_user_channels_status ON user_channels(status);
CREATE INDEX idx_user_channels_auth_acc ON user_channels(user_id, type, name, authorized_account_id);

CREATE TABLE channel_user_mappings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id          UUID NOT NULL REFERENCES user_channels(id) ON DELETE CASCADE,
    external_user_id    VARCHAR(200) NOT NULL,
    external_name       VARCHAR(100),
    liteflow_user_id    UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ DEFAULT now()
);
CREATE UNIQUE INDEX idx_channel_user ON channel_user_mappings(channel_id, external_user_id);

CREATE TABLE conversations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID REFERENCES users(id),
    title            VARCHAR(200),
    memory_extracted BOOLEAN DEFAULT FALSE,
    archived         BOOLEAN DEFAULT FALSE,
    channel_id       UUID REFERENCES user_channels(id),
    external_chat_id VARCHAR(200),
    channel_type     VARCHAR(20) DEFAULT 'web',
    mcp_state        JSONB NOT NULL DEFAULT '{"mode":"inactive","activated_tools":[]}'::jsonb,
    mcp_prev_state   JSONB,
    created_at       TIMESTAMPTZ DEFAULT now(),
    updated_at       TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_conversations_user ON conversations(user_id, updated_at DESC);
CREATE INDEX idx_conversations_archived ON conversations(user_id, archived, updated_at DESC);
CREATE INDEX idx_conversations_channel ON conversations(channel_id, external_chat_id);

CREATE TABLE messages (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id     UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role                VARCHAR(20) NOT NULL,
    sender_type         VARCHAR(20) NOT NULL DEFAULT 'user',
    agent_id            VARCHAR(64),
    parent_message_id   UUID REFERENCES messages(id) ON DELETE SET NULL,
    is_internal         BOOLEAN NOT NULL DEFAULT FALSE,
    content_parts       JSONB NOT NULL DEFAULT '[]'::jsonb,
    token_count         INTEGER,
    metadata            JSONB DEFAULT '{}',
    created_at          TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_messages_conversation ON messages(conversation_id, created_at);
CREATE INDEX idx_messages_agent ON messages(agent_id);
CREATE INDEX idx_messages_parent ON messages(parent_message_id);

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
    storage_type    VARCHAR(20) DEFAULT 'LOCAL',
    upload_status   VARCHAR(20) DEFAULT 'READY',
    created_at      TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_artifacts_conversation ON artifacts(conversation_id, created_at);
CREATE INDEX idx_artifacts_path ON artifacts(conversation_id, file_path);

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

CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    token       VARCHAR(500) NOT NULL UNIQUE,
    device_info VARCHAR(200),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked     BOOLEAN DEFAULT false,
    created_at  TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_refresh_tokens_token ON refresh_tokens(token);
CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);

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

CREATE TABLE mcp_tools (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      UUID NOT NULL REFERENCES user_channels(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id),
    tool_name       VARCHAR(200) NOT NULL,
    display_name    VARCHAR(200) NOT NULL,
    description     TEXT NOT NULL,
    input_schema    JSONB NOT NULL,
    category        VARCHAR(50),
    updated_at      TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_mcp_tools_user ON mcp_tools(user_id);
CREATE INDEX idx_mcp_tools_channel ON mcp_tools(channel_id);
CREATE UNIQUE INDEX idx_mcp_tools_channel_name ON mcp_tools(channel_id, tool_name);

CREATE TABLE scheduled_tasks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id),
    conversation_id   UUID REFERENCES conversations(id) ON DELETE CASCADE,
    source_message_id UUID REFERENCES messages(id) ON DELETE CASCADE,
    name              VARCHAR(200) NOT NULL,
    prompt            TEXT NOT NULL,
    cron_expression   VARCHAR(50),
    task_type         VARCHAR(20) DEFAULT 'recurring',
    run_at            TIMESTAMPTZ,
    timezone          VARCHAR(50) DEFAULT 'Asia/Shanghai',
    output_config     JSONB NOT NULL DEFAULT '{"targets":[{"type":"conversation"}]}',
    status            VARCHAR(20) DEFAULT 'active',
    max_tokens        INTEGER DEFAULT 50000,
    enable_tools      BOOLEAN DEFAULT true,
    last_run_at       TIMESTAMPTZ,
    last_run_status   VARCHAR(20),
    total_runs        INTEGER DEFAULT 0,
    total_tokens      BIGINT DEFAULT 0,
    created_at        TIMESTAMPTZ DEFAULT now(),
    updated_at        TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_scheduled_tasks_user ON scheduled_tasks(user_id);
CREATE INDEX idx_scheduled_tasks_status ON scheduled_tasks(status);
CREATE INDEX idx_scheduled_tasks_conversation ON scheduled_tasks(conversation_id);
CREATE INDEX idx_scheduled_tasks_source_message ON scheduled_tasks(source_message_id);

CREATE TABLE task_executions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id         UUID NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    status          VARCHAR(20) NOT NULL,
    result_summary  TEXT,
    output_targets  JSONB,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    tools_used      JSONB,
    duration_ms     INTEGER,
    error_message   TEXT,
    created_at      TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_task_executions_task ON task_executions(task_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS task_executions;
DROP TABLE IF EXISTS scheduled_tasks;
DROP TABLE IF EXISTS mcp_tools;
DROP TABLE IF EXISTS feedbacks;
DROP TABLE IF EXISTS token_usage;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS user_memories;
DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS channel_user_mappings;
DROP TABLE IF EXISTS user_channels;
DROP TABLE IF EXISTS sms_codes;
DROP TABLE IF EXISTS users;
