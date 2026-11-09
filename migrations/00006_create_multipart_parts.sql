-- +goose Up
CREATE TABLE multipart_parts(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    upload_id UUID NOT NULL REFERENCES multipart_uploads(id) ON DELETE CASCADE,

    part_number INTEGER NOT NULL,

   storage_key TEXT NOT NULL UNIQUE,

  size_bytes BIGINT NOT NULL,

  etag TEXT NOT NULL,

  uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),


 UNIQUE(upload_id, part_number),


);
-- +goose Down
DROP TABLE multipart_uploads;
