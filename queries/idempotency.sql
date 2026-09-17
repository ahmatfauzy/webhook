-- name: CreateIdempotencyKey :one
INSERT INTO idempotency_keys (id, project_id, key, event_id, expires_at) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE project_id = $1 AND key = $2 AND expires_at > NOW() LIMIT 1;

-- name: DeleteExpiredIdempotencyKeys :exec
DELETE FROM idempotency_keys WHERE expires_at <= NOW();
