package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webhooker/internal/auth"
	"webhooker/internal/middleware"
	webhookULID "webhooker/internal/ulid"
)

type APIKeyHandler struct {
	Pool       *pgxpool.Pool
	AdminToken string
}

func (h *APIKeyHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.Create)
	r.Get("/", h.List)
	r.Delete("/{id}", h.Revoke)
	return r
}

func (h *APIKeyHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	// bootstrap fallback: allow admin token to specify project_id in body
	var req struct {
		Name      string  `json:"name"`
		ExpiresAt *string `json:"expires_at"`
		ProjectID string  `json:"project_id"`
	}
	if !parseBody(w, r, &req, 1<<20) {
		// parseBody already wrote error, but we need projectID for admin fallback
		// we already consumed body, so we cannot re-read; just return
		return
	}
	if projectID == "" {
		// try admin token
		tok := r.Header.Get("X-Admin-Token")
		if tok == "" {
			if h2 := r.Header.Get("Authorization"); h2 != "" {
				if t, ok := extractBearer(h2); ok && t == h.AdminToken && h.AdminToken != "" {
					tok = t
				}
			}
		}
		if h.AdminToken != "" && tok == h.AdminToken && req.ProjectID != "" {
			projectID = req.ProjectID
			// verify project exists
			var exists bool
			_ = h.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, projectID).Scan(&exists)
			if !exists {
				writeError(w, r, http.StatusNotFound, "NOT_FOUND", "project not found")
				return
			}
		} else {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "project context missing")
			return
		}
	}
	if req.Name == "" {
		req.Name = "default"
	}
	plaintext, prefix, hash, err := auth.GenerateAPIKey()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to generate key")
		return
	}
	id := webhookULID.Generate("ak_")
	var expiresAt interface{}
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, *req.ExpiresAt); err == nil {
			expiresAt = t
		}
	}
	var createdID, createdPrefix string
	err = h.Pool.QueryRow(r.Context(),
		`INSERT INTO api_keys (id, project_id, name, key_prefix, key_hash, expires_at) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, key_prefix`,
		id, projectID, req.Name, prefix, hash, expiresAt).Scan(&createdID, &createdPrefix)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create api key")
		return
	}
	// return plaintext only once
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{
		"id":         createdID,
		"name":       req.Name,
		"key":        plaintext,
		"key_prefix": createdPrefix,
		"project_id": projectID,
	})
}

func (h *APIKeyHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, name, key_prefix, last_used_at, expires_at, created_at, revoked_at FROM api_keys WHERE project_id=$1 AND revoked_at IS NULL ORDER BY created_at DESC`, projectID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list")
		return
	}
	defer rows.Close()
	var list []map[string]interface{}
	for rows.Next() {
		var id, name, prefix string
		var lastUsed, expires, created, revoked interface{}
		if err := rows.Scan(&id, &name, &prefix, &lastUsed, &expires, &created, &revoked); err != nil {
			continue
		}
		list = append(list, map[string]interface{}{
			"id": id, "name": name, "key_prefix": prefix, "last_used_at": lastUsed, "expires_at": expires, "created_at": created,
		})
	}
	if list == nil {
		list = []map[string]interface{}{}
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"data": list})
}

func extractBearer(h string) (string, bool) {
	const bearer = "Bearer "
	if len(h) <= len(bearer) {
		return "", false
	}
	if h[:len(bearer)] != bearer {
		return "", false
	}
	return h[len(bearer):], true
}

func (h *APIKeyHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := h.Pool.Exec(r.Context(), `UPDATE api_keys SET revoked_at=NOW() WHERE id=$1 AND project_id=$2 AND revoked_at IS NULL`, id, projectID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to revoke")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "api key not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
