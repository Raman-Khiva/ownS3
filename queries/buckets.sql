
-- name: ExistsBucketByName :one
SELECT EXISTS (SELECT 1 FROM buckets WHERE name = $1);

-- name: CreateBucket :one
INSERT INTO buckets (name)
VALUES ($1)
RETURNING *;

-- name: GetBucketByName :one
SELECT * FROM buckets WHERE name=$1;

-- name: GetBuckets :many
SELECT name, created_at
FROM buckets
ORDER BY name;

-- name: IsBucketEmpty :one
SELECT NOT EXISTS (SELECT 1 FROM objects WHERE bucket_id= $1);

-- name: DeleteBucketByName :exec
DELETE FROM buckets Where name = $1;
