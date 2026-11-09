-- +goose Up
CREATE TABLE objects(
    id UUID PRIMARY KEY default gen_random_uuid(),

    bucket_id UUID NOT NULL REFERENCES buckets(id),

    object_key TEXT NOT NULL,

    storage_key TEXT NOT NULL UNIQUE,

    size_bytes BIGINT NOT NULL DEFAULT 0,

    etag TEXT,
    content_type TEXT,

    checksum_algorithm VARCHAR(20),
    checksum_value TEXT,

    metadata JSONB NOT NULL DEFAULT '{}',

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE(bucket_id, object_key)

);

-- +goose Down
DROP TABLE objects;
