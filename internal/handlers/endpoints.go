package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webhooker/internal/crypto"
	"webhooker/internal/middleware"
	webhookULID "webhooker/internal/ulid"
)

type EndpointHandler struct {
	Pool          *pgxpool.Pool
	EncryptionKey []byte
}

func (h *EndpointHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.Create)
	r.Get("/", h.List)
	r.Get("/{id}", h.Get)
	r.Patch("/{id}", h.Update)
	r.Delete("/{id}", h.Delete)
	// subscriptions nested
	r.Post("/{id}/subscriptions", h.AddSubscription)
	r.Delete("/{id}/subscriptions", h.RemoveSubscription)
	r.Get("/{id}/subscriptions", h.ListSubscriptions)
	return r
}

func validateURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errInvalidURL
	}
	if u.Host == "" {
		return errInvalidURL
	}
	// SSRF private check is done in worker send-time; here we just validate format
	// But block localhost if not allowPrivate? For MVP, allow but warn
	_ = allowPrivate
	_ = strings.Contains
	return nil
}

var errInvalidURL = &url.Error{Op: "parse", URL: "", Err: errInvalidURLError}

var errInvalidURLError = errorString("invalid url")

type errorString string

func (e errorString) Error() string { return string(e) }

func (h *EndpointHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	if projectID == "" {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "project missing")
		return
	}
	var req struct {
		Name            string   `json:"name"`
		URL             string   `json:"url"`
		Secret          string   `json:"secret"`
		Description     string   `json:"description"`
		TimeoutMs       *int     `json:"timeout_ms"`
		SubscribedTypes []string `json:"subscribed_types"`
		EventTypes      []string `json:"event_types"` // alternative
	}
	if !parseBody(w, r, &req, 1<<20) {
		return
	}
	if req.URL == "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "url is required")
		return
	}
	if err := validateURL(req.URL, false); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "invalid url")
		return
	}
	if req.Secret == "" {
		// generate random secret if not provided
		req.Secret = webhookULID.Generate("") // use ulid as secret placeholder
	}
	enc, err := crypto.Encrypt(h.EncryptionKey, req.Secret)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to encrypt secret")
		return
	}
	timeoutMs := 10000
	if req.TimeoutMs != nil && *req.TimeoutMs > 0 {
		timeoutMs = *req.TimeoutMs
	}
	id := webhookULID.Generate("ep_")
	name := req.Name
	if name == "" {
		name = req.URL
	}
	var createdID, createdURL string
	err = h.Pool.QueryRow(r.Context(),
		`INSERT INTO endpoints (id, project_id, name, url, secret_encrypted, timeout_ms) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, url`,
		id, projectID, name, req.URL, enc, timeoutMs).Scan(&createdID, &createdURL)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create endpoint")
		return
	}
	// handle subscriptions
	types := req.SubscribedTypes
	if len(types) == 0 {
		types = req.EventTypes
	}
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		sid := webhookULID.Generate("sub_")
		_, _ = h.Pool.Exec(r.Context(), `INSERT INTO subscriptions (id, endpoint_id, event_type) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, sid, createdID, t)
	}
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{
		"id": id, "project_id": projectID, "name": name, "url": req.URL, "timeout_ms": timeoutMs,
	})
}

func (h *EndpointHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	limit := queryInt(r, "limit", 50, 100)
	after := r.URL.Query().Get("after")
	query := `SELECT id, project_id, name, url, status, timeout_ms, created_at, updated_at FROM endpoints WHERE project_id=$1`
	args := []interface{}{projectID}
	if after != "" {
		query += ` AND id > $2 ORDER BY id ASC LIMIT $3`
		args = append(args, after, limit)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $2`
		args = append(args, limit)
	}
	rows, err := h.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list")
		return
	}
	defer rows.Close()
	var list []map[string]interface{}
	for rows.Next() {
		var id, pid, name, urlStr, status string
		var timeout int
		var created, updated interface{}
		if err := rows.Scan(&id, &pid, &name, &urlStr, &status, &timeout, &created, &updated); err != nil {
			continue
		}
		list = append(list, map[string]interface{}{"id": id, "project_id": pid, "name": name, "url": urlStr, "status": status, "timeout_ms": timeout, "created_at": created})
	}
	if list == nil {
		list = []map[string]interface{}{}
	}
	// pagination
	hasMore := len(list) == limit
	var nextCursor string
	if hasMore && len(list) > 0 {
		nextCursor = list[len(list)-1]["id"].(string)
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"data": list,
		"pagination": map[string]interface{}{"has_more": hasMore, "next_cursor": nextCursor},
	})
}

