-- name: CreateDelivery :one
INSERT INTO deliveries (id, event_id, endpoint_id, status, next_attempt_at) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetDelivery :one
SELECT * FROM deliveries WHERE id = $1 LIMIT 1;

-- name: GetDeliveryWithProjectCheck :one
SELECT d.* FROM deliveries d JOIN events e ON e.id = d.event_id WHERE d.id = $1 AND e.project_id = $2 LIMIT 1;

-- name: ListDeliveriesByProject :many
SELECT d.* FROM deliveries d JOIN events e ON e.id = d.event_id WHERE e.project_id = $1 ORDER BY d.created_at DESC LIMIT $2 OFFSET $3;

-- name: UpdateDeliveryStatus :one
UPDATE deliveries SET status = $2, attempt_count = $3, next_attempt_at = $4, delivered_at = $5, updated_at = NOW() WHERE id = $1 RETURNING *;

-- name: PollRetryingDeliveries :many
SELECT * FROM deliveries WHERE status = 'retrying' AND next_attempt_at <= NOW() ORDER BY next_attempt_at ASC LIMIT $1 FOR UPDATE SKIP LOCKED;
