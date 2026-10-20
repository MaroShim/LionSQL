package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/replication"
	"lionsql/pkg/storage/memengine"
)

func TestReplication_ServerIntegration(t *testing.T) {
	// 1. Primary Node Setup
	primaryEng := memengine.New()
	if err := primaryEng.Open(""); err != nil {
		t.Fatalf("Failed to open primary engine: %v", err)
	}
	defer primaryEng.Close()
	primaryCat := catalog.New(primaryEng)
	primarySrv := NewServer(primaryCat, primaryEng)

	primaryRep, err := replication.NewManager(replication.Config{
		NodeID:     "primary-node",
		Role:       replication.RolePrimary,
		HistoryCap: 1000,
		Engine:     primaryEng,
	})
	if err != nil {
		t.Fatalf("Failed to create primary replication manager: %v", err)
	}
	defer primaryRep.Close()
	primarySrv.SetReplicationManager(primaryRep)
	if err := primaryRep.Start(context.Background()); err != nil {
		t.Fatalf("Failed to start primary replication: %v", err)
	}

	primaryHTTP := httptest.NewServer(primarySrv.Handler())
	defer primaryHTTP.Close()

	// 2. Replica Node Setup
	replicaEng := memengine.New()
	if err := replicaEng.Open(""); err != nil {
		t.Fatalf("Failed to open replica engine: %v", err)
	}
	defer replicaEng.Close()
	replicaCat := catalog.New(replicaEng)
	replicaSrv := NewServer(replicaCat, replicaEng)

	replicaRep, err := replication.NewManager(replication.Config{
		NodeID:     "replica-node",
		Role:       replication.RoleReplica,
		PrimaryURL: primaryHTTP.URL,
		HistoryCap: 1000,
		Engine:     replicaEng,
	})
	if err != nil {
		t.Fatalf("Failed to create replica replication manager: %v", err)
	}
	defer replicaRep.Close()
	replicaSrv.SetReplicationManager(replicaRep)
	if err := replicaRep.Start(context.Background()); err != nil {
		t.Fatalf("Failed to start replica replication: %v", err)
	}

	// 3. Verify Replica is Read-Only
	if !replicaSrv.IsReadOnly() {
		t.Fatalf("Expected replica server to be read-only")
	}

	// 4. Test that write operations fail on Replica
	writeQueries := []string{
		"CREATE TABLE test_tab (id INT PRIMARY KEY, val VARCHAR(50))",
		"INSERT INTO accounts (id, holder, balance) VALUES (999, 'bad', 100)",
		"UPDATE accounts SET balance = 500 WHERE id = 1",
		"DELETE FROM accounts WHERE id = 1",
		"DROP TABLE accounts",
		"TRUNCATE TABLE accounts",
		"ALTER TABLE accounts ADD col2 INT",
	}

	for _, q := range writeQueries {
		_, err := replicaSrv.ExecuteSQL(q)
		if err == nil {
			t.Fatalf("Expected write query %q to fail on read-only replica, but got success", q)
		}
		if !strings.Contains(err.Error(), "read-only replica") {
			t.Fatalf("Expected read-only replica error for query %q, got: %v", q, err)
		}
	}

	// 5. Test that interactive READ WRITE transaction fails on Replica
	_, err = replicaSrv.ExecuteSQL("BEGIN TRANSACTION READ WRITE")
	if err == nil {
		t.Fatalf("Expected BEGIN TRANSACTION READ WRITE to fail on replica")
	}

	// 6. Execute write on Primary and verify replication stream catches up
	_, err = primarySrv.ExecuteSQL("CREATE TABLE rep_items (id INT PRIMARY KEY, name VARCHAR(50), qty INT)")
	if err != nil {
		t.Fatalf("Primary failed to CREATE TABLE: %v", err)
	}
	for i := 1; i <= 5; i++ {
		_, err = primarySrv.ExecuteSQL(fmt.Sprintf("INSERT INTO rep_items (id, name, qty) VALUES (%d, 'item_%d', %d)", i, i, i*10))
		if err != nil {
			t.Fatalf("Primary failed to INSERT row %d: %v", i, err)
		}
	}

	// Give streaming a brief moment to sync deterministically
	deadline := time.Now().Add(3 * time.Second)
	replicated := false
	for time.Now().Before(deadline) {
		res, err := replicaSrv.ExecuteSQL("SELECT COUNT(*) AS cnt FROM rep_items")
		if err == nil && len(res.Rows) > 0 {
			if cnt, ok := res.Rows[0]["cnt"].(int64); ok && cnt == 5 {
				replicated = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !replicated {
		t.Fatalf("Replica did not receive 5 replicated records within deadline")
	}

	// 7. Verify SELECT queries succeed on Replica
	selRes, err := replicaSrv.ExecuteSQL("SELECT * FROM rep_items WHERE id = 3")
	if err != nil {
		t.Fatalf("Replica SELECT query failed: %v", err)
	}
	if len(selRes.Rows) != 1 || selRes.Rows[0]["name"] != "item_3" {
		t.Fatalf("Replica SELECT returned unexpected data: %+v", selRes.Rows)
	}

	// 8. Test Failover Promotion: Promote replica to Primary
	if err := replicaRep.Promote(); err != nil {
		t.Fatalf("Failed to promote replica: %v", err)
	}
	if replicaSrv.IsReadOnly() {
		t.Fatalf("Promoted node should no longer be read-only")
	}

	// 9. Verify formerly replica node can now accept writes directly
	_, err = replicaSrv.ExecuteSQL("INSERT INTO rep_items (id, name, qty) VALUES (100, 'promoted_item', 999)")
	if err != nil {
		t.Fatalf("Promoted node failed to execute direct write: %v", err)
	}
	postRes, err := replicaSrv.ExecuteSQL("SELECT * FROM rep_items WHERE id = 100")
	if err != nil || len(postRes.Rows) != 1 {
		t.Fatalf("Promoted node failed to query inserted row: %v", err)
	}
}
