-- +goose Up
CREATE TABLE buckets(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(63)  NOT NULL UNIQUE,
    region VARCHAR(50) NOT NULL DEFAULT 'us-east-1',
    versioning_enabled BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE buckets;
