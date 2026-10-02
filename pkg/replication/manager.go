package replication

import (
	"context"
	"fmt"
	"sync"
	"time"

	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

// Config configures the availability group replication manager.
type Config struct {
	NodeID      string
	Role        Role
	PrimaryURL  string
	HistoryCap  int
	Engine      storage.Engine
	InitialLSN  int64
}

// Manager coordinates HA availability group replication state, roles, and streaming.
type Manager struct {
	mu          sync.RWMutex
	nodeID      string
	role        Role
	primaryURL  string
	eng         storage.Engine
	broadcaster *WALBroadcaster
	client      *ReplicationClient
	cancelFn    context.CancelFunc
	closed      bool
}

// NewManager initializes a replication manager for the given configuration.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.NodeID == "" {
		cfg.NodeID = fmt.Sprintf("node-%d", time.Now().UnixNano()%10000)
	}
	if cfg.Role == "" {
		cfg.Role = RolePrimary
	}
	if cfg.HistoryCap <= 0 {
		cfg.HistoryCap = 10000
	}

	m := &Manager{
		nodeID:      cfg.NodeID,
		role:        cfg.Role,
		primaryURL:  cfg.PrimaryURL,
		eng:         cfg.Engine,
		broadcaster: NewWALBroadcaster(cfg.HistoryCap),
	}

	// Wire engine WAL hook if supported by engine
	type walHookable interface {
		SetWALHook(fn func(rec memengine.WALRecord))
	}
	if wh, ok := m.eng.(walHookable); ok {
		wh.SetWALHook(func(rec memengine.WALRecord) {
			m.mu.RLock()
			isPrimary := m.role == RolePrimary
			m.mu.RUnlock()
			if isPrimary {
				m.broadcaster.Broadcast(rec)
			}
		})
	}

	return m, nil
}

// Start begins replication tasks based on current role.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	m.cancelFn = cancel

	if m.role == RoleReplica {
		if m.primaryURL == "" {
			return fmt.Errorf("primary_url is required for replica role")
		}
		var initialLSN int64
		if m.eng != nil {
			initialLSN = m.eng.CurrentLSN()
		}
		m.client = NewReplicationClient(m.primaryURL, m.nodeID, m.eng, initialLSN)
		m.client.Start(ctx)
	}

	return nil
}

// Role returns the current replication role.
func (m *Manager) Role() Role {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.role
}

// IsReadOnly returns true if the node is currently a read-only secondary replica.
func (m *Manager) IsReadOnly() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.role == RoleReplica
}

// Broadcaster returns the underlying WALBroadcaster for primary streaming.
func (m *Manager) Broadcaster() *WALBroadcaster {
	return m.broadcaster
}

// Promote promotes a secondary replica to primary, enabling write operations.
func (m *Manager) Promote() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.role == RolePrimary {
		return ErrAlreadyPrimary
	}

	// 1. Stop replication client from old primary
	if m.client != nil {
		m.client.Stop()
		m.client = nil
	}

	// 2. Switch role to Primary
	m.role = RolePrimary
	m.primaryURL = ""

	return nil
}

// Demote demotes a primary node to a secondary replica pointing to newPrimaryURL.
func (m *Manager) Demote(newPrimaryURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.role == RoleReplica {
		return ErrNotReplica
	}

	if newPrimaryURL == "" {
		return fmt.Errorf("newPrimaryURL cannot be empty")
	}

	m.role = RoleReplica
	m.primaryURL = newPrimaryURL

	// Start client to stream from new primary
	var currentLSN int64
	if m.eng != nil {
		currentLSN = m.eng.CurrentLSN()
	}
	ctx := context.Background()
	m.client = NewReplicationClient(m.primaryURL, m.nodeID, m.eng, currentLSN)
	m.client.Start(ctx)

	return nil
}

// Status returns the comprehensive replication status of this node.
func (m *Manager) Status() ReplicationStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var currentLSN int64
	if m.eng != nil {
		currentLSN = m.eng.CurrentLSN()
	} else if m.client != nil {
		currentLSN = m.client.LastLSN()
	}

	status := ReplicationStatus{
		Role:       m.role,
		NodeID:     m.nodeID,
		CurrentLSN: currentLSN,
		PrimaryURL: m.primaryURL,
		ReadOnly:   m.role == RoleReplica,
	}

	if m.role == RolePrimary && m.broadcaster != nil {
		status.Replicas = m.broadcaster.GetReplicas(currentLSN)
	}

	return status
}

// Close gracefully stops the replication manager.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true

	if m.cancelFn != nil {
		m.cancelFn()
	}

	if m.client != nil {
		m.client.Stop()
	}

	if m.broadcaster != nil {
		m.broadcaster.Close()
	}

	return nil
}
