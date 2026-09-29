package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) handleListCrons(w http.ResponseWriter, r *http.Request) {
	if s.crons == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": []interface{}{}})
		return
	}
	jobs := s.crons.ListJobs()
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": jobs})
}

func (s *Server) handleCreateCron(w http.ResponseWriter, r *http.Request) {
	if s.crons == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("cron scheduler is not enabled"))
		return
	}

	var req CronJob
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if err := s.crons.AddJob(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusCreated, req)
}

func (s *Server) handleUpdateCron(w http.ResponseWriter, r *http.Request) {
	if s.crons == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("cron scheduler is not enabled"))
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/crons/")
	id = strings.TrimSuffix(id, "/run")

	var req CronJob
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	req.ID = id

	if err := s.crons.UpdateJob(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	updated, _ := s.crons.GetJob(id)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteCron(w http.ResponseWriter, r *http.Request) {
	if s.crons == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("cron scheduler is not enabled"))
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/crons/")
	if err := s.crons.DeleteJob(id); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (s *Server) handleRunCronNow(w http.ResponseWriter, r *http.Request) {
	if s.crons == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("cron scheduler is not enabled"))
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid cron path"))
		return
	}
	id := parts[2]

	if err := s.crons.RunJobNow(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	job, _ := s.crons.GetJob(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":     job.LastStatus == "success",
		"last_status": job.LastStatus,
		"last_error":  job.LastError,
		"duration_ms": job.LastDurationMs,
	})
}
