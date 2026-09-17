package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"webhooker/internal/crypto"
	webhookULID "webhooker/internal/ulid"
)

type WorkerConfig struct {
	Concurrency    int
	Timeout        time.Duration
	MaxRetries     int
	RetrySchedule  []time.Duration
	MaxDelay       time.Duration
	Jitter         float64
	AllowPrivate   bool
	Allowlist      []string
	EncryptionKey  []byte
}

type Worker struct {
	pool   *pgxpool.Pool
	rdb    *redis.Client
	cfg    *WorkerConfig
	client *http.Client
	logger *slog.Logger
}

func NewWorker(pool *pgxpool.Pool, rdb *redis.Client, cfg *WorkerConfig) *Worker {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 20
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 5
	}
	if cfg.MaxDelay == 0 {
		cfg.MaxDelay = 6 * time.Hour
	}
	client := &http.Client{
		Timeout: cfg.Timeout,
	}
	w := &Worker{
		pool:   pool,
		rdb:    rdb,
		cfg:    cfg,
		client: client,
		logger: slog.Default(),
	}
	if w.logger != nil {
		w.logger.Info("worker config", "schedule", cfg.RetrySchedule, "maxRetries", cfg.MaxRetries, "maxDelay", cfg.MaxDelay, "jitter", cfg.Jitter, "allowPrivate", cfg.AllowPrivate)
	}
	return w
}

// ProcessDelivery processes a single delivery by ID (used by tests and poller)
func (w *Worker) ProcessDelivery(ctx context.Context, deliveryID string) error {
	// Load delivery with lock
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var delivery struct {
		ID           string
		EventID      string
		EndpointID   string
		Status       string
		AttemptCount int
	}
	err = tx.QueryRow(ctx, `SELECT id, event_id, endpoint_id, status, attempt_count FROM deliveries WHERE id=$1 FOR UPDATE`, deliveryID).Scan(&delivery.ID, &delivery.EventID, &delivery.EndpointID, &delivery.Status, &delivery.AttemptCount)
	if err != nil {
		return fmt.Errorf("load delivery: %w", err)
	}
	if delivery.Status == "delivered" || delivery.Status == "dead_letter" {
		_ = tx.Commit(ctx)
		return nil
	}
	// mark processing
	_, err = tx.Exec(ctx, `UPDATE deliveries SET status='processing', updated_at=NOW() WHERE id=$1`, deliveryID)
	if err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}
	// load event
	var evtID, evtType string
	var payload json.RawMessage
	var evtCreated time.Time
	err = tx.QueryRow(ctx, `SELECT id, type, payload, created_at FROM events WHERE id=$1`, delivery.EventID).Scan(&evtID, &evtType, &payload, &evtCreated)
	if err != nil {
		_ = tx.Commit(ctx)
		return fmt.Errorf("load event: %w", err)
	}
	// load endpoint
	var epURL, encSecret string
	var timeoutMs int
	var epStatus string
	err = tx.QueryRow(ctx, `SELECT url, secret_encrypted, timeout_ms, status FROM endpoints WHERE id=$1`, delivery.EndpointID).Scan(&epURL, &encSecret, &timeoutMs, &epStatus)
	if err != nil {
		_ = tx.Commit(ctx)
		return fmt.Errorf("load endpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit processing: %w", err)
	}

	// decrypt secret
	secret, err := crypto.Decrypt(w.cfg.EncryptionKey, encSecret)
	if err != nil {
		// treat as dead_letter if cannot decrypt
		w.recordFailure(ctx, deliveryID, delivery.AttemptCount+1, 0, fmt.Sprintf("decrypt failed: %v", err), 0, nil, nil, false)
		w.updateDeliveryAfterFailure(ctx, deliveryID, 0, err.Error(), false)
		return fmt.Errorf("decrypt: %w", err)
	}
	// SSRF check
	if err := ValidateURL(epURL, w.cfg.AllowPrivate, w.cfg.Allowlist); err != nil {
		w.recordFailure(ctx, deliveryID, delivery.AttemptCount+1, 0, fmt.Sprintf("ssrf blocked: %v", err), 0, nil, nil, false)
		w.updateDeliveryAfterFailure(ctx, deliveryID, 0, err.Error(), false)
		return fmt.Errorf("ssrf: %w", err)
	}

	// build webhook body
	bodyObj := map[string]interface{}{
		"id":         evtID,
		"type":       evtType,
		"created_at": evtCreated.UTC().Format(time.RFC3339),
		"data":       json.RawMessage(payload),
	}
	bodyBytes, err := json.Marshal(bodyObj)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := Sign(secret, timestamp, string(bodyBytes))

	// HTTP request
	reqCtx, cancel := context.WithTimeout(ctx, w.cfg.Timeout)
	if timeoutMs > 0 {
		// override with endpoint timeout if set
		reqCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	}
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", epURL, bytes.NewReader(bodyBytes))
	if err != nil {
		w.recordFailure(ctx, deliveryID, delivery.AttemptCount+1, 0, err.Error(), 0, nil, nil, true)
		w.updateDeliveryAfterFailure(ctx, deliveryID, 0, err.Error(), true)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Webhooker/0.1")
	req.Header.Set("X-Webhook-ID", evtID)
	req.Header.Set("X-Webhook-Timestamp", timestamp)
	req.Header.Set("X-Webhook-Signature", signature)

	start := time.Now()
	resp, err := w.client.Do(req)
	duration := time.Since(start)
	durationMs := int(duration.Milliseconds())

	var httpStatus int
	var respHeaders map[string]string
	var respErr string
	var isTimeout bool
	if err != nil {
		// check timeout
		if ctx.Err() == context.DeadlineExceeded || reqCtx.Err() == context.DeadlineExceeded {
			isTimeout = true
		}
		respErr = err.Error()
		// record attempt
		w.recordAttempt(ctx, deliveryID, delivery.AttemptCount+1, "failed", 0, durationMs, respErr, bodyBytes, timestamp, signature, epURL)
		w.updateDeliveryAfterFailure(ctx, deliveryID, 0, respErr, isTimeout)
		return err
	}
	defer resp.Body.Close()
	httpStatus = resp.StatusCode
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	respHeaders = map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}
	_ = respHeaders
	// determine success
	if httpStatus >= 200 && httpStatus < 300 {
		// success
		w.recordAttempt(ctx, deliveryID, delivery.AttemptCount+1, "delivered", httpStatus, durationMs, "", bodyBytes, timestamp, signature, epURL)
		_, _ = w.pool.Exec(ctx, `UPDATE deliveries SET status='delivered', attempt_count=$2, delivered_at=NOW(), updated_at=NOW() WHERE id=$1`, deliveryID, delivery.AttemptCount+1)
		if w.logger != nil {
			w.logger.Info("webhook delivered", "delivery_id", deliveryID, "endpoint_id", delivery.EndpointID, "status", httpStatus, "duration_ms", durationMs)
		}
		return nil
	}
	// failure
	isRetry := IsRetryable(nil, httpStatus, false)
	// for 429, check Retry-After? For now, we treat as retryable
	w.recordAttempt(ctx, deliveryID, delivery.AttemptCount+1, "failed", httpStatus, durationMs, fmt.Sprintf("http %d", httpStatus), bodyBytes, timestamp, signature, epURL)
	w.updateDeliveryAfterFailure(ctx, deliveryID, httpStatus, fmt.Sprintf("http %d", httpStatus), isRetry)
	if w.logger != nil {
		w.logger.Info("webhook failed", "delivery_id", deliveryID, "status", httpStatus, "retryable", isRetry, "duration_ms", durationMs)
	}
	return fmt.Errorf("http %d", httpStatus)
}

