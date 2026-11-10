
-- name: ExistsBucketByName :one
SELECT EXISTS (SELECT 1 FROM buckets WHERE name = $1);

-- name: CreateBucket :one
INSERT INTO buckets (name)
VALUES ($1)
RETURNING *;

-- name: GetBucketByName :one
SELECT * FROM buckets WHERE name=$1;
