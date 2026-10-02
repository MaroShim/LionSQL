package replication

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// RegisterRoutes mounts all replication HTTP endpoints on the given ServeMux.
func (m *Manager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/replication/stream", m.handleStream)
	mux.HandleFunc("GET /api/replication/status", m.handleStatus)
	mux.HandleFunc("POST /api/replication/ack", m.handleAck)
	mux.HandleFunc("POST /api/replication/promote", m.handlePromote)
	mux.HandleFunc("POST /api/replication/demote", m.handleDemote)
}

func (m *Manager) handleStream(w http.ResponseWriter, r *http.Request) {
	if m.Role() != RolePrimary {
		http.Error(w, "node is not a primary", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	q := r.URL.Query()
	replicaID := q.Get("replica_id")
	if replicaID == "" {
		replicaID = r.RemoteAddr
	}

	fromLSNStr := q.Get("from_lsn")
	var fromLSN int64
	if fromLSNStr != "" {
		fromLSN, _ = strconv.ParseInt(fromLSNStr, 10, 64)
	}

	ch, cancel, err := m.broadcaster.Subscribe(replicaID, r.RemoteAddr, fromLSN, 1000)
	if err != nil {
		http.Error(w, fmt.Sprintf("subscribe failed: %v", err), http.StatusBadRequest)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeatTicker := time.NewTicker(5 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeatTicker.C:
			if _, err := fmt.Fprint(w, ":heartbeat\n"); err != nil {
				return
			}
			flusher.Flush()
		case rec, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(rec)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "%s\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (m *Manager) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := m.Status()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

type ackRequest struct {
	NodeID   string `json:"node_id"`
	AckedLSN int64  `json:"acked_lsn"`
}

func (m *Manager) handleAck(w http.ResponseWriter, r *http.Request) {
	if m.Role() != RolePrimary {
		http.Error(w, "node is not a primary", http.StatusBadRequest)
		return
	}

	var req ackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if m.broadcaster != nil {
		m.broadcaster.UpdateAck(req.NodeID, req.AckedLSN)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (m *Manager) handlePromote(w http.ResponseWriter, r *http.Request) {
	if err := m.Promote(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "promoted", "role": string(RolePrimary)})
}

type demoteRequest struct {
	PrimaryURL string `json:"primary_url"`
}

func (m *Manager) handleDemote(w http.ResponseWriter, r *http.Request) {
	var req demoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := m.Demote(req.PrimaryURL); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "demoted", "role": string(RoleReplica)})
}
