-- +goose Up
ALTER TABLE artifacts ADD COLUMN storage_type VARCHAR(20) DEFAULT 'LOCAL';
ALTER TABLE artifacts ADD COLUMN upload_status VARCHAR(20) DEFAULT 'READY';

-- +goose Down
ALTER TABLE artifacts DROP COLUMN IF EXISTS upload_status;
ALTER TABLE artifacts DROP COLUMN IF EXISTS storage_type;
