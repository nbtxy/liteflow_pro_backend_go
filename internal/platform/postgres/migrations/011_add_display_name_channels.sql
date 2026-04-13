-- +goose Up
ALTER TABLE user_channels ADD COLUMN display_name VARCHAR(100);

UPDATE user_channels
SET display_name = name, name = 'feishu', type = 'im'
WHERE type = 'feishu';

UPDATE user_channels
SET display_name = name
WHERE type = 'mcp';

-- +goose Down
ALTER TABLE user_channels DROP COLUMN IF EXISTS display_name;
