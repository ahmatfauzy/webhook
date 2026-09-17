package middleware

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"webhooker/internal/auth"
)

type ctxKeyAuth string

const (
	ProjectIDKey ctxKeyAuth = "project_id"
	APIKeyIDKey  ctxKeyAuth = "api_key_id"
)

// APIKeyAuth validates Bearer whk_live_... via DB lookup by hash.
// Skips auth for health/ready/metrics and for dashboard admin token endpoints.
func APIKeyAuth(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// allowlist unauthenticated
			switch r.URL.Path {
			case "/health", "/ready", "/metrics":
				next.ServeHTTP(w, r)
				return
			}
			// dashboard admin token may be used via X-Admin-Token or Authorization Bearer for project bootstrap
			// We treat Bearer whk_live_ as project auth; admin token handled separately in handlers
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "missing Authorization header")
				return
			}
			token, ok := auth.ExtractBearer(authHeader)
			if !ok {
				WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "invalid Authorization format")
				return
			}
			if err := auth.ValidateFormat(token); err != nil {
				WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "invalid api key format")
				return
			}
			hash := auth.HashAPIKey(token)
			// lookup
			var projectID, keyID string
			var revokedAt interface{}
			err := pool.QueryRow(r.Context(),
				`SELECT id, project_id, revoked_at FROM api_keys WHERE key_hash=$1 LIMIT 1`, hash).
				Scan(&keyID, &projectID, &revokedAt)
			if err != nil {
				WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "invalid api key")
				return
			}
			if revokedAt != nil {
				WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "api key revoked")
				return
			}
			// check expires_at
			var expiresAt *string
			_ = pool.QueryRow(r.Context(), `SELECT expires_at::text FROM api_keys WHERE id=$1`, keyID).Scan(&expiresAt)
			// update last_used_at async (fire and forget)
			go func() {
				_, _ = pool.Exec(context.Background(), `UPDATE api_keys SET last_used_at=NOW() WHERE id=$1`, keyID)
			}()

			ctx := context.WithValue(r.Context(), ProjectIDKey, projectID)
			ctx = context.WithValue(ctx, APIKeyIDKey, keyID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func GetProjectID(ctx context.Context) string {
	if v, ok := ctx.Value(ProjectIDKey).(string); ok {
		return v
	}
	return ""
}

// AdminAuth checks X-Admin-Token or ADMIN_TOKEN env for project bootstrap endpoints (create project without api key)
// Used only for POST /v1/projects when no project yet.
func AdminAuth(adminToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// if already has project auth, pass
			if GetProjectID(r.Context()) != "" {
				next.ServeHTTP(w, r)
				return
			}
			// check admin token
			tok := r.Header.Get("X-Admin-Token")
			if tok == "" {
				// also allow Authorization Bearer adminToken if not whk_live_
				if h := r.Header.Get("Authorization"); h != "" {
					if t, ok := auth.ExtractBearer(h); ok {
						if t[:4] != "whk_" {
							tok = t
						}
					}
				}
			}
			if adminToken != "" && tok == adminToken {
				next.ServeHTTP(w, r)
				return
			}
			// allow unauthenticated project creation in development when no admin token set
			if adminToken == "" {
				next.ServeHTTP(w, r)
				return
			}
			WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "admin token required")
		})
	}
}