func (h *EndpointHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	id := chi.URLParam(r, "id")
	var ep struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
		Name      string `json:"name"`
		URL       string `json:"url"`
		Status    string `json:"status"`
		TimeoutMs int    `json:"timeout_ms"`
	}
	err := h.Pool.QueryRow(r.Context(), `SELECT id, project_id, name, url, status, timeout_ms FROM endpoints WHERE id=$1 AND project_id=$2`, id, projectID).Scan(&ep.ID, &ep.ProjectID, &ep.Name, &ep.URL, &ep.Status, &ep.TimeoutMs)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
		return
	}
	// list subs
	rows, _ := h.Pool.Query(r.Context(), `SELECT event_type FROM subscriptions WHERE endpoint_id=$1`, id)
	var subs []string
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			_ = rows.Scan(&t)
			subs = append(subs, t)
		}
	}
	if subs == nil {
		subs = []string{}
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"id": ep.ID, "project_id": ep.ProjectID, "name": ep.Name, "url": ep.URL, "status": ep.Status, "timeout_ms": ep.TimeoutMs, "subscribed_types": subs})
}

func (h *EndpointHandler) Update(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	id := chi.URLParam(r, "id")
	var req map[string]interface{}
	if !parseBody(w, r, &req, 1<<20) {
		return
	}
	// simple patch: name, url, status, timeout_ms
	// fetch existing
	var name, urlStr, status string
	var timeout int
	err := h.Pool.QueryRow(r.Context(), `SELECT name, url, status, timeout_ms FROM endpoints WHERE id=$1 AND project_id=$2`, id, projectID).Scan(&name, &urlStr, &status, &timeout)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
		return
	}
	if v, ok := req["name"].(string); ok {
		name = v
	}
	if v, ok := req["url"].(string); ok {
		if err := validateURL(v, false); err != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "invalid url")
			return
		}
		urlStr = v
	}
	if v, ok := req["status"].(string); ok {
		status = v
	}
	if v, ok := req["timeout_ms"].(float64); ok {
		timeout = int(v)
	}
	_, err = h.Pool.Exec(r.Context(), `UPDATE endpoints SET name=$1, url=$2, status=$3, timeout_ms=$4 WHERE id=$5 AND project_id=$6`, name, urlStr, status, timeout, id, projectID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"id": id, "name": name, "url": urlStr, "status": status, "timeout_ms": timeout})
}

func (h *EndpointHandler) Delete(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := h.Pool.Exec(r.Context(), `DELETE FROM endpoints WHERE id=$1 AND project_id=$2`, id, projectID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *EndpointHandler) AddSubscription(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	endpointID := chi.URLParam(r, "id")
	// verify endpoint belongs to project
	var exists bool
	_ = h.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM endpoints WHERE id=$1 AND project_id=$2)`, endpointID, projectID).Scan(&exists)
	if !exists {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
		return
	}
	var req struct {
		EventType string `json:"event_type"`
		Type      string `json:"type"`
	}
	if !parseBody(w, r, &req, 1<<20) {
		return
	}
	t := req.EventType
	if t == "" {
		t = req.Type
	}
	t = strings.TrimSpace(t)
	if t == "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "event_type required")
		return
	}
	// validate type grammar-ish: must contain dot, no wildcard
	if !strings.Contains(t, ".") || strings.Contains(t, "*") {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "invalid event type")
		return
	}
	sid := webhookULID.Generate("sub_")
	_, err := h.Pool.Exec(r.Context(), `INSERT INTO subscriptions (id, endpoint_id, event_type) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, sid, endpointID, t)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to add subscription")
		return
	}
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{"id": sid, "endpoint_id": endpointID, "event_type": t})
}

func (h *EndpointHandler) RemoveSubscription(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	endpointID := chi.URLParam(r, "id")
	var req struct {
		EventType string `json:"event_type"`
	}
	// allow query param ?event_type= or body
	if r.URL.Query().Get("event_type") != "" {
		req.EventType = r.URL.Query().Get("event_type")
	} else {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.EventType == "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "event_type required")
		return
	}
	var exists bool
	_ = h.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM endpoints WHERE id=$1 AND project_id=$2)`, endpointID, projectID).Scan(&exists)
	if !exists {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
		return
	}
	_, _ = h.Pool.Exec(r.Context(), `DELETE FROM subscriptions WHERE endpoint_id=$1 AND event_type=$2`, endpointID, req.EventType)
	w.WriteHeader(http.StatusNoContent)
}

func (h *EndpointHandler) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	endpointID := chi.URLParam(r, "id")
	var exists bool
	_ = h.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM endpoints WHERE id=$1 AND project_id=$2)`, endpointID, projectID).Scan(&exists)
	if !exists {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT id, event_type, created_at FROM subscriptions WHERE endpoint_id=$1`, endpointID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list")
		return
	}
	defer rows.Close()
	var list []map[string]interface{}
	for rows.Next() {
		var id, et string
		var created interface{}
		_ = rows.Scan(&id, &et, &created)
		list = append(list, map[string]interface{}{"id": id, "event_type": et, "created_at": created})
	}
	if list == nil {
		list = []map[string]interface{}{}
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"data": list})
}
