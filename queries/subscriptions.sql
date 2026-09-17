-- name: CreateSubscription :one
INSERT INTO subscriptions (id, endpoint_id, event_type) VALUES ($1, $2, $3) RETURNING *;

-- name: DeleteSubscription :exec
DELETE FROM subscriptions WHERE endpoint_id = $1 AND event_type = $2;

-- name: ListSubscriptionsByEndpoint :many
SELECT * FROM subscriptions WHERE endpoint_id = $1;

-- name: FindEndpointsForEventType :many
SELECT e.* FROM endpoints e
JOIN subscriptions s ON s.endpoint_id = e.id
WHERE e.project_id = $1 AND s.event_type = $2 AND e.status = 'active';

-- name: DeleteSubscriptionsByEndpoint :exec
DELETE FROM subscriptions WHERE endpoint_id = $1;
