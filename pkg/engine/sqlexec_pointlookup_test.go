package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestParsePointLookupCandidate(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantTbl   string
		wantCol   string
		wantParam string
		wantLit   string
		wantProjs int
		isNil     bool
	}{
		{
			name:      "PG Prepared parameter $1",
			query:     "SELECT * FROM users WHERE id = $1;",
			wantTbl:   "users",
			wantCol:   "id",
			wantParam: "$1",
			wantProjs: 1,
		},
		{
			name:      "MSSQL/ODBC Parameter ?",
			query:     "SELECT id, name FROM accounts WHERE id = ?",
			wantTbl:   "accounts",
			wantCol:   "id",
			wantParam: "?",
			wantProjs: 2,
		},
		{
			name:      "Integer literal with table qualifier",
			query:     "SELECT u.id, u.name AS full_name FROM users u WHERE u.id = 10023",
			wantTbl:   "users",
			wantCol:   "id",
			wantLit:   "10023",
			wantProjs: 2,
		},
		{
			name:      "String literal with quotes",
			query:     "SELECT * FROM customers WHERE code = 'CUST-001'",
			wantTbl:   "customers",
			wantCol:   "code",
			wantLit:   "'CUST-001'",
			wantProjs: 1,
		},
		{
			name:      "Reversed equality (lit = col)",
			query:     "SELECT * FROM items WHERE 500 = item_id",
			wantTbl:   "items",
			wantCol:   "item_id",
			wantLit:   "500",
			wantProjs: 1,
		},
		{
			name:  "Disallowed JOIN",
			query: "SELECT u.id FROM users u JOIN orders o ON u.id = o.user_id WHERE u.id = 1",
			isNil: true,
		},
		{
			name:  "Disallowed ORDER BY",
			query: "SELECT * FROM users WHERE id = 1 ORDER BY id",
			isNil: true,
		},
		{
			name:  "Disallowed complex condition with AND",
			query: "SELECT * FROM users WHERE id = 1 AND active = 1",
			isNil: true,
		},
		{
			name:  "Disallowed aggregate projection",
			query: "SELECT COUNT(*) FROM users WHERE id = 1",
			isNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cand := ParsePointLookupCandidate(tt.query)
			if tt.isNil {
				if cand != nil {
					t.Fatalf("expected nil candidate for query %q, got %+v", tt.query, cand)
				}
				return
			}
			if cand == nil {
				t.Fatalf("expected candidate for query %q, got nil", tt.query)
			}
			if cand.TableName != tt.wantTbl {
				t.Errorf("TableName: got %q, want %q", cand.TableName, tt.wantTbl)
			}
			if cand.PKColumn != tt.wantCol {
				t.Errorf("PKColumn: got %q, want %q", cand.PKColumn, tt.wantCol)
			}
			if cand.ParamMarker != tt.wantParam {
				t.Errorf("ParamMarker: got %q, want %q", cand.ParamMarker, tt.wantParam)
			}
			if cand.LiteralValue != tt.wantLit {
				t.Errorf("LiteralValue: got %q, want %q", cand.LiteralValue, tt.wantLit)
			}
			if len(cand.Projections) != tt.wantProjs {
				t.Errorf("Projections count: got %d, want %d", len(cand.Projections), tt.wantProjs)
			}
		})
	}
}

func TestExecutePointLookup_DirectFastPath(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	sessID := "test-sess-pointlookup"

	// 1. Setup schema & insert test row
	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50), balance INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO users VALUES (101, 'Alice', 5000);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 2. Direct PK point lookup hit
	res, err := coord.ExecutePointLookup(sessID, "users", storage.IntValue(101), []string{"*"})
	if err != nil {
		t.Fatalf("ExecutePointLookup hit failed: %v", err)
	}
	if !res.Success || len(res.Rows) != 1 || res.RowsAffected != 1 {
		t.Fatalf("expected 1 row, got %+v", res)
	}
	if res.Rows[0]["name"] != "Alice" || res.Rows[0]["balance"] != int64(5000) {
		t.Errorf("unexpected row data: %+v", res.Rows[0])
	}

	// 3. Selective projection & aliases
	resProj, err := coord.ExecutePointLookup(sessID, "users", storage.IntValue(101), []string{"name AS user_name", "balance"})
	if err != nil {
		t.Fatalf("ExecutePointLookup proj failed: %v", err)
	}
	if len(resProj.Columns) != 2 || resProj.Columns[0] != "user_name" {
		t.Errorf("expected projected columns [user_name, balance], got %v", resProj.Columns)
	}
	if resProj.Rows[0]["user_name"] != "Alice" {
		t.Errorf("expected user_name 'Alice', got %+v", resProj.Rows[0])
	}

	// 4. Point lookup miss (ErrKeyNotFound -> empty result)
	resMiss, err := coord.ExecutePointLookup(sessID, "users", storage.IntValue(9999), []string{"*"})
	if err != nil {
		t.Fatalf("ExecutePointLookup miss returned error: %v", err)
	}
	if len(resMiss.Rows) != 0 || resMiss.RowsAffected != 0 {
		t.Errorf("expected 0 rows on miss, got %d rows", len(resMiss.Rows))
	}
}
