package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"webhooker/internal/config"
	"webhooker/internal/middleware"
	"webhooker/internal/queue"
	webhookULID "webhooker/internal/ulid"
)

var eventTypeRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

type EventHandler struct {
	Pool   *pgxpool.Pool
	Redis  *redis.Client
	Config *config.Config
}

func (h *EventHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.Create)
	r.Get("/", h.List)
	r.Get("/{id}", h.Get)
	return r
}

type createEventReq struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (h *EventHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	if projectID == "" {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "project missing")
		return
	}
	// Idempotency-Key header
	idemKey := r.Header.Get("Idempotency-Key")
	if len(idemKey) > 256 {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Idempotency-Key too long (max 256)")
		return
	}
	// check idempotency if provided
	if idemKey != "" {
		var existingEventID string
		err := h.Pool.QueryRow(r.Context(),
			`SELECT event_id FROM idempotency_keys WHERE project_id=$1 AND key=$2 AND expires_at > NOW() LIMIT 1`,
			projectID, idemKey).Scan(&existingEventID)
		if err == nil {
			// return original event
			var evtID, evtType string
			var payload json.RawMessage
			var createdAt time.Time
			err = h.Pool.QueryRow(r.Context(), `SELECT id, type, payload, created_at FROM events WHERE id=$1 AND project_id=$2`, existingEventID, projectID).Scan(&evtID, &evtType, &payload, &createdAt)
			if err == nil {
				writeJSON(w, r, http.StatusOK, map[string]interface{}{
					"id": evtID, "type": evtType, "status": "queued", "created_at": createdAt,
				})
				return
			}
		} else if err != pgx.ErrNoRows {
			// log error but continue
		}
	}

	// read body with limit
	r.Body = http.MaxBytesReader(w, r.Body, h.Config.MaxEventPayloadSize)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		if err.Error() == "http: request body too large" {
			writeError(w, r, http.StatusRequestEntityTooLarge, "INVALID_REQUEST", "payload too large")
			return
		}
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}
	if len(bodyBytes) == 0 {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "empty body")
		return
	}
	var req createEventReq
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON")
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	if req.Type == "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "type is required")
		return
	}
	if len(req.Type) > 128 {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "type max 128 chars")
		return
	}
	if !eventTypeRegex.MatchString(req.Type) {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "invalid event type format")
		return
	}
	if strings.Contains(req.Type, "*") {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "wildcard not supported")
		return
	}
	if len(req.Data) == 0 {
		req.Data = json.RawMessage(`{}`)
	}
	// generate event id
	evtID := webhookULID.Generate("evt_")
	// create event + deliveries in tx
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to begin tx")
		return
	}
	defer tx.Rollback(ctx)
	// insert event
	_, err = tx.Exec(ctx, `INSERT INTO events (id, project_id, type, payload) VALUES ($1,$2,$3,$4)`, evtID, projectID, req.Type, req.Data)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create event")
		return
	}
	// idempotency record if key provided
	if idemKey != "" {
		idemID := webhookULID.Generate("req_")
		expiresAt := time.Now().Add(24 * time.Hour)
		_, err = tx.Exec(ctx, `INSERT INTO idempotency_keys (id, project_id, key, event_id, expires_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (project_id, key) DO NOTHING`, idemID, projectID, idemKey, evtID, expiresAt)
		if err != nil {
			// non-fatal? but we should roll back?
			// log and continue
		}
	}
	// find matching endpoints
	rows, err := tx.Query(ctx, `SELECT e.id FROM endpoints e JOIN subscriptions s ON s.endpoint_id=e.id WHERE e.project_id=$1 AND s.event_type=$2 AND e.status='active'`, projectID, req.Type)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to find subscriptions")
		return
	}
	var endpointIDs []string
	for rows.Next() {
		var eid string
		_ = rows.Scan(&eid)
		endpointIDs = append(endpointIDs, eid)
	}
	rows.Close()
	// create deliveries pending
	var deliveryIDs []string
	for _, eid := range endpointIDs {
		delID := webhookULID.Generate("del_")
		_, err = tx.Exec(ctx, `INSERT INTO deliveries (id, event_id, endpoint_id, status) VALUES ($1,$2,$3,'pending')`, delID, evtID, eid)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create delivery")
			return
		}
		deliveryIDs = append(deliveryIDs, delID)
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit")
		return
	}
	// publish to redis stream (after commit, best effort)
	if h.Redis != nil {
		for _, delID := range deliveryIDs {
			_ = queue.PublishDelivery(context.Background(), h.Redis, delID)
		}
	}
	// return 202 Accepted — ingestion is async, delivery happens via queue
	writeJSON(w, r, http.StatusAccepted, map[string]interface{}{
		"id": evtID, "type": req.Type, "status": "queued",
	})
}

func (h *EventHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	id := chi.URLParam(r, "id")
	var evtID, evtType string
	var payload json.RawMessage
	var createdAt time.Time
	err := h.Pool.QueryRow(r.Context(), `SELECT id, type, payload, created_at FROM events WHERE id=$1 AND project_id=$2`, id, projectID).Scan(&evtID, &evtType, &payload, &createdAt)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "event not found")
		return
	}
	var data json.RawMessage = payload
	// try to parse payload as json
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"id": evtID, "type": evtType, "data": json.RawMessage(data), "created_at": createdAt,
	})
}

func (h *EventHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID := middleware.GetProjectID(r.Context())
	limit := queryInt(r, "limit", 50, 100)
	after := r.URL.Query().Get("after")
	eventType := r.URL.Query().Get("type")
	// also allow event_type query
	if eventType == "" {
		eventType = r.URL.Query().Get("event_type")
	}
	query := `SELECT id, type, payload, created_at FROM events WHERE project_id=$1`
	args := []interface{}{projectID}
	argIdx := 2
	if eventType != "" {
		query += ` AND type=$` + strconv.Itoa(argIdx)
		args = append(args, eventType)
		argIdx++
	}
	if after != "" {
		query += ` AND id > $` + strconv.Itoa(argIdx)
		args = append(args, after)
		argIdx++
	}
	query += ` ORDER BY id ASC LIMIT $` + strconv.Itoa(argIdx)
	args = append(args, limit)

	rows, err := h.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list events")
		return
	}
	defer rows.Close()
	type evt struct {
		ID        string          `json:"id"`
		Type      string          `json:"type"`
		Data      json.RawMessage `json:"data"`
		CreatedAt time.Time       `json:"created_at"`
	}
	var list []evt
	for rows.Next() {
		var e evt
		var payload json.RawMessage
		var created time.Time
		var id, typ string
		if err := rows.Scan(&id, &typ, &payload, &created); err != nil {
			continue
		}
		e.ID = id
		e.Type = typ
		e.Data = payload
		e.CreatedAt = created
		list = append(list, e)
	}
	if list == nil {
		list = []evt{}
	}
	hasMore := len(list) == limit
	var nextCursor string
	if hasMore {
		nextCursor = list[len(list)-1].ID
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"data":       list,
		"pagination": map[string]interface{}{"has_more": hasMore, "next_cursor": nextCursor},
	})
}
