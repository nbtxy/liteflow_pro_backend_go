-- +goose Up
ALTER TABLE conversations
    ADD COLUMN IF NOT EXISTS mcp_state JSONB NOT NULL DEFAULT '{"mode":"inactive","activated_tools":[]}'::jsonb,
    ADD COLUMN IF NOT EXISTS mcp_prev_state JSONB;

-- +goose Down
ALTER TABLE conversations
    DROP COLUMN IF EXISTS mcp_prev_state,
    DROP COLUMN IF EXISTS mcp_state;
