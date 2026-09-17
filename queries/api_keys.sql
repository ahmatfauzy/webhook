-- name: CreateAPIKey :one
INSERT INTO api_keys (id, project_id, name, key_prefix, key_hash, expires_at) VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = $1 AND revoked_at IS NULL LIMIT 1;

-- name: ListAPIKeysByProject :many
SELECT id, project_id, name, key_prefix, last_used_at, expires_at, created_at, revoked_at FROM api_keys WHERE project_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC;

-- name: RevokeAPIKey :exec
UPDATE api_keys SET revoked_at = NOW() WHERE id = $1 AND project_id = $2;

-- name: UpdateAPIKeyLastUsed :exec
UPDATE api_keys SET last_used_at = NOW() WHERE id = $1;
