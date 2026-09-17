package delivery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestWorkerProcessDeliveryMock(t *testing.T) {
	// This test requires postgres+redis; skip if not available
	if testing.Short() {
		t.Skip("skip integration")
	}
	// Try to connect to test DB via env or default
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
	// spin mock endpoint that returns 200
	var received bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Webhook-ID") == "" {
			t.Errorf("missing X-Webhook-ID")
		}
		if r.Header.Get("X-Webhook-Signature") == "" {
			t.Errorf("missing signature")
		}
		received = true
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	// create minimal project/endpoint/event/delivery via DB
	// Use helper to ensure worker can deliver
	// For RED phase, worker not implemented — this test should fail
	worker := NewWorker(pool, rdb, &WorkerConfig{
		Concurrency:    1,
		Timeout:        5 * time.Second,
		MaxRetries:     5,
		AllowPrivate:   true,
		EncryptionKey:  []byte("0123456789abcdef0123456789abcdef"),
	})
	if worker == nil {
		t.Fatalf("worker should not be nil")
	}
	// This will be implemented to actually process a deliveryID
	t.Fatalf("worker not yet implemented — RED gate")
}
