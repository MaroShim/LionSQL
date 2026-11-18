package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lionsql/pkg/api"
	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestAPI_SessionReset(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	srv := api.NewServer(cat, eng)
	defer srv.Close()

	sessID := "http-user-1"
	_, err := srv.ExecuteSessionSQL(sessID, "CREATE TABLE accounts (id INT PRIMARY KEY, bal INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = srv.ExecuteSessionSQL(sessID, "BEGIN TRAN; INSERT INTO accounts VALUES (1, 500);")
	if err != nil {
		t.Fatalf("INSERT in tx failed: %v", err)
	}

	// Call POST /api/session/reset
	body, _ := json.Marshal(map[string]any{
		"session_id": sessID,
		"reset_tx":   true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/session/reset", bytes.NewReader(body))
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	// Verify transaction rolled back: accounts table has 0 rows
	res, err := srv.ExecuteSessionSQL(sessID, "SELECT COUNT(*) AS c FROM accounts;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) == 0 || res.Rows[0]["c"] != int64(0) {
		t.Fatalf("expected 0 rows after rollback, got %v", res.Rows)
	}
}
