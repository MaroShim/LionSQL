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

func TestWALBroadcaster_SubscribeAndBroadcast(t *testing.T) {
	b := replication.NewWALBroadcaster(10)
	defer b.Close()

	// 1. Initial broadcast before subscriber connects
	b.Broadcast(memengine.WALRecord{LSN: 1, Ops: []memengine.WALOp{{Type: memengine.OpCreateTable}}})
	b.Broadcast(memengine.WALRecord{LSN: 2, Ops: []memengine.WALOp{{Type: memengine.OpCreateTable}}})

	// 2. Subscribe from LSN 1 (should receive LSN 2 from ring buffer)
	ch, cancel, err := b.Subscribe("replica-1", "127.0.0.1:5001", 1, 100)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer cancel()

	select {
	case rec := <-ch:
		if rec.LSN != 2 {
			t.Fatalf("expected LSN 2, got %d", rec.LSN)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for backlog record")
	}

	// 3. Real-time broadcast while subscribed
	b.Broadcast(memengine.WALRecord{LSN: 3, Ops: []memengine.WALOp{{Type: memengine.OpCreateTable}}})

	select {
	case rec := <-ch:
		if rec.LSN != 3 {
			t.Fatalf("expected LSN 3, got %d", rec.LSN)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live record")
	}

	// 4. UpdateAck and verify lag
	b.UpdateAck("replica-1", 3)
	replicas := b.GetReplicas(3)
	if len(replicas) != 1 {
		t.Fatalf("expected 1 replica, got %d", len(replicas))
	}
	if replicas[0].ReplicationLag != 0 {
		t.Fatalf("expected 0 lag, got %d", replicas[0].ReplicationLag)
	}
}

func TestManager_RoleAndPromote(t *testing.T) {
	eng := memengine.New()

	mgr, err := replication.NewManager(replication.Config{
		NodeID: "node-replica",
		Role:   replication.RoleReplica,
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	defer mgr.Close()

	if !mgr.IsReadOnly() {
		t.Fatal("expected replica to be read-only")
	}
	if mgr.Role() != replication.RoleReplica {
		t.Fatalf("expected role REPLICA, got %s", mgr.Role())
	}

	// Promote to primary
	if err := mgr.Promote(); err != nil {
		t.Fatalf("failed to promote: %v", err)
	}

	if mgr.IsReadOnly() {
		t.Fatal("expected primary not to be read-only")
	}
	if mgr.Role() != replication.RolePrimary {
		t.Fatalf("expected role PRIMARY, got %s", mgr.Role())
	}

	// Promote again should return ErrAlreadyPrimary
	if err := mgr.Promote(); err != replication.ErrAlreadyPrimary {
		t.Fatalf("expected ErrAlreadyPrimary, got %v", err)
	}
}

func TestManager_EngineWALHookIntegration(t *testing.T) {
	eng := memengine.New()

	mgr, err := replication.NewManager(replication.Config{
		NodeID: "primary-1",
		Role:   replication.RolePrimary,
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	defer mgr.Close()

	ch, cancel, err := mgr.Broadcaster().Subscribe("replica-sub", "127.0.0.1:9000", 0, 100)
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}
	defer cancel()

	// Perform DDL on primary engine
	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// Verify WAL record was broadcast to subscriber
	select {
	case rec := <-ch:
		if rec.LSN != 1 {
			t.Fatalf("expected LSN 1, got %d", rec.LSN)
		}
		if len(rec.Ops) != 1 || rec.Ops[0].Type != memengine.OpCreateTable {
			t.Fatalf("unexpected op: %+v", rec.Ops)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broadcast from engine commit")
	}

	// Verify CurrentLSN
	if eng.CurrentLSN() != 1 {
		t.Fatalf("expected current LSN 1, got %d", eng.CurrentLSN())
	}
}

func TestManager_ContextCancellation(t *testing.T) {
	eng := memengine.New()
	mgr, err := replication.NewManager(replication.Config{
		NodeID: "test-node",
		Role:   replication.RolePrimary,
		Engine: eng,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = mgr.Close()
}

func TestReplication_EndToEndStreamingAndFailover(t *testing.T) {
	// 1. Setup Primary Node
	primaryEng := memengine.New()
	primaryMgr, err := replication.NewManager(replication.Config{
		NodeID: "primary-node",
		Role:   replication.RolePrimary,
		Engine: primaryEng,
	})
	if err != nil {
		t.Fatalf("failed to create primary manager: %v", err)
	}
	defer primaryMgr.Close()

	mux := http.NewServeMux()
	primaryMgr.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := primaryMgr.Start(ctx); err != nil {
		t.Fatalf("failed to start primary manager: %v", err)
	}

	// 2. Setup Secondary Replica Node
	replicaEng := memengine.New()
	replicaMgr, err := replication.NewManager(replication.Config{
		NodeID:     "replica-node",
		Role:       replication.RoleReplica,
		PrimaryURL: ts.URL,
		Engine:     replicaEng,
	})
	if err != nil {
		t.Fatalf("failed to create replica manager: %v", err)
	}
	defer replicaMgr.Close()

	if err := replicaMgr.Start(ctx); err != nil {
		t.Fatalf("failed to start replica manager: %v", err)
	}

	if !replicaMgr.IsReadOnly() {
		t.Fatal("expected replica to be in read-only mode")
	}

	// 3. Mutate Primary: DDL + DML
	schema := &storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "price", Type: storage.TypeInt},
		},
	}
	if err := primaryEng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed on primary: %v", err)
	}

	sess, err := primaryEng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed on primary: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = tx.Insert("products", storage.Record{
		"id":    storage.IntValue(10),
		"name":  storage.StringValue("laptop"),
		"price": storage.IntValue(1200),
	})
	_ = tx.Insert("products", storage.Record{
		"id":    storage.IntValue(20),
		"name":  storage.StringValue("mouse"),
		"price": storage.IntValue(25),
	})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit tx failed: %v", err)
	}

	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx tx2 failed: %v", err)
	}
	_ = tx2.Update("products", storage.IntValue(10), storage.Record{
		"price": storage.IntValue(1150),
	})
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit tx2 failed: %v", err)
	}

	expectedLSN := primaryEng.CurrentLSN()

	// 4. Poll deterministically until replica catches up
	deadline := time.Now().Add(5 * time.Second)
	caughtUp := false
	for time.Now().Before(deadline) {
		if replicaEng.CurrentLSN() >= expectedLSN {
			caughtUp = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !caughtUp {
		t.Fatalf("timed out waiting for replica to catch up to LSN %d (current: %d)", expectedLSN, replicaEng.CurrentLSN())
	}

	// 5. Verify data on Replica
	repSess, err := replicaEng.NewSession()
	if err != nil {
		t.Fatalf("NewSession on replica failed: %v", err)
	}
	defer repSess.Close()

	repTx, err := repSess.BeginTx(storage.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx on replica failed: %v", err)
	}
	defer repTx.Rollback()

	prod, err := repTx.Get("products", storage.IntValue(10))
	if err != nil {
		t.Fatalf("Get product 10 on replica failed: %v", err)
	}
	if prod["price"].IVal != 1150 {
		t.Errorf("expected product price=1150, got %d", prod["price"].IVal)
	}

	// 6. Test Failover Promotion
	if err := replicaMgr.Promote(); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}
	if replicaMgr.IsReadOnly() {
		t.Fatal("expected promoted node not to be read-only")
	}
	if replicaMgr.Role() != replication.RolePrimary {
		t.Fatalf("expected promoted node role PRIMARY, got %s", replicaMgr.Role())
	}

	// Promoted node can now accept write transactions
	newTx, err := repSess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx on promoted node failed: %v", err)
	}
	_ = newTx.Insert("products", storage.Record{
		"id":    storage.IntValue(30),
		"name":  storage.StringValue("keyboard"),
		"price": storage.IntValue(75),
	})
	if err := newTx.Commit(); err != nil {
		t.Fatalf("Commit on promoted node failed: %v", err)
	}

	newProd, err := repTx.Get("products", storage.IntValue(30))
	if err != nil {
		t.Fatalf("failed to retrieve newly written record from promoted node: %v", err)
	}
	if newProd["name"].SVal != "keyboard" {
		t.Errorf("expected keyboard, got %v", newProd["name"].SVal)
	}
}

