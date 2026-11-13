package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

// TestCorpus_AdventureWorksLT verifies that LionSQL successfully executes the Microsoft AdventureWorksLT
// official lightweight OLTP schema DDL, sample data seeds, and real-world business queries.
func TestCorpus_AdventureWorksLT(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer func() {
		coord.Close()
		eng.Close()
	}()

	// 1. Read and execute Schema DDL
	schemaPath := filepath.Join("..", "..", "test", "corpora", "adventureworks", "adventureworks_lt_schema.sql")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("failed to read schema file: %v", err)
	}

	schemaStmts := splitSQLStatements(string(schemaBytes))
	t.Logf("Executing %d DDL statements from AdventureWorksLT schema...", len(schemaStmts))
	for i, stmt := range schemaStmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		res, err := coord.ExecuteSQL(stmt)
		if err != nil {
			t.Fatalf("DDL stmt [%d] failed: %v\nSQL: %s", i, err, stmt)
		}
		_ = res
	}

	// 2. Read and execute Seed Data Inserts
	dataPath := filepath.Join("..", "..", "test", "corpora", "adventureworks", "adventureworks_lt_data.sql")
	dataBytes, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("failed to read data seed file: %v", err)
	}

	dataStmts := splitSQLStatements(string(dataBytes))
	t.Logf("Executing %d INSERT statements from AdventureWorksLT data seed...", len(dataStmts))
	for i, stmt := range dataStmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		res, err := coord.ExecuteSQL(stmt)
		if err != nil {
			t.Fatalf("Data seed stmt [%d] failed: %v\nSQL: %s", i, err, stmt)
		}
		_ = res
	}

	// 3. Verify Table Counts
	verifyCount(t, coord, "SELECT COUNT(*) FROM [SalesLT].[Customer];", 5)
	verifyCount(t, coord, "SELECT COUNT(*) FROM [SalesLT].[Address];", 5)
	verifyCount(t, coord, "SELECT COUNT(*) FROM [SalesLT].[ProductCategory];", 9)
	verifyCount(t, coord, "SELECT COUNT(*) FROM [SalesLT].[Product];", 6)
	verifyCount(t, coord, "SELECT COUNT(*) FROM [SalesLT].[SalesOrderHeader];", 3)
	verifyCount(t, coord, "SELECT COUNT(*) FROM [SalesLT].[SalesOrderDetail];", 5)

	// 4. Read and execute Business Queries
	queryPath := filepath.Join("..", "..", "test", "corpora", "adventureworks", "adventureworks_lt_queries.sql")
	queryBytes, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("failed to read queries file: %v", err)
	}

	queryStmts := splitSQLStatements(string(queryBytes))
	t.Logf("Executing %d real-world business queries on AdventureWorksLT...", len(queryStmts))
	for i, q := range queryStmts {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		res, err := coord.ExecuteSQL(q)
		if err != nil {
			t.Fatalf("Business query [%d] failed: %v\nSQL: %s", i, err, q)
		}
		t.Logf("Query [%d] passed, returned %d rows", i+1, len(res.Rows))
		if len(res.Rows) == 0 {
			t.Errorf("expected Query [%d] to return >0 rows, got 0", i+1)
		}
	}
}

func verifyCount(t *testing.T, coord *engine.Coordinator, sql string, expected int64) {
	t.Helper()
	res, err := coord.ExecuteSQL(sql)
	if err != nil {
		t.Fatalf("count query failed: %v\nSQL: %s", err, sql)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected 1 row result from %s", sql)
	}
	var countVal int64
	for _, v := range res.Rows[0] {
		switch num := v.(type) {
		case int64:
			countVal = num
		case int:
			countVal = int64(num)
		case float64:
			countVal = int64(num)
		}
		break
	}
	if countVal != expected {
		t.Errorf("count mismatch for %s: expected %d, got %d", sql, expected, countVal)
	}
}

func splitSQLStatements(content string) []string {
	var stmts []string
	lines := strings.Split(content, "\n")
	var cur strings.Builder

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		if trimmed == "GO" || trimmed == "go" {
			s := strings.TrimSpace(cur.String())
			if s != "" {
				stmts = append(stmts, s)
			}
			cur.Reset()
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			s := strings.TrimSpace(cur.String())
			if s != "" {
				stmts = append(stmts, s)
			}
			cur.Reset()
		}
	}
	s := strings.TrimSpace(cur.String())
	if s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}
