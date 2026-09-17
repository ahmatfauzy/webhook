# TDD Evidence — Worker Delivery

## Source plan
- PRD sections for delivery, retry, signature, SSRF, queue/worker, observability
- No external plan file; journeys derived from grilling decisions (MIT, strict isolation, HMAC, retry schedule, stream topology)

## User journeys
1. As worker, I consume pending deliveries via Redis Streams consumer group so that each event is delivered to its subscribed endpoint.
2. As system, I sign webhook requests with HMAC so that consumers can verify authenticity.
3. As worker, I retry on transient failures with backoff and respect Retry-After, otherwise mark dead letter.
4. As system, I block SSRF to private/metadata IPs unless explicitly allowed for local dev.

## Task report

| Task | Summary | Validation command | Output excerpt | Guarantee |
|------|---------|-------------------|----------------|-----------|
| Signer | HMAC v1 deterministic, verifies timestamp+body | `go test ./internal/delivery -run TestSign` | `ok 0.014s` | Signature is `v1=` hex HMAC and verification fails on wrong secret/timestamp/body |
| Retry | Retryable codes and backoff schedule | `go test ./internal/delivery -run TestIsRetryable` | `ok 0.011s` | 408/409/429/5xx retry, 400/401/403/404/422 not, jitter within ±20% |
| SSRF | Private/metadata blocking, allowPrivate override | `go test ./internal/delivery -run TestValidateURL` | `ok 0.006s` | localhost/127/10/192.168/169.254 blocked when allowPrivate false, allowed when true |
| Worker | End-to-end delivery to mock HTTP 200, signature verified | `go test ./internal/delivery -run TestWorkerProcessDeliveryMock -count=1 -v` | `PASS 0.08s` + `INFO webhook delivered delivery_id=del_... status=200` | Worker decrypts secret, validates URL, builds X-Webhook-* headers, posts, records attempt, marks delivered, signature matches |
| E2E | Manual retry with mock 500→200, schedule 2s | `bash /tmp/check_e2e_retry2.sh` (server+worker+mock) | `webhook failed status=500 retryable=true` → `webhook delivered status=200 after 2s` + `attempt_count=2` | Retry schedule and poll re-queue work, second attempt succeeds, dead_letter not yet triggered |

**RED evidence:** `go test ./internal/delivery -run TestValidateURL` initially `undefined: ValidateURL` build failed (compile-time RED). `TestWorkerProcessDeliveryMock` failed with `undefined: NewWorker` and `t.Fatalf RED gate` before implementation.

**GREEN evidence:** After implementing `ssrf.go` and `worker.go`, same commands `ok` with `PASS` and delivery logs.

## Test specification

| # | What is guaranteed | Test file or command | Type | Result | Evidence |
|---|--------------------|----------------------|------|--------|----------|
| 1 | HMAC signature is v1 hex and verification is constant-time | `internal/delivery/signer_test.go:TestSignAndVerify` | unit | PASS | `go test -run TestSign` |
| 2 | 429/500/502/503/504 retry, 400/401/403/404/422 not, network/timeout retry | `internal/delivery/retry_test.go:TestIsRetryable` | unit | PASS | `go test -run TestIsRetryable` |
| 3 | Backoff uses explicit schedule 30s/2m then exponential capped at 6h, jitter ±20% | `internal/delivery/retry_test.go:TestNextAttemptSchedule` | unit | PASS | `go test -run TestNextAttemptSchedule` |
| 4 | Private IPs and metadata blocked, allowPrivate bypass works | `internal/delivery/ssrf_test.go:TestValidateURL` | unit | PASS | `go test -run TestValidateURL` |
| 5 | Worker delivers single pending delivery to mock 200, records attempt, updates to delivered | `internal/delivery/worker_test.go:TestWorkerProcessDeliveryMock` | integration | PASS | `go test -run TestWorkerProcessDeliveryMock -v` + worker log `delivered` |
| 6 | Worker retries 500→200 via poll and redis re-queue after 2s schedule | `manual E2E retry` | e2e | PASS | `worker log: failed 500 then delivered 200` + `SELECT status=delivered attempt_count=2` |

## Coverage and known gaps

```
go test ./internal/delivery -cover
ok  	webhooker/internal/delivery	0.105s	coverage: 54.2% of statements
```

- Covered: `Sign`, `Verify`, `IsRetryable`, `IsNonRetryable`, `NextAttempt`, `ValidateURL`, `ProcessDelivery` happy and retry paths, `recordAttempt`, `updateDeliveryAfterFailure`
- Not covered (intentional): `Worker.Run` loop, `pollRetrying`, `claimPending`, `XReadGroup` blocking path — exercised via manual E2E with mock server, not via unit `go test`. Overall repo coverage 0% for handlers/middleware etc., will increase with API integration tests.
- Threshold 80% not yet met for delivery package; will be addressed by adding tests for `Run` and `pollRetrying` with testcontainers in next iteration.

## Merge evidence

- Checkpoint commits on `main`:
  - `a5ad608 test: add reproducer for delivery worker, ssrf, signer, retry (RED)` — 4 test files, `go test` build failed
  - `c720654 fix: implement delivery worker, ssrf, and wire runWorker (GREEN)` — `go vet` ok, `go test` 7 PASS, `go build` ok, E2E simple 200 delivered
  - `40461a6 refactor: add worker config and poll logging for observability` — `go vet` ok, `go test` still PASS, E2E retry 500→200 delivered after 2s
- Squash allowed only after this evidence preserved; PR body should copy this table.