func (w *Worker) recordAttempt(ctx context.Context, deliveryID string, attemptNum int, status string, httpStatus int, durationMs int, errStr string, bodyBytes []byte, ts, sig, url string) {
	id := webhookULID.Generate("att_")
	reqHeaders, _ := json.Marshal(map[string]string{
		"Content-Type":       "application/json",
		"X-Webhook-ID":       deliveryID,
		"X-Webhook-Timestamp": ts,
		"X-Webhook-Signature": sig,
		"User-Agent":         "Webhooker/0.1",
	})
	// we need http status etc. For simplicity store errStr in error column
	_, _ = w.pool.Exec(ctx, `INSERT INTO delivery_attempts (id, delivery_id, attempt_number, status, http_status, request_headers, response_body, error, duration_ms) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, deliveryID, attemptNum, status, httpStatus, reqHeaders, nil, errStr, durationMs)
	_ = bodyBytes
	_ = url
}

func (w *Worker) recordFailure(ctx context.Context, deliveryID string, attemptNum int, httpStatus int, errStr string, durationMs int, reqHeaders, respHeaders interface{}, isRetry bool) {
	// helper for decrypt/ssrf failures
	w.recordAttempt(ctx, deliveryID, attemptNum, "failed", httpStatus, durationMs, errStr, nil, "", "", "")
	_ = isRetry
}

func (w *Worker) updateDeliveryAfterFailure(ctx context.Context, deliveryID string, httpStatus int, errStr string, isRetry bool) {
	// load attempt count
	var attemptCount int
	_ = w.pool.QueryRow(ctx, `SELECT attempt_count FROM deliveries WHERE id=$1`, deliveryID).Scan(&attemptCount)
	// increment already? Actually ProcessDelivery already has old count, new count = old+1
	// we need to fetch current after recordAttempt? For simplicity, increment
	// Determine if should retry
	shouldRetry := false
	if isRetry {
		// check IsRetryable via httpStatus
		if IsRetryable(nil, httpStatus, isRetry) || isRetry {
			// also check max retries
			if attemptCount+1 < w.cfg.MaxRetries {
				shouldRetry = true
			} else {
				// need to check current attempt_count +1 vs MaxRetries
				// we have not yet updated attempt_count, so check
				shouldRetry = attemptCount+1 < w.cfg.MaxRetries
			}
		}
	} else {
		// check via IsRetryable
		if IsRetryable(nil, httpStatus, false) {
			shouldRetry = attemptCount+1 < w.cfg.MaxRetries
		}
	}
	// For non-retryable, directly dead_letter
	if IsNonRetryable(httpStatus) {
		shouldRetry = false
	}
	// Special case: if httpStatus ==0 and isRetry true (network), retry
	if httpStatus == 0 && isRetry {
		shouldRetry = (attemptCount+1) < w.cfg.MaxRetries
	}

	if shouldRetry {
		nextDelay := NextAttempt(w.cfg.RetrySchedule, attemptCount, w.cfg.MaxDelay, w.cfg.Jitter)
		nextAt := time.Now().Add(nextDelay)
		_, _ = w.pool.Exec(ctx, `UPDATE deliveries SET status='retrying', attempt_count=attempt_count+1, next_attempt_at=$2, updated_at=NOW() WHERE id=$1`, deliveryID, nextAt)
	} else {
		_, _ = w.pool.Exec(ctx, `UPDATE deliveries SET status='dead_letter', attempt_count=attempt_count+1, updated_at=NOW() WHERE id=$1`, deliveryID)
	}
}

func (w *Worker) Run(ctx context.Context) error {
	// ensure stream
	if err := w.rdb.XGroupCreateMkStream(ctx, "webhooker:deliveries", "webhook-workers", "0").Err(); err != nil {
		if err.Error() != "BUSYGROUP Consumer Group name already exists" && !contains(err.Error(), "BUSYGROUP") {
			// ignore
		}
	}
	// poll retrying deliveries that are due
	go w.pollRetrying(ctx)
	// consume stream
	consumer := fmt.Sprintf("worker-%d", time.Now().UnixNano())
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		streams, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    "webhook-workers",
			Consumer: consumer,
			Streams:  []string{"webhooker:deliveries", ">"},
			Count:    10,
			Block:    5 * time.Second,
		}).Result()
		if err != nil && err != redis.Nil {
			if ctx.Err() != nil {
				return nil
			}
			time.Sleep(time.Second)
			continue
		}
		for _, stream := range streams {
			for _, msg := range stream.Messages {
				deliveryID, _ := msg.Values["delivery_id"].(string)
				if deliveryID == "" {
					_, _ = w.rdb.XAck(ctx, "webhooker:deliveries", "webhook-workers", msg.ID).Result()
					continue
				}
				// process with concurrency limit? For MVP, sequential per worker
				_ = w.ProcessDelivery(ctx, deliveryID)
				_, _ = w.rdb.XAck(ctx, "webhooker:deliveries", "webhook-workers", msg.ID).Result()
			}
		}
		// also handle pending PEL via XAUTOCLAIM
		w.claimPending(ctx, consumer)
	}
}

func (w *Worker) pollRetrying(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// poll deliveries where status=retrying and next_attempt_at <= now
			rows, err := w.pool.Query(ctx, `SELECT id FROM deliveries WHERE status='retrying' AND next_attempt_at <= NOW() ORDER BY next_attempt_at ASC LIMIT 100 FOR UPDATE SKIP LOCKED`)
			if err != nil {
				if w.logger != nil {
					w.logger.Error("poll retrying query failed", "error", err)
				}
				continue
			}
			var ids []string
			for rows.Next() {
				var id string
				_ = rows.Scan(&id)
				ids = append(ids, id)
			}
			rows.Close()
			if len(ids) > 0 && w.logger != nil {
				w.logger.Info("poll retrying found", "count", len(ids), "ids", ids)
			}
			for _, id := range ids {
				// push back to stream
				_, err := w.rdb.XAdd(ctx, &redis.XAddArgs{
					Stream: "webhooker:deliveries",
					Values: map[string]interface{}{"delivery_id": id},
				}).Result()
				if err != nil && w.logger != nil {
					w.logger.Error("XAdd retry failed", "id", id, "error", err)
				} else if w.logger != nil {
					w.logger.Info("re-queued retry", "delivery_id", id)
				}
			}
		}
	}
}

func (w *Worker) claimPending(ctx context.Context, consumer string) {
	// XAUTOCLAIM idle >30s
	msgs, _, err := w.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   "webhooker:deliveries",
		Group:    "webhook-workers",
		Consumer: consumer,
		MinIdle:  30 * time.Second,
		Start:    "0-0",
		Count:    10,
	}).Result()
	if err != nil {
		return
	}
	for _, msg := range msgs {
		deliveryID, _ := msg.Values["delivery_id"].(string)
		if deliveryID != "" {
			_ = w.ProcessDelivery(ctx, deliveryID)
			_, _ = w.rdb.XAck(ctx, "webhooker:deliveries", "webhook-workers", msg.ID).Result()
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	}()
}

// ensure pgx import used
var _ = pgx.ErrNoRows
