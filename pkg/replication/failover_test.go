package replication_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sqlion/pkg/replication"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

// TestLeaderElection_LSNPrecedence verifies that candidate replicas with lower LSN cannot win votes
// over replicas with higher LSN, guaranteeing zero-data-loss Always On failover semantics.
func TestLeaderElection_LSNPrecedence(t *testing.T) {
	engA := memengine.New()
	mgrA, err := replication.NewManager(replication.Config{
		NodeID:      "node-a",
		Role:        replication.RoleReplica,
		Engine:      engA,
		InitialLSN:  100,
		InitialTerm: 1,
	})
	if err != nil {
		t.Fatalf("failed to create mgrA: %v", err)
	}
	defer mgrA.Close()

	// 1. Candidate C with lower LSN (LSN 80 < 100) requests vote
	resp := mgrA.HandleVote(replication.VoteRequest{
		Term:         2,
		CandidateID:  "node-c",
		CandidateURL: "http://127.0.0.1:8093",
		LastLSN:      80,
	})
	if resp.VoteGranted {
		t.Fatalf("expected vote denied for candidate with lower LSN (80 < 100)")
	}

	// 2. Candidate B with higher LSN (LSN 120 >= 100) requests vote in term 2
	resp2 := mgrA.HandleVote(replication.VoteRequest{
		Term:         2,
		CandidateID:  "node-b",
		CandidateURL: "http://127.0.0.1:8092",
		LastLSN:      120,
	})
	if !resp2.VoteGranted {
		t.Fatalf("expected vote granted for candidate with higher LSN (120 >= 100), reason: %s", resp2.Reason)
	}

	// 3. Second candidate asks in same term 2 -> denied (one vote per term)
	resp3 := mgrA.HandleVote(replication.VoteRequest{
		Term:         2,
		CandidateID:  "node-d",
		CandidateURL: "http://127.0.0.1:8094",
		LastLSN:      150,
	})
	if resp3.VoteGranted {
		t.Fatalf("expected vote denied in same term 2 (already voted for node-b)")
	}

	// 4. Stale term request (Term 1 < 2) -> denied
	respStale := mgrA.HandleVote(replication.VoteRequest{
		Term:         1,
		CandidateID:  "node-e",
		CandidateURL: "http://127.0.0.1:8095",
		LastLSN:      200,
	})
	if respStale.VoteGranted {
		t.Fatalf("expected vote denied for stale term")
	}
}

