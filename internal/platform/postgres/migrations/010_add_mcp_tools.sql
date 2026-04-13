-- +goose Up
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

-- +goose Down
DROP TABLE IF EXISTS mcp_tools;
