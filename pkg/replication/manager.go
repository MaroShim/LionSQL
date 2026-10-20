package replication

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// Config configures the availability group replication manager.
type Config struct {
	NodeID           string
	Role             Role
	PrimaryURL       string
	SelfURL          string
	Peers            []string
	AutoFailover     bool
	HeartbeatTimeout time.Duration
	ElectionTimeout  time.Duration
	Quorum           int
	HistoryCap       int
	Engine           storage.Engine
	InitialLSN       int64
	InitialTerm      int64
}

// Manager coordinates HA availability group replication state, roles, streaming, and auto-failover.
type Manager struct {
	mu               sync.RWMutex
	nodeID           string
	role             Role
	primaryURL       string
	selfURL          string
	peers            []string
	autoFailover     bool
	heartbeatTimeout time.Duration
	electionTimeout  time.Duration
	quorum           int
	term             int64
	initialLSN       int64
	votedFor         string
	leaderID         string
	lastHeartbeat    atomic.Int64
	eng              storage.Engine
	broadcaster      *WALBroadcaster
	client           *ReplicationClient
	ctx              context.Context
	cancelFn         context.CancelFunc
	failoverCancel   context.CancelFunc
	failoverWg       sync.WaitGroup
	httpClient       *http.Client
	closed           bool
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
	if cfg.HeartbeatTimeout <= 0 {
		cfg.HeartbeatTimeout = 3 * time.Second
	}
	if cfg.ElectionTimeout <= 0 {
		cfg.ElectionTimeout = 1500 * time.Millisecond
	}
	if cfg.InitialTerm <= 0 {
		cfg.InitialTerm = 1
	}

	m := &Manager{
		nodeID:           cfg.NodeID,
		role:             cfg.Role,
		primaryURL:       cfg.PrimaryURL,
		selfURL:          cfg.SelfURL,
		peers:            cfg.Peers,
		autoFailover:     cfg.AutoFailover,
		heartbeatTimeout: cfg.HeartbeatTimeout,
		electionTimeout:  cfg.ElectionTimeout,
		quorum:           cfg.Quorum,
		term:             cfg.InitialTerm,
		initialLSN:       cfg.InitialLSN,
		eng:              cfg.Engine,
		broadcaster:      NewWALBroadcaster(cfg.HistoryCap),
		httpClient: &http.Client{
			Timeout: cfg.ElectionTimeout,
		},
	}
	m.lastHeartbeat.Store(time.Now().UnixNano())
	if cfg.Role == RolePrimary {
		m.leaderID = cfg.NodeID
		m.votedFor = cfg.NodeID
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
	m.ctx = ctx
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
		m.client.SetOnTermReceived(func(t int64) {
			m.HandlePrimaryTerm(t)
		})
		m.client.Start(ctx)
	}

	if m.autoFailover {
		m.startFailoverMonitor(ctx)
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

// Term returns the current election term.
func (m *Manager) Term() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.term
}

// LeaderID returns the current recognized availability group leader ID.
func (m *Manager) LeaderID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.leaderID
}

// AutoFailoverEnabled returns true if automatic failover is enabled on this node.
func (m *Manager) AutoFailoverEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.autoFailover
}

// Peers returns the configured availability group peer URLs.
func (m *Manager) Peers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.peers...)
}

// SelfURL returns the HTTP URL of this node used for peer routing.
func (m *Manager) SelfURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.selfURL
}

// SetSelfURL configures this node's HTTP URL.
func (m *Manager) SetSelfURL(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.selfURL = url
}

// SetPeers updates the peer list for this node.
func (m *Manager) SetPeers(peers []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers = peers
}

// SetQuorum configures custom quorum size.
func (m *Manager) SetQuorum(q int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.quorum = q
}

// Broadcaster returns the underlying WALBroadcaster for primary streaming.
func (m *Manager) Broadcaster() *WALBroadcaster {
	return m.broadcaster
}

// Promote promotes a secondary replica to primary with a bumped term, enabling write operations.
func (m *Manager) Promote() error {
	return m.PromoteWithTerm(m.Term() + 1)
}

