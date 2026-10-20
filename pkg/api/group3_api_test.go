package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestGroup3_HTTPTablePK_RejectBadType tests BUG-HTTP-TABLE-PK-1:
// Creating a table via HTTP API with an unsupported column type returns 400 Bad Request.
func TestGroup3_HTTPTablePK_RejectBadType(t *testing.T) {
	server := setupMemServer(t)

	reqBody := map[string]any{
		"name": "invalid_table",
		"columns": []map[string]any{
			{"name": "id", "type": "INT", "primary_key": true},
			{"name": "weird_col", "type": "UNSUPPORTED_TYPE_XYZ"},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/tables", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for unsupported type, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGroup3_HTTPTablePK_SyntheticRowID tests BUG-DDL-PK-1 in HTTP API:
// Creating a table without explicit PK does NOT promote columns[0] to PK, but ensures _row_id.
func TestGroup3_HTTPTablePK_SyntheticRowID(t *testing.T) {
	server := setupMemServer(t)

	reqBody := map[string]any{
		"name": "notifications",
		"columns": []map[string]any{
			{"name": "content", "type": "VARCHAR"},
			{"name": "is_read", "type": "BOOL"},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/tables", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("POST /api/tables failed with status %d: %s", w.Code, w.Body.String())
	}

	tbl, err := server.Coordinator().StorageEngine().GetTable("notifications")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}

	contentCol, _, _ := tbl.FindColumn("content")
	if contentCol.PrimaryKey {
		t.Fatalf("expected content column NOT to be primary key")
	}

	pkCol, err := tbl.PrimaryKeyColumn()
	if err != nil || pkCol == nil || pkCol.Name != "_row_id" {
		t.Fatalf("expected _row_id to be primary key, got: %v, err: %v", pkCol, err)
	}
}

// TestGroup3_API_PK_Gen_ConcurrentUnique tests BUG-API-PK-GEN-1:
// High-concurrency record creation without explicit ID must produce unique PKs with 0 collisions.
func TestGroup3_API_PK_Gen_ConcurrentUnique(t *testing.T) {
	server := setupMemServer(t)

	execStatements(t, server, "CREATE TABLE event_stream (id INT PRIMARY KEY, message VARCHAR(100));")

	const numWorkers = 20
	const itemsPerWorker = 10
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers*itemsPerWorker)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < itemsPerWorker; i++ {
				payload := map[string]any{
					"message": fmt.Sprintf("event-w%d-%d", workerID, i),
				}
				pBytes, _ := json.Marshal(payload)
				req := httptest.NewRequest("POST", "/api/tables/event_stream/records", bytes.NewReader(pBytes))
				rec := httptest.NewRecorder()
				server.ServeHTTP(rec, req)

				if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
					errCh <- fmt.Errorf("worker %d failed: status %d body %s", workerID, rec.Code, rec.Body.String())
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	res, err := server.ExecuteSQL("SELECT COUNT(*) AS cnt FROM event_stream;")
	if err != nil {
		t.Fatalf("SELECT COUNT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	cntVal := fmt.Sprintf("%v", res.Rows[0]["cnt"])
	if cntVal != fmt.Sprintf("%d", numWorkers*itemsPerWorker) {
		t.Fatalf("expected %d total records, got: %s", numWorkers*itemsPerWorker, cntVal)
	}
}
