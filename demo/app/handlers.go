package main

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const (
	listOrdersDefaultLimit = 50
	listOrdersMaxLimit     = 200
)

// App holds shared dependencies for all HTTP handlers.
type App struct {
	db       *DB
	version  string
	hostname string
	injector *errorInjector
}

func (a *App) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	// Chaos injection for v2: simulate errors/latency at the configured rate.
	if rate := a.injector.currentRate(time.Now()); rate > 0 && rand.Float64() < rate {
		time.Sleep(time.Duration(200+rand.Intn(800)) * time.Millisecond)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "simulated failure"})
		return
	}

	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title is required"})
		return
	}

	if a.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database not configured"})
		return
	}

	order, err := a.db.CreateOrder(body.Title)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, order)
}

func (a *App) handleListOrders(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseListPagination(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if a.db == nil {
		writeJSON(w, http.StatusOK, []Order{})
		return
	}

	orders, err := a.db.ListOrders(limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

// parseListPagination extracts limit/offset from query params, applying
// defaults and clamping limit to listOrdersMaxLimit. Returns a 400-friendly
// error message for non-integer or negative values.
func parseListPagination(r *http.Request) (limit, offset int, err error) {
	limit = listOrdersDefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n <= 0 {
			return 0, 0, errInvalidParam("limit must be a positive integer")
		}
		if n > listOrdersMaxLimit {
			n = listOrdersMaxLimit
		}
		limit = n
	}

	if v := r.URL.Query().Get("offset"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 0 {
			return 0, 0, errInvalidParam("offset must be a non-negative integer")
		}
		offset = n
	}
	return limit, offset, nil
}

type errInvalidParam string

func (e errInvalidParam) Error() string { return string(e) }

func (a *App) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if a.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database not configured"})
		return
	}

	order, err := a.db.GetOrder(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if order == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (a *App) handleDeleteOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if a.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database not configured"})
		return
	}

	found, err := a.db.DeleteOrder(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// writeJSON serialises v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
