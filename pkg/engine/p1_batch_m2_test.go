package engine

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func newM2Coordinator(t *testing.T) *Coordinator {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	setup := `
CREATE TABLE m2_t (id INT PRIMARY KEY);
INSERT INTO m2_t (id) VALUES (1);
INSERT INTO m2_t (id) VALUES (2);
INSERT INTO m2_t (id) VALUES (3);
CREATE TABLE m2_u (k INT PRIMARY KEY, v INT);
INSERT INTO m2_u (k, v) VALUES (1, 10);
INSERT INTO m2_u (k, v) VALUES (2, 20);
INSERT INTO m2_u (k, v) VALUES (3, 30);
`
	if _, err := coord.ExecuteSessionSQL("sess-m2", setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	return coord
}

// Defect 14-1: APPLY accumulated rows must be capped.
func TestP1_BatchM2_ApplyRowLimit(t *testing.T) {
	coord := newM2Coordinator(t)

	old := maxApplyRows
	maxApplyRows = 5
	defer func() { maxApplyRows = old }()

	_, err := coord.ExecuteSessionSQL("sess-m2", "SELECT a.id, b.v FROM m2_t a CROSS APPLY (SELECT v FROM m2_u) b;")
	if err == nil {
		t.Fatalf("expected APPLY row limit error (3x3=9 rows > 5), got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "apply") || !strings.Contains(strings.ToLower(err.Error()), "limit") {
		t.Fatalf("expected error mentioning APPLY limit, got: %v", err)
	}
}

// Scope-047: a failing derived table must not leave materialized temp tables behind.
func TestP1_BatchM2_DerivedTableErrorCleansTempTables(t *testing.T) {
	coord := newM2Coordinator(t)

	before, err := coord.engine.ListTables()
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQL("sess-m2",
		"SELECT x.id FROM (SELECT id FROM m2_t) AS x JOIN (SELECT k FROM m2_missing) AS y ON x.id = y.k;")
	if err == nil {
		t.Fatalf("expected derived table error for missing table")
	}

	after, err := coord.engine.ListTables()
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}
	if len(after) != len(before) {
		var names []string
		for _, tb := range after {
			names = append(names, tb.Name)
		}
		t.Fatalf("expected no leaked derived temp tables: before=%d after=%d (%v)", len(before), len(after), names)
	}
}
