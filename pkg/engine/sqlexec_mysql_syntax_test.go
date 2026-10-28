package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestMySQL_SyntaxErgonomics(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Create table
	_, err := coord.ExecuteSQL("CREATE TABLE `users` (`id` INT PRIMARY KEY, `name` VARCHAR(50), `score` FLOAT DEFAULT 10.5);")
	if err != nil {
		t.Fatalf("CREATE TABLE with backticks failed: %v", err)
	}

	// 2. Insert with backticks
	_, err = coord.ExecuteSQL("INSERT INTO `users` (`id`, `name`, `score`) VALUES (1, 'Alice', 95.0), (2, 'Bob', 88.5);")
	if err != nil {
		t.Fatalf("INSERT with backticks failed: %v", err)
	}

	// 3. SELECT with backticks
	selRes, err := coord.ExecuteSQL("SELECT `u`.`id`, `u`.`name` FROM `users` AS `u` WHERE `u`.`id` = 1;")
	if err != nil {
		t.Fatalf("SELECT with backticks failed: %v", err)
	}
	if len(selRes.Rows) != 1 || selRes.Rows[0]["name"] != "Alice" {
		t.Fatalf("Unexpected select result: %+v", selRes.Rows)
	}

	// 4. DESC and DESCRIBE
	for _, q := range []string{"DESC `users`;", "DESCRIBE users;", "EXPLAIN users;"} {
		descRes, err := coord.ExecuteSQL(q)
		if err != nil {
			t.Fatalf("%q failed: %v", q, err)
		}
		if len(descRes.Rows) != 3 {
			t.Fatalf("%q expected 3 columns described, got %d", q, len(descRes.Rows))
		}
		if descRes.Rows[0]["Field"] != "id" || descRes.Rows[0]["Key"] != "PRI" || descRes.Rows[0]["Null"] != "NO" {
			t.Fatalf("%q unexpected id row: %+v", q, descRes.Rows[0])
		}
		if descRes.Rows[1]["Field"] != "name" || descRes.Rows[1]["Type"] != "VARCHAR" {
			t.Fatalf("%q unexpected name row: %+v", q, descRes.Rows[1])
		}
		if descRes.Rows[2]["Field"] != "score" || descRes.Rows[2]["Default"] != "10.5" {
			t.Fatalf("%q unexpected score row: %+v", q, descRes.Rows[2])
		}
	}

	// 5. SHOW TABLES and SHOW FULL TABLES
	for _, q := range []string{"SHOW TABLES;", "SHOW FULL TABLES;"} {
		tblRes, err := coord.ExecuteSQL(q)
		if err != nil {
			t.Fatalf("%q failed: %v", q, err)
		}
		if len(tblRes.Rows) != 1 {
			t.Fatalf("%q expected 1 table, got %d", q, len(tblRes.Rows))
		}
		colName := tblRes.Columns[0]
		if tblRes.Rows[0][colName] != "users" {
			t.Fatalf("%q expected 'users', got %v", q, tblRes.Rows[0][colName])
		}
	}

	// 6. SHOW DATABASES / SHOW SCHEMAS
	for _, q := range []string{"SHOW DATABASES;", "SHOW SCHEMAS;"} {
		dbRes, err := coord.ExecuteSQL(q)
		if err != nil {
			t.Fatalf("%q failed: %v", q, err)
		}
		if len(dbRes.Rows) < 1 {
			t.Fatalf("%q expected databases list, got empty", q)
		}
		found := false
		for _, r := range dbRes.Rows {
			if r["Database"] == "lionsql" || r["Database"] == "master" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%q did not contain standard databases: %+v", q, dbRes.Rows)
		}
	}

	// 7. DESC on non-existing table should return error
	if _, err := coord.ExecuteSQL("DESC non_existing;"); err == nil {
		t.Fatalf("Expected error for DESC non_existing, got nil")
	}
}