// PromoteWithTerm promotes this node to primary under an explicit term.
func (m *Manager) PromoteWithTerm(newTerm int64) error {
	m.mu.Lock()
	if m.role == RolePrimary {
		m.mu.Unlock()
		return ErrAlreadyPrimary
	}

	// 1. Detach replication client from old primary
	client := m.client
	m.client = nil

	// 2. Switch role to Primary
	m.role = RolePrimary
	m.primaryURL = ""
	if newTerm > m.term {
		m.term = newTerm
	}
	m.leaderID = m.nodeID
	m.votedFor = m.nodeID

	term := m.term
	leaderID := m.nodeID
	leaderURL := m.selfURL
	peers := make([]string, len(m.peers))
	copy(peers, m.peers)
	m.mu.Unlock()

	// 3. Stop client outside lock
	if client != nil {
		client.Stop()
	}

	// 3. Notify peers asynchronously
	if len(peers) > 0 {
		go m.broadcastLeaderAnnouncement(term, leaderID, leaderURL, peers)
	}

	return nil
}

// Demote demotes a primary node to a secondary replica pointing to newPrimaryURL.
func (m *Manager) Demote(newPrimaryURL string) error {
	return m.DemoteWithTerm(newPrimaryURL, m.Term())
}

// DemoteWithTerm demotes a primary node under an explicit term.
func (m *Manager) DemoteWithTerm(newPrimaryURL string, newTerm int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.role == RoleReplica && m.primaryURL == newPrimaryURL {
		if newTerm > m.term {
			m.term = newTerm
		}
		return nil
	}

	if newPrimaryURL == "" {
		return fmt.Errorf("newPrimaryURL cannot be empty")
	}

	m.role = RoleReplica
	m.primaryURL = newPrimaryURL
	if newTerm > m.term {
		m.term = newTerm
	}
	m.leaderID = ""
	m.votedFor = ""
	m.lastHeartbeat.Store(time.Now().UnixNano())

	m.restartClientLocked()

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
		Role:         m.role,
		NodeID:       m.nodeID,
		CurrentLSN:   currentLSN,
		PrimaryURL:   m.primaryURL,
		ReadOnly:     m.role == RoleReplica,
		Term:         m.term,
		LeaderID:     m.leaderID,
		AutoFailover: m.autoFailover,
		Peers:        append([]string(nil), m.peers...),
	}

	if m.role == RolePrimary && m.broadcaster != nil {
		status.Replicas = m.broadcaster.GetReplicas(currentLSN)
	}

	return status
}

// LastHeartbeatTime returns the timestamp of the last observed heartbeat or stream activity.
func (m *Manager) LastHeartbeatTime() time.Time {
	m.mu.RLock()
	c := m.client
	m.mu.RUnlock()

	if c != nil {
		return c.LastHeartbeatTime()
	}
	nanos := m.lastHeartbeat.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

func (m *Manager) restartClientLocked() {
	oldClient := m.client
	m.client = nil

	if m.primaryURL != "" && m.role == RoleReplica && !m.closed {
		var initialLSN int64
		if m.eng != nil {
			initialLSN = m.eng.CurrentLSN()
		}
		if initialLSN < m.initialLSN {
			initialLSN = m.initialLSN
		}
		m.client = NewReplicationClient(m.primaryURL, m.nodeID, m.eng, initialLSN)
		m.client.SetOnTermReceived(func(t int64) {
			m.HandlePrimaryTerm(t)
		})
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		m.client.Start(ctx)
	}

	if oldClient != nil {
		go oldClient.Stop()
	}
}

func (m *Manager) resetHeartbeatLocked() {
	m.lastHeartbeat.Store(time.Now().UnixNano())
	if m.client != nil {
		m.client.ResetHeartbeat()
	}
}

// Close gracefully stops the replication manager.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true

	if m.failoverCancel != nil {
		m.failoverCancel()
	}
	if m.cancelFn != nil {
		m.cancelFn()
	}

	client := m.client
	m.client = nil
	broadcaster := m.broadcaster
	m.mu.Unlock()

	m.failoverWg.Wait()

	if client != nil {
		client.Stop()
	}

	if broadcaster != nil {
		broadcaster.Close()
	}

	return nil
}

