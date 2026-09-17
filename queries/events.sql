-- name: CreateEvent :one
INSERT INTO events (id, project_id, type, payload) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetEvent :one
SELECT * FROM events WHERE id = $1 AND project_id = $2 LIMIT 1;

-- name: ListEventsByProject :many
SELECT * FROM events WHERE project_id = $1 AND ($2::text IS NULL OR type = $2) AND ($3::text IS NULL OR id > $3) ORDER BY id ASC LIMIT $4;

-- name: ListEventsByProjectCursor :many
SELECT * FROM events WHERE project_id = $1 AND id > $2 ORDER BY id ASC LIMIT $3;
