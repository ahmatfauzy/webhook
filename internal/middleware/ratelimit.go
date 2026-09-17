package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimit is per-project + per-API-key sliding window via Redis.
// For MVP, uses simple INCR + EXPIRE per second window (fixed window), not full sliding.
// Key: rl:{project_id} or rl:{api_key_id}
func RateLimit(rdb *redis.Client, rps int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rdb == nil || rps <= 0 {
				next.ServeHTTP(w, r)
				return
			}
			// skip health
			switch r.URL.Path {
			case "/health", "/ready", "/metrics":
				next.ServeHTTP(w, r)
				return
			}
			projectID := GetProjectID(r.Context())
			if projectID == "" {
				// admin or unauthenticated, use IP
				projectID = r.RemoteAddr
			}
			keyID, _ := r.Context().Value(APIKeyIDKey).(string)
			// priority API key -> project
			key := fmt.Sprintf("rl:%s", projectID)
			if keyID != "" {
				key = fmt.Sprintf("rl:key:%s", keyID)
			}
			ctx, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
			defer cancel()
			// fixed window 1s
			count, err := rdb.Incr(ctx, key).Result()
			if err != nil {
				// fail open on redis error
				next.ServeHTTP(w, r)
				return
			}
			if count == 1 {
				_ = rdb.Expire(ctx, key, time.Second).Err()
			}
			if int(count) > rps {
				w.Header().Set("Retry-After", "1")
				WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
