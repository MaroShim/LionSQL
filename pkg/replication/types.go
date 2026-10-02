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
	Role       Role          `json:"role"`
	NodeID     string        `json:"node_id"`
	CurrentLSN int64         `json:"current_lsn"`
	PrimaryURL string        `json:"primary_url,omitempty"`
	ReadOnly   bool          `json:"read_only"`
	Replicas   []ReplicaInfo `json:"replicas,omitempty"`
}
