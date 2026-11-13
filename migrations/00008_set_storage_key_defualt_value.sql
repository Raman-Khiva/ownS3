-- +goose Up
ALTER TABLE objects
ALTER COLUMN storage_key
SET DEFAULT gen_random_uuid();

-- +goose Down
SELECT 'down SQL query';
