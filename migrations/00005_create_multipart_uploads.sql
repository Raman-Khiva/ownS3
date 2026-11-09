-- +goose Up
CREATE TABLE multipart_uploads(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    bucket_id UUID NOT NULL REFERENCES buckets(id),

    object_key TEXT NOT NULL,

    upload_id VARCHAR(128) NOT NULL UNIQUE,

    initiated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    expires_at TIMESTAMPTZ
);

-- +goose Down
DROP TABLE multipart_uploads;
