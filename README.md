# Webhooker

> Open-source, self-hosted webhook infrastructure for reliable event delivery.

```
Project → API Key → Endpoint → Subscription → Event (202) → Delivery → Redis Stream
```

## Quick start

```bash
cp .env.example .env
docker compose up -d postgres redis
go run ./cmd/webhooker migrate
go run ./cmd/webhooker server &
# in dev, project creation allowed without admin token
curl -X POST http://localhost:8080/v1/projects -H "Content-Type: application/json" -d '{"name":"demo"}'
# => {"id":"proj_01J...","name":"demo"}

# create api key for bootstrap, use admin token to create first key:
curl -X POST http://localhost:8080/v1/api-keys \
  -H "Content-Type: application/json" \
  -H "X-Admin-Token: dev-admin-token" \
  -d '{"name":"default","project_id":"proj_01J..."}'
# => {"id":"ak_...","key":"whk_live_...","key_prefix":"whk_live_..."}

# event ingestion
curl -X POST http://localhost:8080/v1/events \
  -H "Authorization: Bearer whk_live_xxx" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: req_123" \
  -d '{"type":"order.created","data":{"order_id":"ord_123"}}'
# => 202 {"id":"evt_...","type":"order.created","status":"queued"}
```

Health:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
curl http://localhost:8080/metrics
```

## Config

All via env — see `.env.example`. `ENCRYPTION_KEY` is 32-byte base64, `WEBHOOK_ALLOW_PRIVATE_IPS=true` for local dev.

## Migrations

Run `make migrate` or `go run ./cmd/webhooker migrate`.
