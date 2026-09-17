package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"webhooker/internal/crypto"
	webhookULID "webhooker/internal/ulid"
)

func TestWorkerProcessDeliveryMock(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration")
	}
	url := "postgres://webhooker:webhooker@localhost:5433/webhooker?sslmode=disable"
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("no db: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("no db ping: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6380"})
	defer rdb.Close()
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("no redis: %v", err)
	}

	// mock endpoint
	var gotID, gotSig, gotTS string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("X-Webhook-ID")
		gotSig = r.Header.Get("X-Webhook-Signature")
		gotTS = r.Header.Get("X-Webhook-Timestamp")
		var err error
		gotBody, err = json.Marshal(map[string]interface{}{})
		// read body
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = buf[:n]
		if err != nil {
			_ = err
		}
		if gotID == "" {
			t.Errorf("missing X-Webhook-ID")
		}
		if gotSig == "" {
			t.Errorf("missing signature")
		}
		if gotTS == "" {
			t.Errorf("missing timestamp")
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	// create test project/endpoint/event/delivery
	ctx := context.Background()
	projID := webhookULID.Generate("proj_")
	_, err = pool.Exec(ctx, `INSERT INTO projects (id, name) VALUES ($1,$2)`, projID, "test-proj")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, projID)
	})

	encKey := []byte("0123456789abcdef0123456789abcdef")
	secret := "test-secret"
	encSecret, err := crypto.Encrypt(encKey, secret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	epID := webhookULID.Generate("ep_")
	_, err = pool.Exec(ctx, `INSERT INTO endpoints (id, project_id, name, url, secret_encrypted, timeout_ms) VALUES ($1,$2,$3,$4,$5,$6)`, epID, projID, "test-ep", srv.URL, encSecret, 5000)
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	evtID := webhookULID.Generate("evt_")
	_, err = pool.Exec(ctx, `INSERT INTO events (id, project_id, type, payload) VALUES ($1,$2,$3,$4)`, evtID, projID, "order.created", json.RawMessage(`{"order_id":"ord_123"}`))
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	delID := webhookULID.Generate("del_")
	_, err = pool.Exec(ctx, `INSERT INTO deliveries (id, event_id, endpoint_id, status) VALUES ($1,$2,$3,'pending')`, delID, evtID, epID)
	if err != nil {
		t.Fatalf("create delivery: %v", err)
	}

	worker := NewWorker(pool, rdb, &WorkerConfig{
		Concurrency:   1,
		Timeout:       5 * time.Second,
		MaxRetries:    5,
		RetrySchedule: []time.Duration{30 * time.Second, 2 * time.Minute},
		MaxDelay:      6 * time.Hour,
		Jitter:        0,
		AllowPrivate:  true,
		EncryptionKey: encKey,
	})
	if worker == nil {
		t.Fatalf("worker should not be nil")
	}
	// process delivery
	if err := worker.ProcessDelivery(ctx, delID); err != nil {
		t.Fatalf("ProcessDelivery failed: %v", err)
	}
	// verify delivery became delivered
	var status string
	var attempts int
	err = pool.QueryRow(ctx, `SELECT status, attempt_count FROM deliveries WHERE id=$1`, delID).Scan(&status, &attempts)
	if err != nil {
		t.Fatalf("query delivery: %v", err)
	}
	if status != "delivered" {
		t.Fatalf("expected delivered, got %s", status)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
	// verify signature
	if gotID != evtID {
		t.Fatalf("X-Webhook-ID mismatch: got %s want %s", gotID, evtID)
	}
	expectedSig := Sign(secret, gotTS, string(gotBody))
	if gotSig != expectedSig {
		t.Fatalf("signature mismatch: got %s want %s body=%s ts=%s", gotSig, expectedSig, string(gotBody), gotTS)
	}
	// verify attempt recorded
	var count int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM delivery_attempts WHERE delivery_id=$1`, delID).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 attempt record, got %d", count)
	}
}
