package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func Health() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

func Ready(pool *pgxpool.Pool, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		pgOK := pool.Ping(ctx) == nil
		redisOK := true
		if rdb != nil {
			redisOK = rdb.Ping(ctx).Err() == nil
		}
		if !pgOK || !redisOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not ready"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready","postgres":"ok","redis":"ok"}`))
	}
}

func Metrics() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// minimal prometheus metrics — real metrics wired later
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`# HELP webhooker_events_total Total events ingested
# TYPE webhooker_events_total counter
webhooker_events_total 0
# HELP webhooker_deliveries_total Total deliveries
# TYPE webhooker_deliveries_total counter
webhooker_deliveries_total 0
`))
	}
}