// TestLeaderElection_SplitBrainPrevention verifies that a primary node receiving a higher
// term leader announcement immediately steps down to a replica, preventing split-brain writes.
func TestLeaderElection_SplitBrainPrevention(t *testing.T) {
	eng := memengine.New()
	mgr, err := replication.NewManager(replication.Config{
		NodeID:      "primary-old",
		Role:        replication.RolePrimary,
		Engine:      eng,
		InitialTerm: 1,
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	defer mgr.Close()

	if mgr.Role() != replication.RolePrimary || mgr.IsReadOnly() {
		t.Fatalf("expected active read-write primary")
	}

	// A higher term announcement arrives from a newly elected leader
	err = mgr.HandleLeaderAnnouncement(replication.LeaderAnnouncement{
		Term:      2,
		LeaderID:  "primary-new",
		LeaderURL: "http://127.0.0.1:8091",
	})
	if err != nil {
		t.Fatalf("failed to handle announcement: %v", err)
	}

	// Verify old primary immediately stepped down to prevent split-brain
	if mgr.Role() != replication.RoleReplica {
		t.Fatalf("expected old primary to demote to REPLICA, got %s", mgr.Role())
	}
	if !mgr.IsReadOnly() {
		t.Fatalf("expected demoted node to be strictly read-only")
	}
	if mgr.Term() != 2 {
		t.Fatalf("expected term 2, got %d", mgr.Term())
	}
	if mgr.LeaderID() != "primary-new" {
		t.Fatalf("expected leader ID primary-new, got %s", mgr.LeaderID())
	}
}

// TestLeaderElection_ElectLeader_Quorum verifies election vote gathering across HTTP peers.
func TestLeaderElection_ElectLeader_Quorum(t *testing.T) {
	eng1 := memengine.New()
	mgr1, _ := replication.NewManager(replication.Config{
		NodeID:       "node-1",
		Role:         replication.RoleReplica,
		Engine:       eng1,
		AutoFailover: true,
		InitialTerm:  1,
	})
	defer mgr1.Close()

	mux1 := http.NewServeMux()
	mgr1.RegisterRoutes(mux1)
	ts1 := httptest.NewServer(mux1)
	defer ts1.Close()
	mgr1.SetSelfURL(ts1.URL)

	eng2 := memengine.New()
	mgr2, _ := replication.NewManager(replication.Config{
		NodeID:       "node-2",
		Role:         replication.RoleReplica,
		Engine:       eng2,
		AutoFailover: true,
		InitialTerm:  1,
	})
	defer mgr2.Close()

	mux2 := http.NewServeMux()
	mgr2.RegisterRoutes(mux2)
	ts2 := httptest.NewServer(mux2)
	defer ts2.Close()
	mgr2.SetSelfURL(ts2.URL)

	// Configure peers for node-1 (node-2 is peer)
	mgr1.SetPeers([]string{ts2.URL})
	mgr2.SetPeers([]string{ts1.URL})

	// Elect leader on node-1 (cluster size: 2, quorum: 2)
	ctx := context.Background()
	won, err := mgr1.ElectLeader(ctx)
	if err != nil {
		t.Fatalf("ElectLeader failed: %v", err)
	}
	if !won {
		t.Fatalf("expected node-1 to win election with majority quorum")
	}

	if mgr1.Role() != replication.RolePrimary {
		t.Fatalf("expected node-1 to become PRIMARY, got %s", mgr1.Role())
	}
	if mgr1.Term() != 2 {
		t.Fatalf("expected term 2, got %d", mgr1.Term())
	}
}

// TestLeaderElection_EndToEndAutoFailover tests automatic primary death detection,
// leader election, and seamless failover to the highest LSN replica.
func TestLeaderElection_EndToEndAutoFailover(t *testing.T) {
	// 1. Setup Primary (node-0)
	priEng := memengine.New()
	priMgr, err := replication.NewManager(replication.Config{
		NodeID:      "node-0",
		Role:        replication.RolePrimary,
		Engine:      priEng,
		InitialTerm: 1,
	})
	if err != nil {
		t.Fatalf("failed to create primary manager: %v", err)
	}

	priMux := http.NewServeMux()
	priMgr.RegisterRoutes(priMux)
	priServer := httptest.NewServer(priMux)
	priMgr.SetSelfURL(priServer.URL)

	priCtx, priCancel := context.WithCancel(context.Background())
	defer priCancel()

	if err := priMgr.Start(priCtx); err != nil {
		t.Fatalf("failed to start primary: %v", err)
	}

	// 2. Setup Replica 1 (node-1)
	rep1Eng := memengine.New()
	rep1Mgr, err := replication.NewManager(replication.Config{
		NodeID:           "node-1",
		Role:             replication.RoleReplica,
		PrimaryURL:       priServer.URL,
		Engine:           rep1Eng,
		AutoFailover:     true,
		HeartbeatTimeout: 600 * time.Millisecond,
		ElectionTimeout:  500 * time.Millisecond,
		Quorum:           1, // In failover with dead primary, 1 vote suffices or self+rep2
		InitialTerm:      1,
	})
	if err != nil {
		t.Fatalf("failed to create rep1 manager: %v", err)
	}
	defer rep1Mgr.Close()

	rep1Mux := http.NewServeMux()
	rep1Mgr.RegisterRoutes(rep1Mux)
	rep1Server := httptest.NewServer(rep1Mux)
	defer rep1Server.Close()
	rep1Mgr.SetSelfURL(rep1Server.URL)

	repCtx, repCancel := context.WithCancel(context.Background())
	defer repCancel()

	if err := rep1Mgr.Start(repCtx); err != nil {
		t.Fatalf("failed to start rep1: %v", err)
	}

	// 3. Write data to Primary and verify Replica 1 catches up
	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
		},
	}
	if err := priEng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable on primary failed: %v", err)
	}

	sess, _ := priEng.NewSession()
	tx, _ := sess.BeginTx(storage.TxOptions{})
	_ = tx.Insert("accounts", storage.Record{"id": storage.IntValue(1), "balance": storage.IntValue(500)})
	_ = tx.Commit()
	sess.Close()

	targetLSN := priEng.CurrentLSN()

	deadline := time.Now().Add(5 * time.Second)
	caughtUp := false
	for time.Now().Before(deadline) {
		if rep1Eng.CurrentLSN() >= targetLSN {
			caughtUp = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !caughtUp {
		t.Fatalf("replica 1 timed out catching up to LSN %d (current: %d)", targetLSN, rep1Eng.CurrentLSN())
	}

	// 4. Primary node dies (cancel context, close manager, close server)
	priCancel()
	_ = priMgr.Close()
	priServer.CloseClientConnections()
	priServer.Close()

	// 5. Automatic Failover: Replica 1 should detect heartbeat timeout and promote to Primary
	promotedDeadline := time.Now().Add(5 * time.Second)
	promoted := false
	for time.Now().Before(promotedDeadline) {
		if rep1Mgr.Role() == replication.RolePrimary {
			promoted = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !promoted {
		t.Fatalf("replica 1 failed to auto-promote to PRIMARY after primary death (role: %s)", rep1Mgr.Role())
	}

	if rep1Mgr.IsReadOnly() {
		t.Fatalf("promoted node should not be read-only")
	}

	// 6. Promoted node can now accept write transactions
	rep1Sess, err := rep1Eng.NewSession()
	if err != nil {
		t.Fatalf("failed to open session on promoted node: %v", err)
	}
	defer rep1Sess.Close()

	writeTx, err := rep1Sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("failed to begin write tx on promoted node: %v", err)
	}
	_ = writeTx.Insert("accounts", storage.Record{"id": storage.IntValue(2), "balance": storage.IntValue(999)})
	if err := writeTx.Commit(); err != nil {
		t.Fatalf("failed to commit write on newly elected primary: %v", err)
	}

	// Verify newly inserted record exists
	readTx, _ := rep1Sess.BeginTx(storage.TxOptions{ReadOnly: true})
	defer readTx.Rollback()
	rec, err := readTx.Get("accounts", storage.IntValue(2))
	if err != nil || rec["balance"].IVal != 999 {
		t.Fatalf("failed to retrieve record from newly elected primary: %v", err)
	}
}
