-- name: CreateDeliveryAttempt :one
INSERT INTO delivery_attempts (id, delivery_id, attempt_number, status, http_status, request_headers, response_headers, response_body, error, duration_ms) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING *;

-- name: ListDeliveryAttempts :many
SELECT * FROM delivery_attempts WHERE delivery_id = $1 ORDER BY attempt_number ASC;
