package middleware

import (
	"context"
	"net/http"

	webhookULID "webhooker/internal/ulid"
)

type ctxKey string

const RequestIDKey ctxKey = "request_id"

// RequestID ensures X-Request-ID header exists, generates req_ ULID if missing, and echoes it.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = webhookULID.Generate("req_")
		}
		// echo
		w.Header().Set("X-Request-ID", reqID)
		ctx := context.WithValue(r.Context(), RequestIDKey, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GetRequestID(ctx context.Context) string {
	if v, ok := ctx.Value(RequestIDKey).(string); ok {
		return v
	}
	return ""
}
