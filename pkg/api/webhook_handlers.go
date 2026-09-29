package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	items := s.webhooks.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("webhook dispatcher not initialized"))
		return
	}

	var wh Webhook
	if err := json.NewDecoder(r.Body).Decode(&wh); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json payload: %w", err))
		return
	}

	if err := s.webhooks.Register(&wh); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusCreated, wh)
}

func (s *Server) handleGetWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("webhook not found"))
		return
	}

	id := r.PathValue("id")
	wh, ok := s.webhooks.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("webhook with id %q not found", id))
		return
	}

	writeJSON(w, http.StatusOK, wh)
}

func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("webhook dispatcher not initialized"))
		return
	}

	id := r.PathValue("id")
	var wh Webhook
	if err := json.NewDecoder(r.Body).Decode(&wh); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json payload: %w", err))
		return
	}

	if err := s.webhooks.Update(id, &wh); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	updated, _ := s.webhooks.Get(id)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("webhook not found"))
		return
	}

	id := r.PathValue("id")
	if !s.webhooks.Delete(id) {
		writeError(w, http.StatusNotFound, fmt.Errorf("webhook with id %q not found", id))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"deleted": true,
		"id":      id,
	})
}

func (s *Server) handleTestWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("webhook dispatcher not initialized"))
		return
	}

	id := r.PathValue("id")
	del, err := s.webhooks.TestPing(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, del)
}

func (s *Server) handleListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
			limit = val
		}
	}

	dels := s.webhooks.Deliveries(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"items": dels,
		"total": len(dels),
	})
}

func (s *Server) handleClearWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	if s.webhooks != nil {
		s.webhooks.ClearDeliveries()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": "webhook delivery history cleared",
	})
}
