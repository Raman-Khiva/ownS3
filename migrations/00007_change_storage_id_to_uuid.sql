-- +goose Up
ALTER TABLE objects
ALTER COLUMN storage_key TYPE UUID
USING gen_random_uuid();

ALTER TABLE objects
ALTER COLUMN storage_key SET NOT NULL;

ALTER TABLE objects
ADD CONSTRAINT objects_storage_key_unique UNIQUE (storage_key);

-- +goose Down
SELECT 'down SQL query';
