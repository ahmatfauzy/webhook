-- name: CreateProject :one
INSERT INTO projects (id, name, description) VALUES ($1, $2, $3) RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1 LIMIT 1;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: UpdateProject :one
UPDATE projects SET name = $2, description = $3 WHERE id = $1 RETURNING *;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = $1;
