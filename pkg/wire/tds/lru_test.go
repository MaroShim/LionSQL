package tds

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage/memengine"
)

func TestTdsPreparedStatementCache_BasicOperations(t *testing.T) {
	cache := NewTdsPreparedStatementCache(3)
	if cache.Capacity() != 3 {
		t.Fatalf("expected capacity 3, got %d", cache.Capacity())
	}
	if cache.Len() != 0 {
		t.Fatalf("expected len 0, got %d", cache.Len())
	}

	prep1 := &TdsPreparedStatement{Handle: 1, Query: "SELECT 1"}
	cache.Put(prep1)
	if cache.Len() != 1 {
		t.Fatalf("expected len 1, got %d", cache.Len())
	}

	got, exists := cache.Get(1)
	if !exists || got.Query != "SELECT 1" {
		t.Fatalf("expected to get prep1, got %v, exists=%v", got, exists)
	}

	// Update existing handle
	prep1Updated := &TdsPreparedStatement{Handle: 1, Query: "SELECT 100"}
	cache.Put(prep1Updated)
	if cache.Len() != 1 {
		t.Fatalf("expected len 1 after update, got %d", cache.Len())
	}
	gotUpdated, exists := cache.Get(1)
	if !exists || gotUpdated.Query != "SELECT 100" {
		t.Fatalf("expected updated query 'SELECT 100', got %v", gotUpdated)
	}

	// Delete
	cache.Delete(1)
	if cache.Len() != 0 {
		t.Fatalf("expected len 0 after delete, got %d", cache.Len())
	}
	if _, exists := cache.Get(1); exists {
		t.Fatalf("expected handle 1 to be deleted")
	}
}

func TestTdsPreparedStatementCache_LRUEviction(t *testing.T) {
	cache := NewTdsPreparedStatementCache(3)

	cache.Put(&TdsPreparedStatement{Handle: 1, Query: "Q1"})
	cache.Put(&TdsPreparedStatement{Handle: 2, Query: "Q2"})
	cache.Put(&TdsPreparedStatement{Handle: 3, Query: "Q3"})

	if cache.Len() != 3 {
		t.Fatalf("expected len 3, got %d", cache.Len())
	}

	// Adding 4th statement evicts handle 1 (the oldest / least recently used)
	cache.Put(&TdsPreparedStatement{Handle: 4, Query: "Q4"})
	if cache.Len() != 3 {
		t.Fatalf("expected len 3 after eviction, got %d", cache.Len())
	}
	if _, exists := cache.Get(1); exists {
		t.Fatalf("expected handle 1 to be evicted")
	}
	for _, h := range []int32{2, 3, 4} {
		if _, exists := cache.Get(h); !exists {
			t.Fatalf("expected handle %d to exist", h)
		}
	}

	// Access handle 2 to promote it to MRU (front). LRU order is now: 3 (oldest), 4, 2 (newest)
	if _, exists := cache.Get(2); !exists {
		t.Fatalf("handle 2 should exist")
	}

	// Adding handle 5 should now evict handle 3 (not 2!)
	cache.Put(&TdsPreparedStatement{Handle: 5, Query: "Q5"})
	if _, exists := cache.Get(3); exists {
		t.Fatalf("expected handle 3 to be evicted after handle 2 was promoted")
	}
	if _, exists := cache.Get(2); !exists {
		t.Fatalf("expected handle 2 to still exist")
	}
	if _, exists := cache.Get(4); !exists {
		t.Fatalf("expected handle 4 to still exist")
	}
	if _, exists := cache.Get(5); !exists {
		t.Fatalf("expected handle 5 to exist")
	}
}

func TestTdsPreparedStatementCache_Concurrency(t *testing.T) {
	cache := NewTdsPreparedStatementCache(10)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h := int32(workerID*100 + j)
				cache.Put(&TdsPreparedStatement{Handle: h, Query: fmt.Sprintf("Q_%d", h)})
				_, _ = cache.Get(h)
				if j%5 == 0 {
					cache.Delete(h)
				}
			}
		}(i)
	}

	wg.Wait()
	if cache.Len() > 10 {
		t.Fatalf("expected cache len <= 10, got %d", cache.Len())
	}
}

func TestTDS_PreparedStatement_BoundedLRUEviction_E2E(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)
	planCache := runtime.NewPlanCache()
	srv := NewServerWithCache("127.0.0.1:0", cat, eng, planCache)
	// Set small capacity of 3 for deterministic LRU eviction testing
	srv.SetMaxPreparedPerSession(3)

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Close()

	conn := connectClient(t, srv)
	defer conn.Close()
	performHandshake(t, conn)

	_ = sendSQLBatch(t, conn, "CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(100));")
	_ = sendSQLBatch(t, conn, "INSERT INTO items (id, name) VALUES (1, 'One'), (2, 'Two'), (3, 'Three'), (4, 'Four');")

	prepareStmt := func(id int) int32 {
		var prepParams bytes.Buffer
		prepParams.Write(buildIntParam("@handle", true, 0))
		prepParams.Write(buildNVarCharParam("@params", false, "@id INT"))
		prepParams.Write(buildNVarCharParam("@stmt", false, fmt.Sprintf("SELECT name FROM items WHERE id = @id; /* tag %d */", id)))

		respPrep := sendRPC(t, conn, 11, 0, prepParams.Bytes())
		if !bytes.Contains(respPrep, []byte{TokenReturnValue}) {
			t.Fatalf("sp_prepare: expected TokenReturnValue")
		}
		rvIdx := bytes.Index(respPrep, []byte{TokenReturnValue})
		valOffset := rvIdx + 1 + 2 + 1 + 14 + 1 + 4 + 2 + 1
		return int32(respPrep[valOffset])
	}

	executeStmt := func(handle int32, idVal int) (bool, string) {
		var execParams bytes.Buffer
		execParams.Write(buildIntParam("@handle", false, handle))
		execParams.Write(buildIntParam("@id", false, int32(idVal)))

		respExec := sendRPC(t, conn, 12, 0, execParams.Bytes())
		if bytes.Contains(respExec, []byte{TokenError}) {
			return false, "error"
		}
		if bytes.Contains(respExec, []byte{TokenRow}) {
			return true, "success"
		}
		return false, "unknown"
	}

	// 1. Prepare 3 statements (filling capacity of 3)
	h1 := prepareStmt(1)
	h2 := prepareStmt(2)
	h3 := prepareStmt(3)

	// 2. Execute h1 to promote it to MRU. Order: h2 (oldest), h3, h1 (newest)
	ok, _ := executeStmt(h1, 1)
	if !ok {
		t.Fatalf("execute h1 failed")
	}

	// 3. Prepare 4th statement: should trigger LRU eviction of h2!
	h4 := prepareStmt(4)

	// 4. Verify h2 was evicted
	okH2, _ := executeStmt(h2, 2)
	if okH2 {
		t.Fatalf("expected h2 to be evicted and fail execution with Error 8179")
	}

	// 5. Verify h1, h3, h4 are all still valid and execute successfully
	for _, h := range []struct {
		handle int32
		id     int
	}{
		{h1, 1},
		{h3, 3},
		{h4, 4},
	} {
		ok, _ := executeStmt(h.handle, h.id)
		if !ok {
			t.Fatalf("expected handle %d to succeed", h.handle)
		}
	}
}
