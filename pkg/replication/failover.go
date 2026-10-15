package replication

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func (m *Manager) startFailoverMonitor(ctx context.Context) {
	failoverCtx, cancel := context.WithCancel(ctx)
	m.failoverCancel = cancel
	m.failoverWg.Add(1)
	go m.runFailoverLoop(failoverCtx)
}

func (m *Manager) runFailoverLoop(ctx context.Context) {
	defer m.failoverWg.Done()

	checkInterval := m.heartbeatTimeout / 3
	if checkInterval < 100*time.Millisecond {
		checkInterval = 100 * time.Millisecond
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.RLock()
			isReplica := m.role == RoleReplica
			autoFailover := m.autoFailover
			closed := m.closed
			hbTimeout := m.heartbeatTimeout
			m.mu.RUnlock()

			if !isReplica || !autoFailover || closed {
				continue
			}

			lastHB := m.LastHeartbeatTime()
			if !lastHB.IsZero() && time.Since(lastHB) > hbTimeout {
				// Primary heartbeat timeout detected: trigger election
				_, _ = m.ElectLeader(ctx)
			}
		}
	}
}

// ElectLeader initiates a leader election cycle adhering to Always On highest-LSN precedence.
func (m *Manager) ElectLeader(ctx context.Context) (bool, error) {
	m.mu.Lock()
	if m.role == RolePrimary || !m.autoFailover || m.closed {
		m.mu.Unlock()
		return false, nil
	}

	m.term++
	candidateTerm := m.term
	candidateID := m.nodeID
	candidateURL := m.selfURL
	m.votedFor = m.nodeID
	m.leaderID = ""

	var candidateLSN int64
	if m.eng != nil {
		candidateLSN = m.eng.CurrentLSN()
	} else if m.client != nil {
		candidateLSN = m.client.LastLSN()
	}
	if candidateLSN < m.initialLSN {
		candidateLSN = m.initialLSN
	}

	peers := make([]string, len(m.peers))
	copy(peers, m.peers)

	quorum := m.quorum
	if quorum <= 0 {
		quorum = (len(peers)+1)/2 + 1
	}
	m.mu.Unlock()

	var votes atomic.Int32
	votes.Store(1) // Vote for self

	if int(votes.Load()) >= quorum {
		if err := m.becomeLeader(candidateTerm); err != nil {
			return false, err
		}
		return true, nil
	}

	voteReq := VoteRequest{
		Term:         candidateTerm,
		CandidateID:  candidateID,
		CandidateURL: candidateURL,
		LastLSN:      candidateLSN,
	}

	electionTimeout := m.electionTimeout
	if electionTimeout <= 0 {
		electionTimeout = 1500 * time.Millisecond
	}
	electionCtx, cancel := context.WithTimeout(ctx, electionTimeout)
	defer cancel()

	var wg sync.WaitGroup
	var higherTerm atomic.Int64

	for _, peer := range peers {
		cleanPeer := strings.TrimRight(strings.TrimSpace(peer), "/")
		if cleanPeer == "" || cleanPeer == candidateURL {
			continue
		}

		wg.Add(1)
		go func(peerURL string) {
			defer wg.Done()
			resp, err := m.requestVote(electionCtx, peerURL, voteReq)
			if err != nil {
				return
			}
			if resp.Term > candidateTerm {
				higherTerm.Store(resp.Term)
				return
			}
			if resp.VoteGranted {
				votes.Add(1)
			}
		}(cleanPeer)
	}

	wg.Wait()

	if ht := higherTerm.Load(); ht > candidateTerm {
		m.mu.Lock()
		if ht > m.term {
			m.term = ht
			m.votedFor = ""
			m.leaderID = ""
		}
		m.mu.Unlock()
		return false, ErrSplitBrain
	}

	if int(votes.Load()) >= quorum {
		if err := m.becomeLeader(candidateTerm); err != nil {
			return false, err
		}
		return true, nil
	}

	return false, ErrElectionFailed
}

func (m *Manager) becomeLeader(term int64) error {
	m.mu.Lock()
	if m.term != term || m.closed {
		m.mu.Unlock()
		return ErrStaleTerm
	}

	// 1. Detach streaming client
	client := m.client
	m.client = nil

	// 2. Transition role to Primary
	m.role = RolePrimary
	m.leaderID = m.nodeID
	m.primaryURL = ""
	candidateURL := m.selfURL
	peers := make([]string, len(m.peers))
	copy(peers, m.peers)
	m.mu.Unlock()

	// 3. Stop client outside the lock
	if client != nil {
		client.Stop()
	}

	// 4. Asynchronously broadcast leader announcement to all peers
	if len(peers) > 0 {
		go m.broadcastLeaderAnnouncement(term, m.nodeID, candidateURL, peers)
	}

	return nil
}

