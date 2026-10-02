package replication

import (
	"sync"
	"time"

	"sqlion/pkg/storage/memengine"
)

// WALBroadcaster manages real-time WAL stream fan-out to connected secondary replicas.
type WALBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[string]*subscriber
	history     []memengine.WALRecord
	historyCap  int
	historyHead int
	historyLen  int
	closed      bool
}

type subscriber struct {
	id         string
	remoteAddr string
	ch         chan memengine.WALRecord
	ackedLSN   int64
	lastSeen   time.Time
	connected  time.Time
}

// NewWALBroadcaster creates a broadcaster with a ring buffer for recent WAL records.
func NewWALBroadcaster(historyCap int) *WALBroadcaster {
	if historyCap <= 0 {
		historyCap = 10000
	}
	return &WALBroadcaster{
		subscribers: make(map[string]*subscriber),
		history:     make([]memengine.WALRecord, historyCap),
		historyCap:  historyCap,
	}
}

// Broadcast sends a committed WAL record to all active subscribers and caches it in the history buffer.
func (b *WALBroadcaster) Broadcast(rec memengine.WALRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	// 1. Append to ring buffer
	idx := (b.historyHead + b.historyLen) % b.historyCap
	if b.historyLen < b.historyCap {
		b.history[idx] = rec
		b.historyLen++
	} else {
		b.history[b.historyHead] = rec
		b.historyHead = (b.historyHead + 1) % b.historyCap
	}

	// 2. Non-blocking fan-out to all active replica subscribers (Golden Rule 36)
	for _, sub := range b.subscribers {
		select {
		case sub.ch <- rec:
		default:
			// If buffer is full, subscriber is lagging; in high load, channel buffer absorbs spikes
		}
	}
}

// Subscribe registers a replica subscriber starting from the requested fromLSN.
// It pre-populates the channel with buffered historical records if fromLSN is behind.
func (b *WALBroadcaster) Subscribe(replicaID, remoteAddr string, fromLSN int64, bufSize int) (<-chan memengine.WALRecord, func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, nil, ErrStreamClosed
	}

	if bufSize <= 0 {
		bufSize = 1024
	}

	ch := make(chan memengine.WALRecord, bufSize)
	sub := &subscriber{
		id:         replicaID,
		remoteAddr: remoteAddr,
		ch:         ch,
		ackedLSN:   fromLSN,
		lastSeen:   time.Now(),
		connected:  time.Now(),
	}

	// If existing subscriber with same ID, close old one
	if old, exists := b.subscribers[replicaID]; exists {
		close(old.ch)
	}
	b.subscribers[replicaID] = sub

	// Replay history if fromLSN is within our buffer range
	for i := 0; i < b.historyLen; i++ {
		hIdx := (b.historyHead + i) % b.historyCap
		rec := b.history[hIdx]
		if rec.LSN > fromLSN {
			select {
			case ch <- rec:
			default:
				// Ring buffer replay best effort
			}
		}
	}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if current, exists := b.subscribers[replicaID]; exists && current == sub {
				delete(b.subscribers, replicaID)
				close(sub.ch)
			}
		})
	}

	return ch, cancel, nil
}

// UpdateAck updates the last acknowledged LSN and heartbeat timestamp from a replica.
func (b *WALBroadcaster) UpdateAck(replicaID string, lsn int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if sub, exists := b.subscribers[replicaID]; exists {
		if lsn > sub.ackedLSN {
			sub.ackedLSN = lsn
		}
		sub.lastSeen = time.Now()
	}
}

// GetReplicas returns a snapshot of connected replica states.
func (b *WALBroadcaster) GetReplicas(primaryLSN int64) []ReplicaInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()

	result := make([]ReplicaInfo, 0, len(b.subscribers))
	now := time.Now()
	for _, sub := range b.subscribers {
		lag := primaryLSN - sub.ackedLSN
		if lag < 0 {
			lag = 0
		}
		state := "STREAMING"
		if now.Sub(sub.lastSeen) > 10*time.Second {
			state = "DISCONNECTED"
		} else if lag > 100 {
			state = "CATCHING_UP"
		}

		result = append(result, ReplicaInfo{
			NodeID:         sub.id,
			RemoteAddr:     sub.remoteAddr,
			ConnectedAt:    sub.connected,
			LastHeartbeat:  sub.lastSeen,
			AckedLSN:       sub.ackedLSN,
			ReplicationLag: lag,
			State:          state,
		})
	}
	return result
}

// Close shuts down the broadcaster and closes all subscriber channels.
func (b *WALBroadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	for _, sub := range b.subscribers {
		close(sub.ch)
	}
	b.subscribers = make(map[string]*subscriber)
}
