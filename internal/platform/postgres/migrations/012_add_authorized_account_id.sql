-- +goose Up
ALTER TABLE user_channels ADD COLUMN authorized_account_id VARCHAR(255);
CREATE INDEX idx_user_channels_auth_acc ON user_channels(user_id, type, name, authorized_account_id);

-- +goose Down
DROP INDEX IF EXISTS idx_user_channels_auth_acc;
ALTER TABLE user_channels DROP COLUMN IF EXISTS authorized_account_id;
