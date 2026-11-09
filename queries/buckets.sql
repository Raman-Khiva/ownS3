

-- name: ExistsBucketByName :one
SELECT EXISTS (SELECT 1 FROM buckets WHERE name = $1);
