package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"webhooker/internal/middleware"
)

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v interface{}) {
	middleware.WriteJSON(w, r, status, v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	middleware.WriteError(w, r, status, code, msg)
}

func parseBody(w http.ResponseWriter, r *http.Request, dst interface{}, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if err.Error() == "http: request body too large" {
			writeError(w, r, http.StatusRequestEntityTooLarge, "INVALID_REQUEST", "payload too large")
			return false
		}
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON")
		return false
	}
	return true
}

func queryInt(r *http.Request, key string, def, max int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}
