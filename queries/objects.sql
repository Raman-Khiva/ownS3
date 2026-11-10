

-- name: ObjectExsits :one
SELECT EXISTS (SELECT 1 FROM objects WHERE object_key=$1 AND bucket_id=$2);


-- name: CreateObject :one
INSERT INTO objects (bucket_id,object_key, storage_key, size_bytes)
VALUES ($1,$2, $3, $4)
RETURNING *;

-- name: GetObjectsByBucket :many
SELECT * FROM objects WHERE bucket_id=$1;


-- name: GetObjectByBucketAndKey :one
SELECT * FROM objects
WHERE bucket_id=$1 AND object_key=$2;

-- name: DeleteObjectByBucketAndKey :exec
DELETE FROM objects
WHERE bucket_id=$1 AND object_key=$2;
