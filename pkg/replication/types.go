package replication

import (
	"errors"
	"time"
)

// Role defines the HA replication role of a node.
type Role string

const (
	RolePrimary Role = "PRIMARY"
	RoleReplica Role = "REPLICA"
)

// Standard replication errors.
var (
	ErrReadOnlyReplica = errors.New("cannot execute write operations on read-only replica")
	ErrAlreadyPrimary  = errors.New("node is already primary")
	ErrNotReplica      = errors.New("node is not a replica")
	ErrStreamClosed    = errors.New("replication stream closed")
	ErrStaleTerm       = errors.New("replication term is stale")
	ErrSplitBrain      = errors.New("split-brain detected: higher term exists")
	ErrElectionFailed  = errors.New("election failed: insufficient quorum votes")
)

// ReplicaInfo holds the runtime state of a connected secondary replica.
type ReplicaInfo struct {
	NodeID         string    `json:"node_id"`
	RemoteAddr     string    `json:"remote_addr"`
	ConnectedAt    time.Time `json:"connected_at"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
	AckedLSN       int64     `json:"acked_lsn"`
	ReplicationLag int64     `json:"replication_lag"` // PrimaryLSN - AckedLSN
	State          string    `json:"state"`           // "STREAMING", "CATCHING_UP", "DISCONNECTED"
}

// ReplicationStatus represents the comprehensive availability group status of this node.
type ReplicationStatus struct {
	Role         Role          `json:"role"`
	NodeID       string        `json:"node_id"`
	CurrentLSN   int64         `json:"current_lsn"`
	PrimaryURL   string        `json:"primary_url,omitempty"`
	ReadOnly     bool          `json:"read_only"`
	Term         int64         `json:"term"`
	LeaderID     string        `json:"leader_id,omitempty"`
	AutoFailover bool          `json:"auto_failover"`
	Peers        []string      `json:"peers,omitempty"`
	Replicas     []ReplicaInfo `json:"replicas,omitempty"`
}

// VoteRequest carries an election candidate's vote request to availability group peers.
type VoteRequest struct {
	Term         int64  `json:"term"`
	CandidateID  string `json:"candidate_id"`
	CandidateURL string `json:"candidate_url"`
	LastLSN      int64  `json:"last_lsn"`
}

// VoteResponse carries the peer's vote decision and current term.
type VoteResponse struct {
	Term        int64  `json:"term"`
	VoteGranted bool   `json:"vote_granted"`
	Reason      string `json:"reason,omitempty"`
}

// LeaderAnnouncement notifies peer nodes of a newly elected primary leader.
type LeaderAnnouncement struct {
	Term      int64  `json:"term"`
	LeaderID  string `json:"leader_id"`
	LeaderURL string `json:"leader_url"`
}