// HandleVote evaluates a candidate's vote request and grants or denies the vote.
func (m *Manager) HandleVote(req VoteRequest) VoteResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Term freshness check
	if req.Term < m.term {
		return VoteResponse{
			Term:        m.term,
			VoteGranted: false,
			Reason:      "stale term",
		}
	}

	// 2. Term step-up: if term is higher, update term and fence local primary if applicable
	if req.Term > m.term {
		m.term = req.Term
		m.votedFor = ""
		m.leaderID = ""
		if m.role == RolePrimary {
			// Fencing: demote old primary to replica to eliminate split-brain write skew
			m.role = RoleReplica
			m.primaryURL = req.CandidateURL
			m.restartClientLocked()
		}
	}

	// 3. Zero-loss LSN Precedence Rule:
	// Candidate's LSN must be >= local node's current LSN
	var localLSN int64
	if m.eng != nil {
		localLSN = m.eng.CurrentLSN()
	} else if m.client != nil {
		localLSN = m.client.LastLSN()
	}
	if localLSN < m.initialLSN {
		localLSN = m.initialLSN
	}

	if req.LastLSN < localLSN {
		return VoteResponse{
			Term:        m.term,
			VoteGranted: false,
			Reason:      fmt.Sprintf("candidate LSN %d is behind local LSN %d", req.LastLSN, localLSN),
		}
	}

	// 4. Single-vote invariant per term
	if m.votedFor == "" || m.votedFor == req.CandidateID {
		m.votedFor = req.CandidateID
		m.resetHeartbeatLocked()
		return VoteResponse{
			Term:        m.term,
			VoteGranted: true,
		}
	}

	return VoteResponse{
		Term:        m.term,
		VoteGranted: false,
		Reason:      "already voted for " + m.votedFor,
	}
}

// HandleLeaderAnnouncement acknowledges a newly elected leader and synchronizes cluster topology.
func (m *Manager) HandleLeaderAnnouncement(ann LeaderAnnouncement) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ann.Term < m.term {
		return ErrStaleTerm
	}

	if ann.Term > m.term {
		m.term = ann.Term
		m.votedFor = ""
	}
	m.leaderID = ann.LeaderID

	// Fencing: If this node was previously primary, demote immediately
	if m.role == RolePrimary && ann.LeaderID != m.nodeID {
		m.role = RoleReplica
		m.primaryURL = ann.LeaderURL
		m.restartClientLocked()
	} else if m.role == RoleReplica {
		if ann.LeaderURL != "" && ann.LeaderURL != m.primaryURL {
			m.primaryURL = ann.LeaderURL
			m.restartClientLocked()
		}
	}

	m.resetHeartbeatLocked()
	return nil
}

// HandlePrimaryTerm checks for a higher term reported by primary stream headers.
func (m *Manager) HandlePrimaryTerm(primaryTerm int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if primaryTerm > m.term {
		m.term = primaryTerm
		m.votedFor = ""
		if m.role == RolePrimary {
			m.role = RoleReplica
		}
	}
}

func (m *Manager) requestVote(ctx context.Context, peerURL string, req VoteRequest) (*VoteResponse, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/replication/vote", strings.TrimRight(peerURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vote request returned status %d", resp.StatusCode)
	}

	var voteResp VoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&voteResp); err != nil {
		return nil, err
	}
	return &voteResp, nil
}

func (m *Manager) broadcastLeaderAnnouncement(term int64, leaderID, leaderURL string, peers []string) {
	ann := LeaderAnnouncement{
		Term:      term,
		LeaderID:  leaderID,
		LeaderURL: leaderURL,
	}
	data, err := json.Marshal(ann)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 2 * time.Second}
	var wg sync.WaitGroup

	for _, peer := range peers {
		cleanPeer := strings.TrimRight(strings.TrimSpace(peer), "/")
		if cleanPeer == "" || cleanPeer == leaderURL {
			continue
		}

		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			url := fmt.Sprintf("%s/api/replication/leader", target)
			req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
		}(cleanPeer)
	}

	wg.Wait()
}
