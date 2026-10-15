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
	mux.HandleFunc("POST /api/replication/vote", m.handleVote)
	mux.HandleFunc("POST /api/replication/leader", m.handleLeader)
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
	w.Header().Set("X-Replication-Term", strconv.FormatInt(m.Term(), 10))
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeatTicker := time.NewTicker(5 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeatTicker.C:
			if _, err := fmt.Fprintf(w, ":heartbeat term=%d\n", m.Term()); err != nil {
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

type promoteRequest struct {
	Term int64 `json:"term,omitempty"`
}

func (m *Manager) handlePromote(w http.ResponseWriter, r *http.Request) {
	var req promoteRequest
	if r.Body != nil && r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	var err error
	if req.Term > 0 {
		err = m.PromoteWithTerm(req.Term)
	} else {
		err = m.Promote()
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "promoted",
		"role":   string(RolePrimary),
		"term":   m.Term(),
	})
}

type demoteRequest struct {
	PrimaryURL string `json:"primary_url"`
	Term       int64  `json:"term,omitempty"`
}

func (m *Manager) handleDemote(w http.ResponseWriter, r *http.Request) {
	var req demoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var err error
	if req.Term > 0 {
		err = m.DemoteWithTerm(req.PrimaryURL, req.Term)
	} else {
		err = m.Demote(req.PrimaryURL)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "demoted",
		"role":   string(RoleReplica),
		"term":   m.Term(),
	})
}

func (m *Manager) handleVote(w http.ResponseWriter, r *http.Request) {
	var req VoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := m.HandleVote(req)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (m *Manager) handleLeader(w http.ResponseWriter, r *http.Request) {
	var ann LeaderAnnouncement
	if err := json.NewDecoder(r.Body).Decode(&ann); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := m.HandleLeaderAnnouncement(ann); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

