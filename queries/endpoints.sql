-- name: CreateEndpoint :one
INSERT INTO endpoints (id, project_id, name, url, secret_encrypted, secret_version, timeout_ms) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: GetEndpoint :one
SELECT * FROM endpoints WHERE id = $1 AND project_id = $2 LIMIT 1;

-- name: ListEndpointsByProject :many
SELECT * FROM endpoints WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: UpdateEndpoint :one
UPDATE endpoints SET name = $2, url = $3, timeout_ms = $4, status = $5, updated_at = NOW() WHERE id = $1 AND project_id = $6 RETURNING *;

-- name: DeleteEndpoint :exec
DELETE FROM endpoints WHERE id = $1 AND project_id = $2;

-- name: GetEndpointForDelivery :one
SELECT * FROM endpoints WHERE id = $1 LIMIT 1;
