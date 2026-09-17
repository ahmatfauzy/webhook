package middleware

import (
	"net/http"
	"strings"
)

// CORS handles Access-Control-Allow-Origin.
// AllowedOrigins empty => no CORS. "*" is rejected in production unless explicitly allowed.
func CORS(allowedOrigins []string, appEnv string) func(http.Handler) http.Handler {
	allowedSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowedSet[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			// preflight
			if r.Method == http.MethodOptions {
				if isAllowed(origin, allowedSet, appEnv) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID, Idempotency-Key")
					w.Header().Set("Access-Control-Max-Age", "86400")
					w.WriteHeader(http.StatusNoContent)
					return
				}
				// not allowed
				http.Error(w, "CORS not allowed", http.StatusForbidden)
				return
			}
			if isAllowed(origin, allowedSet, appEnv) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			} else if appEnv != "production" && len(allowedOrigins) == 0 {
				// in dev with no config, allow all for local dev? PRD says avoid * in prod, but dev may need
				// still, we don't auto allow; just pass through without header
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isAllowed(origin string, set map[string]bool, appEnv string) bool {
	if set[origin] {
		return true
	}
	if set["*"] {
		// wildcard is only respected when explicitly configured
		return true
	}
	// check wildcard subdomains? not needed
	_ = strings.Contains
	return false
}
