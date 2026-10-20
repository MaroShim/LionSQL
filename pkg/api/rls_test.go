package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestRLS_TokenSubstitutionAndEvaluation(t *testing.T) {
	mgr := NewRLSManager()

	authAlice := &AuthContext{
		ID:       "usr_alice",
		Email:    "alice@example.com",
		Role:     "user",
		TenantID: "tenant_42",
	}

	// 1. SubstituteAuthTokens
	rawRule := "user_id = @auth.id && tenant_id = @auth.tenant_id"
	resolved := SubstituteAuthTokens(rawRule, authAlice)
	expected := "user_id = 'usr_alice' && tenant_id = 'tenant_42'"
	if resolved != expected {
		t.Fatalf("expected resolved rule %q, got %q", expected, resolved)
	}

	// 2. Evaluate Rule: matching row
	rowMatch := map[string]any{
		"user_id":   "usr_alice",
		"tenant_id": "tenant_42",
		"title":     "Quarterly Report",
	}
	allowed, err := mgr.EvaluateRule(rawRule, rowMatch, authAlice)
	if err != nil || !allowed {
		t.Fatalf("expected row to match rule, got allowed=%v, err=%v", allowed, err)
	}

	// 3. Evaluate Rule: non-matching row (different user)
	rowBob := map[string]any{
		"user_id":   "usr_bob",
		"tenant_id": "tenant_42",
		"title":     "Bob Notes",
	}
	allowedBob, errBob := mgr.EvaluateRule(rawRule, rowBob, authAlice)
	if allowedBob || errBob == nil {
		t.Fatalf("expected row to fail rule, got allowed=%v, err=%v", allowedBob, errBob)
	}
}

func TestRLS_EndToEndTableSecurity(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)

	server := NewServer(cat, eng)
	defer eng.Close()
	defer server.Webhooks().Close()

	// 1. Create table "documents"
	ddl := `
	CREATE TABLE documents (
		id INT PRIMARY KEY,
		title VARCHAR(100) NOT NULL,
		owner_id VARCHAR(50) NOT NULL,
		status VARCHAR(20) NOT NULL
	);
	`
	if _, err := server.ExecuteSQL(ddl); err != nil {
		t.Fatalf("failed to create documents table: %v", err)
	}

	// 2. Seed rows
	_, _ = server.ExecuteSQL("INSERT INTO documents (id, title, owner_id, status) VALUES (1, 'Alice Diary', 'alice', 'draft');")
	_, _ = server.ExecuteSQL("INSERT INTO documents (id, title, owner_id, status) VALUES (2, 'Bob Secret', 'bob', 'draft');")
	_, _ = server.ExecuteSQL("INSERT INTO documents (id, title, owner_id, status) VALUES (3, 'Bob Press Release', 'bob', 'published');")

	// 3. Configure RLS rules on "documents"
	listRule := "status = 'published' || owner_id = @auth.id"
	viewRule := "status = 'published' || owner_id = @auth.id"
	createRule := "owner_id = @auth.id"
	updateRule := "owner_id = @auth.id"
	deleteRule := "owner_id = @auth.id || @auth.role = 'admin'"

	err := server.RLS().SetRules(&TableRules{
		TableName:  "documents",
		ListRule:   &listRule,
		ViewRule:   &viewRule,
		CreateRule: &createRule,
		UpdateRule: &updateRule,
		DeleteRule: &deleteRule,
	})
	if err != nil {
		t.Fatalf("failed to set RLS rules: %v", err)
	}

	// 4. Test List as Alice (should see Alice Diary and Bob Press Release, but NOT Bob Secret)
	reqList := httptest.NewRequest(http.MethodGet, "/api/records/documents", nil)
	reqList.Header.Set("X-User-ID", "alice")
	recList := httptest.NewRecorder()
	server.ServeHTTP(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("GET /api/records/documents failed with %d: %s", recList.Code, recList.Body.String())
	}
	var listResp struct {
		Items      []map[string]any `json:"items"`
		TotalItems int              `json:"totalItems"`
	}
	_ = json.Unmarshal(recList.Body.Bytes(), &listResp)

	if listResp.TotalItems != 2 || len(listResp.Items) != 2 {
		t.Fatalf("expected Alice to see exactly 2 documents, got %d (len %d): %+v", listResp.TotalItems, len(listResp.Items), listResp.Items)
	}
	for _, doc := range listResp.Items {
		if doc["title"] == "Bob Secret" {
			t.Fatalf("SECURITY VIOLATION: Alice was able to view Bob Secret draft!")
		}
	}

	// 5. Test Single Record View:
	// - Alice viewing Bob Secret (draft) -> 403 Forbidden
	reqViewSecret := httptest.NewRequest(http.MethodGet, "/api/records/documents/2", nil)
	reqViewSecret.Header.Set("X-User-ID", "alice")
	recViewSecret := httptest.NewRecorder()
	server.ServeHTTP(recViewSecret, reqViewSecret)

	if recViewSecret.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when Alice views Bob Secret, got %d", recViewSecret.Code)
	}

	// - Alice viewing Bob Press Release (published) -> 200 OK
	reqViewPub := httptest.NewRequest(http.MethodGet, "/api/records/documents/3", nil)
	reqViewPub.Header.Set("X-User-ID", "alice")
	recViewPub := httptest.NewRecorder()
	server.ServeHTTP(recViewPub, reqViewPub)

	if recViewPub.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when Alice views Bob published release, got %d", recViewPub.Code)
	}

	// 6. Test Record Creation:
	// - Alice attempting to create document with owner_id="bob" -> 403 Forbidden
	spoofDoc := []byte(`{"id": 4, "title": "Spoofed Doc", "owner_id": "bob", "status": "draft"}`)
	reqSpoof := httptest.NewRequest(http.MethodPost, "/api/records/documents", bytes.NewReader(spoofDoc))
	reqSpoof.Header.Set("Content-Type", "application/json")
	reqSpoof.Header.Set("X-User-ID", "alice")
	recSpoof := httptest.NewRecorder()
	server.ServeHTTP(recSpoof, reqSpoof)

	if recSpoof.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when Alice spoofs owner_id='bob', got %d", recSpoof.Code)
	}

	// - Alice creating document with owner_id="alice" -> 201 Created
	validDoc := []byte(`{"id": 4, "title": "Alice New Memo", "owner_id": "alice", "status": "draft"}`)
	reqValid := httptest.NewRequest(http.MethodPost, "/api/records/documents", bytes.NewReader(validDoc))
	reqValid.Header.Set("Content-Type", "application/json")
	reqValid.Header.Set("X-User-ID", "alice")
	recValid := httptest.NewRecorder()
	server.ServeHTTP(recValid, reqValid)

	if recValid.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for valid doc creation, got %d: %s", recValid.Code, recValid.Body.String())
	}

	// 7. Test Record Update:
	// - Alice attempting to update Bob's document (id 2) -> 403 Forbidden
	updatePayload := []byte(`{"title": "Hacked Title"}`)
	reqUpdate := httptest.NewRequest(http.MethodPatch, "/api/records/documents/2", bytes.NewReader(updatePayload))
	reqUpdate.Header.Set("Content-Type", "application/json")
	reqUpdate.Header.Set("X-User-ID", "alice")
	recUpdate := httptest.NewRecorder()
	server.ServeHTTP(recUpdate, reqUpdate)

	if recUpdate.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when Alice updates Bob document, got %d", recUpdate.Code)
	}

	// 8. Test Record Delete:
	// - Alice attempting to delete Bob's document (id 2) -> 403 Forbidden
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/records/documents/2", nil)
	reqDel.Header.Set("X-User-ID", "alice")
	recDel := httptest.NewRecorder()
	server.ServeHTTP(recDel, reqDel)

	if recDel.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when Alice deletes Bob document, got %d", recDel.Code)
	}

	// - Admin deleting Bob's document -> 200 OK (rule allows owner_id = @auth.id || @auth.role = 'admin')
	reqDelAdmin := httptest.NewRequest(http.MethodDelete, "/api/records/documents/2", nil)
	reqDelAdmin.Header.Set("X-User-Role", "admin")
	recDelAdmin := httptest.NewRecorder()
	server.ServeHTTP(recDelAdmin, reqDelAdmin)

	if recDelAdmin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when Admin deletes document, got %d: %s", recDelAdmin.Code, recDelAdmin.Body.String())
	}
}

