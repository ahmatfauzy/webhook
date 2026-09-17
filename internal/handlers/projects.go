package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	webhookULID "webhooker/internal/ulid"
)

type ProjectHandler struct {
	Pool *pgxpool.Pool
}

func (h *ProjectHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.Create)
	r.Get("/", h.List)
	r.Get("/{id}", h.Get)
	r.Patch("/{id}", h.Update)
	r.Delete("/{id}", h.Delete)
	return r
}

func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !parseBody(w, r, &req, 1<<20) {
		return
	}
	if req.Name == "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}
	id := webhookULID.Generate("proj_")
	var created struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO projects (id, name, description) VALUES ($1,$2,$3) RETURNING id, name, COALESCE(description,'')`,
		id, req.Name, req.Description).Scan(&created.ID, &created.Name, &created.Description)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create project")
		return
	}
	writeJSON(w, r, http.StatusCreated, created)
}

func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50, 100)
	offset := queryInt(r, "offset", 0, 1000000)
	rows, err := h.Pool.Query(r.Context(), `SELECT id, name, COALESCE(description,''), created_at, updated_at FROM projects ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list projects")
		return
	}
	defer rows.Close()
	type proj struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
	}
	var list []proj
	for rows.Next() {
		var p proj
		var createdAt, updatedAt interface{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &createdAt, &updatedAt); err != nil {
			continue
		}
		list = append(list, p)
	}
	if list == nil {
		list = []proj{}
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"data": list})
}

func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var p struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	err := h.Pool.QueryRow(r.Context(), `SELECT id, name, COALESCE(description,'') FROM projects WHERE id=$1`, id).Scan(&p.ID, &p.Name, &p.Description)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}
	writeJSON(w, r, http.StatusOK, p)
}

func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if !parseBody(w, r, &req, 1<<20) {
		return
	}
	// fetch first
	var name, desc string
	err := h.Pool.QueryRow(r.Context(), `SELECT name, COALESCE(description,'') FROM projects WHERE id=$1`, id).Scan(&name, &desc)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}
	if req.Name != nil {
		name = *req.Name
	}
	if req.Description != nil {
		desc = *req.Description
	}
	var out struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	err = h.Pool.QueryRow(r.Context(), `UPDATE projects SET name=$2, description=$3 WHERE id=$1 RETURNING id, name, COALESCE(description,'')`, id, name, desc).Scan(&out.ID, &out.Name, &out.Description)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update")
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}

func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, err := h.Pool.Exec(r.Context(), `DELETE FROM projects WHERE id=$1`, id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
