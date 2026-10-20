package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/backup"
	"lionsql/pkg/engine"
	"lionsql/pkg/execution"
)

type execSQLReq struct {
	Query     string `json:"query"`
	SQL       string `json:"sql"`
	SessionID string `json:"session_id"`
	TimeoutMS int64  `json:"timeout_ms"`
	Format    string `json:"format"` // "envelope" (default), "array", "raw", "records"
}

func (s *Server) handleExecuteSQL(w http.ResponseWriter, r *http.Request) {
	var req execSQLReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	sqlStr := req.Query
	if strings.TrimSpace(sqlStr) == "" {
		sqlStr = req.SQL
	}

	sessID := req.SessionID
	if sessID == "" {
		sessID = "default"
	}

	reqCtx := r.Context()
	timeoutDur := time.Duration(0)
	if req.TimeoutMS > 0 {
		timeoutDur = time.Duration(req.TimeoutMS) * time.Millisecond
	} else if h := r.Header.Get("X-Query-Timeout"); h != "" {
		if d, err := parseTimeoutDuration(h); err == nil && d > 0 {
			timeoutDur = d
		}
	} else if q := r.URL.Query().Get("timeout_ms"); q != "" {
		if ms, err := strconv.ParseInt(q, 10, 64); err == nil && ms > 0 {
			timeoutDur = time.Duration(ms) * time.Millisecond
		}
	}

	if timeoutDur > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(reqCtx, timeoutDur)
		defer cancel()
	}

	result, err := s.ExecuteSessionSQLContext(reqCtx, sessID, sqlStr)
	if err != nil {
		if result != nil {
			writeJSON(w, http.StatusOK, result)
			return
		}
		writeJSON(w, http.StatusOK, &QueryResult{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Format resolution: query parameter takes precedence over request body
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = strings.ToLower(strings.TrimSpace(req.Format))
	}

	// If client requested pure array format, return only the rows array: [ { ... }, { ... } ]
	if format == "array" || format == "raw" || format == "records" {
		rows := result.Rows
		if rows == nil {
			rows = []map[string]any{}
		}
		writeJSON(w, http.StatusOK, rows)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleQueryCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		SPID      int    `json:"spid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	canceled := false
	if req.SessionID != "" {
		canceled = s.CancelSessionQuery(req.SessionID)
	} else if req.SPID > 0 {
		canceled = s.CancelQueryBySPID(req.SPID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"canceled": canceled,
	})
}

func (s *Server) handleListActiveQueries(w http.ResponseWriter, r *http.Request) {
	active := s.GetActiveQueries()
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"queries": active,
		"total":   len(active),
	})
}

func (s *Server) buildPrometheusServerState() engine.PrometheusServerState {
	st := engine.PrometheusServerState{StartTime: s.startTime}
	st.ActiveConnections = s.coordinator.SessionCount()
	if s.engine != nil {
		engType := strings.ToLower(s.engine.Type())
		if strings.Contains(engType, "memory") {
			st.StorageEngine = "memengine"
		} else if strings.Contains(engType, "wiredtiger") {
			st.StorageEngine = "wiredtiger"
		} else {
			st.StorageEngine = engType
		}
		if tbls, err := s.engine.ListTables(); err == nil {
			st.TableCount = len(tbls)
		}
		st.ReplicationCurrentLSN = s.engine.CurrentLSN()
	}
	if s.planCache != nil {
		st.PlanCacheHits, st.PlanCacheMisses = s.planCache.Stats()
	}

	if memLimit := execution.GetGlobalMemoryLimit(); memLimit > 0 && memLimit < math.MaxInt64 {
		st.MemoryLimitBytes = memLimit
	}

	if s.replication != nil {
		repStatus := s.replication.Status()
		st.ReplicationRole = string(repStatus.Role)
		if repStatus.CurrentLSN > 0 {
			st.ReplicationCurrentLSN = repStatus.CurrentLSN
		}
		var maxLag int64
		for _, rep := range repStatus.Replicas {
			if rep.ReplicationLag > maxLag {
				maxLag = rep.ReplicationLag
			}
		}
		st.ReplicationLagBytes = maxLag
	} else {
		st.ReplicationRole = "standalone"
	}

	return st
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.Metrics() == nil {
		http.Error(w, "metrics collector not initialized", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	st := s.buildPrometheusServerState()
	_, _ = w.Write([]byte(s.Metrics().FormatPrometheus(st)))
}

func (s *Server) handleOpenTelemetryMetrics(w http.ResponseWriter, r *http.Request) {
	if s.Metrics() == nil {
		http.Error(w, "metrics collector not initialized", http.StatusInternalServerError)
		return
	}
	st := s.buildPrometheusServerState()
	data, err := s.Metrics().FormatOpenTelemetry(st)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to format opentelemetry metrics: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleGetSlowQueries(w http.ResponseWriter, r *http.Request) {
	if s.SlowQueries() == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"threshold_ms": 0.0,
			"count":        0,
			"entries":      []SlowQueryEntry{},
		})
		return
	}
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	entries := s.SlowQueries().GetEntries(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"threshold_ms": float64(s.SlowQueries().Threshold().Microseconds()) / 1000.0,
		"count":        len(entries),
		"log_file":     s.SlowQueries().LogFilePath(),
		"entries":      entries,
	})
}

func (s *Server) handleSetSlowQueryThreshold(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ThresholdMS float64 `json:"threshold_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.ThresholdMS <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("threshold_ms must be greater than 0"))
		return
	}
	d := time.Duration(req.ThresholdMS * float64(time.Millisecond))
	s.SetSlowQueryThreshold(d)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"threshold_ms": req.ThresholdMS,
	})
}

func (s *Server) handleSetSlowQueryLogFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FilePath string `json:"file_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.SetSlowQueryLogFile(req.FilePath); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"file_path": s.SlowQueries().LogFilePath(),
	})
}

func (s *Server) handleClearSlowQueries(w http.ResponseWriter, r *http.Request) {
	if s.SlowQueries() != nil {
		s.SlowQueries().Clear()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "slow queries cleared",
	})
}

func (s *Server) handleGetGeneralQueries(w http.ResponseWriter, r *http.Request) {
	if s.GeneralLog() == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":  false,
			"count":    0,
			"log_file": "",
			"entries":  []GeneralLogEntry{},
		})
		return
	}
	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	entries := s.GeneralLog().GetEntries(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":  s.GeneralLog().IsEnabled(),
		"count":    len(entries),
		"log_file": s.GeneralLog().LogFilePath(),
		"entries":  entries,
	})
}

func (s *Server) handleToggleGeneralLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.SetGeneralLogEnabled(req.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"enabled": req.Enabled,
	})
}

func (s *Server) handleSetGeneralLogFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FilePath string `json:"file_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.SetGeneralLogFile(req.FilePath); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"file_path": s.GeneralLog().LogFilePath(),
	})
}

func (s *Server) handleClearGeneralQueries(w http.ResponseWriter, r *http.Request) {
	if s.GeneralLog() != nil {
		s.GeneralLog().Clear()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "general query log cleared",
	})
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	// If GET, or download parameter requested, stream backup directly
	if r.Method == http.MethodGet || r.URL.Query().Get("download") == "true" || r.URL.Query().Get("download") == "1" {
		filename := fmt.Sprintf("sqlion-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		_, _ = backup.CreateBackup(w, s.engine, s.catalog)
		return
	}

	// For POST, parse JSON body to see if a specific server target path is provided
	var req struct {
		Target string `json:"target"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if strings.TrimSpace(req.Target) == "" {
		filename := fmt.Sprintf("sqlion-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		_, _ = backup.CreateBackup(w, s.engine, s.catalog)
		return
	}

	manifest, err := backup.CreateBackupFile(req.Target, s.engine, s.catalog)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("backup failed: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "success",
		"target":   req.Target,
		"manifest": manifest,
	})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	opts := backup.DefaultRestoreOptions()

	// 1. Multipart form file upload
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(256 * 1024 * 1024); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("failed to parse multipart form: %w", err))
			return
		}
		file, _, err := r.FormFile("backup")
		if err != nil {
			file, _, err = r.FormFile("file")
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("missing backup archive file (field 'backup' or 'file')"))
			return
		}
		defer file.Close()

		res, err := backup.RestoreBackup(file, s.engine, s.catalog, opts)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("restore failed: %w", err))
			return
		}

		if s.broker != nil {
			s.broker.Broadcast(RealtimeEvent{
				Action: ActionCreate,
				Table:  "*",
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"result": res,
		})
		return
	}

	// 2. JSON request referencing server file path
	var req struct {
		Source       string `json:"source"`
		DropExisting *bool  `json:"drop_existing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json request: %w", err))
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("source file path is required"))
		return
	}
	if req.DropExisting != nil {
		opts.DropExisting = *req.DropExisting
	}

	res, err := backup.RestoreBackupFile(req.Source, s.engine, s.catalog, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("restore failed: %w", err))
		return
	}

	if s.broker != nil {
		s.broker.Broadcast(RealtimeEvent{
			Action: ActionCreate,
			Table:  "*",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"result": res,
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