func TestRLS_RulesManagementAPI(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	cat := catalog.New(eng)

	server := NewServer(cat, eng)
	defer eng.Close()
	defer server.Webhooks().Close()

	_, _ = server.ExecuteSQL("CREATE TABLE posts (id INT PRIMARY KEY, content VARCHAR(200));")

	// 1. GET /api/tables/posts/rules (default empty)
	reqGet := httptest.NewRequest(http.MethodGet, "/api/tables/posts/rules", nil)
	recGet := httptest.NewRecorder()
	server.ServeHTTP(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("GET /api/tables/posts/rules failed: %d", recGet.Code)
	}

	// 2. PUT /api/tables/posts/rules
	listRule := "status = 'public'"
	rulesPayload, _ := json.Marshal(map[string]any{
		"list_rule":   listRule,
		"create_rule": "@auth.id != ''",
	})
	reqPut := httptest.NewRequest(http.MethodPut, "/api/tables/posts/rules", bytes.NewReader(rulesPayload))
	reqPut.Header.Set("Content-Type", "application/json")
	recPut := httptest.NewRecorder()
	server.ServeHTTP(recPut, reqPut)

	if recPut.Code != http.StatusOK {
		t.Fatalf("PUT /api/tables/posts/rules failed: %d", recPut.Code)
	}

	rules, ok := server.RLS().GetRules("posts")
	if !ok || rules.ListRule == nil || *rules.ListRule != listRule {
		t.Fatalf("RLS rules not updated: %+v", rules)
	}

	// 3. DELETE /api/tables/posts/rules
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/tables/posts/rules", nil)
	recDel := httptest.NewRecorder()
	server.ServeHTTP(recDel, reqDel)

	if recDel.Code != http.StatusOK {
		t.Fatalf("DELETE /api/tables/posts/rules failed: %d", recDel.Code)
	}

	if _, ok := server.RLS().GetRules("posts"); ok {
		t.Fatalf("expected RLS rules to be deleted")
	}
}
