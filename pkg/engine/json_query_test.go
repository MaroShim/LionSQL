package engine_test

import (
	"fmt"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func TestJSON_ScalarQueries(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. ISJSON
	res, err := coord.ExecuteSQL("SELECT ISJSON('{\"name\": \"Alice\"}') AS valid, ISJSON('not a json') AS invalid;")
	if err != nil || !res.Success {
		t.Fatalf("ISJSON failed: %v", err)
	}
	if fmt.Sprintf("%v", res.Rows[0]["valid"]) != "1" {
		t.Fatalf("expected valid=1, got %v", res.Rows[0]["valid"])
	}
	if fmt.Sprintf("%v", res.Rows[0]["invalid"]) != "0" {
		t.Fatalf("expected invalid=0, got %v", res.Rows[0]["invalid"])
	}

	// 2. JSON_VALUE & JSON_QUERY
	res, err = coord.ExecuteSQL("SELECT JSON_VALUE('{\"user\": {\"name\": \"Alice\", \"age\": 25}}', '$.user.name') AS name, JSON_QUERY('{\"user\": {\"tags\": [\"go\", \"db\"]}}', '$.user.tags') AS tags;")
	if err != nil || !res.Success {
		t.Fatalf("JSON_VALUE/JSON_QUERY failed: %v", err)
	}
	if res.Rows[0]["name"] != "Alice" {
		t.Fatalf("expected name='Alice', got %v", res.Rows[0]["name"])
	}
	if res.Rows[0]["tags"] != `["go","db"]` && res.Rows[0]["tags"] != `["go", "db"]` {
		t.Fatalf("expected tags='[\"go\",\"db\"]', got %v", res.Rows[0]["tags"])
	}

	// 3. PostgreSQL operators: -> and ->>
	res, err = coord.ExecuteSQL("SELECT '{\"name\": \"Bob\", \"age\": 30}' -> 'name' AS obj, '{\"name\": \"Bob\", \"age\": 30}' ->> 'name' AS txt;")
	if err != nil || !res.Success {
		t.Fatalf("-> / ->> failed: %v", err)
	}
	if res.Rows[0]["obj"] != `"Bob"` {
		t.Fatalf("expected obj='\"Bob\"', got %v", res.Rows[0]["obj"])
	}
	if res.Rows[0]["txt"] != "Bob" {
		t.Fatalf("expected txt='Bob', got %v", res.Rows[0]["txt"])
	}

	// 4. JSON_MODIFY
	res, err = coord.ExecuteSQL("SELECT JSON_MODIFY('{\"name\": \"Bob\"}', '$.name', 'Charlie') AS modified;")
	if err != nil || !res.Success {
		t.Fatalf("JSON_MODIFY failed: %v", err)
	}
	if res.Rows[0]["modified"] != `{"name":"Charlie"}` && res.Rows[0]["modified"] != `{"name": "Charlie"}` {
		t.Fatalf("expected modified='{\"name\":\"Charlie\"}', got %v", res.Rows[0]["modified"])
	}
}

func TestJSON_TableQueries(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// Create table with JSON column
	_, err := coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, doc VARCHAR(500));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES 
		(1, '{"city": "Seoul", "age": 28, "tags": ["go", "db"], "nested": {"country": "KR"}}'),
		(2, '{"city": "Busan", "age": 35, "tags": ["rust"], "nested": {"country": "KR"}}'),
		(3, '{"city": "Tokyo", "age": 22, "tags": ["c++"], "nested": {"country": "JP"}}');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Projections with ->> and JSON_VALUE
	res, err := coord.ExecuteSQL("SELECT id, doc->>'city' AS city, JSON_VALUE(doc, '$.age') AS age FROM users ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT projection failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["city"] != "Seoul" || fmt.Sprintf("%v", res.Rows[0]["age"]) != "28" {
		t.Fatalf("row 0 mismatch: %+v", res.Rows[0])
	}
	if res.Rows[1]["city"] != "Busan" || fmt.Sprintf("%v", res.Rows[1]["age"]) != "35" {
		t.Fatalf("row 1 mismatch: %+v", res.Rows[1])
	}

	// 2. WHERE filter with ->>
	res, err = coord.ExecuteSQL("SELECT id, doc->>'city' AS city FROM users WHERE doc->>'city' = 'Seoul';")
	if err != nil || !res.Success {
		t.Fatalf("SELECT WHERE ->> failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["city"] != "Seoul" {
		t.Fatalf("expected 1 row with city='Seoul', got %+v", res.Rows)
	}

	// 3. WHERE filter with JSON_VALUE
	res, err = coord.ExecuteSQL("SELECT id, doc->>'city' AS city FROM users WHERE JSON_VALUE(doc, '$.city') = 'Busan';")
	if err != nil || !res.Success {
		t.Fatalf("SELECT WHERE JSON_VALUE failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["city"] != "Busan" {
		t.Fatalf("expected 1 row with city='Busan', got %+v", res.Rows)
	}

	// 4. Chained extraction: doc->'nested'->>'country'
	res, err = coord.ExecuteSQL("SELECT id, doc->'nested'->>'country' AS country FROM users WHERE doc->'nested'->>'country' = 'JP';")
	if err != nil || !res.Success {
		t.Fatalf("SELECT chained -> / ->> failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["country"] != "JP" {
		t.Fatalf("expected 1 row with country='JP', got %+v", res.Rows)
	}

	// 5. Array indexing extraction: doc->'tags'->>0
	res, err = coord.ExecuteSQL("SELECT id, doc->'tags'->>0 AS first_tag FROM users ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("SELECT array indexing failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["first_tag"] != "go" {
		t.Fatalf("expected first_tag='go', got %v", res.Rows[0]["first_tag"])
	}
	if res.Rows[1]["first_tag"] != "rust" {
		t.Fatalf("expected first_tag='rust', got %v", res.Rows[1]["first_tag"])
	}

	// 6. Volcano CBO multi-table JOIN with JSON extraction & filter
	_, err = coord.ExecuteSQL("CREATE TABLE orders (order_id INT PRIMARY KEY, user_id INT, details VARCHAR(500));")
	if err != nil {
		t.Fatalf("CREATE orders failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO orders VALUES 
		(101, 1, '{"item": "laptop", "price": 1200}'),
		(102, 2, '{"item": "phone", "price": 800}');`)
	if err != nil {
		t.Fatalf("INSERT orders failed: %v", err)
	}

	res, err = coord.ExecuteSQL(`SELECT u.id, u.doc->>'city' AS city, o.details->>'item' AS item 
		FROM users u 
		JOIN orders o ON u.id = o.user_id 
		WHERE u.doc->>'city' = 'Seoul' AND o.details->>'item' = 'laptop';`)
	if err != nil || !res.Success {
		t.Fatalf("JOIN with JSON failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 joined row, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["city"] != "Seoul" || res.Rows[0]["item"] != "laptop" {
		t.Fatalf("unexpected joined row: %+v", res.Rows[0])
	}
}

func TestJSON_ExpressionIndex(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, doc VARCHAR(500));")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES 
		(1, '{"city": "Seoul", "email": "alice@sqlion.org", "nested": {"country": "KR"}}'),
		(2, '{"city": "Busan", "email": "bob@sqlion.org", "nested": {"country": "KR"}}'),
		(3, '{"city": "Tokyo", "email": "charlie@sqlion.org", "nested": {"country": "JP"}}');`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Create expression index with PostgreSQL ->> syntax: ((doc->>'city'))
	_, err = coord.ExecuteSQL("CREATE INDEX idx_user_city ON users ((doc->>'city'));")
	if err != nil {
		t.Fatalf("CREATE INDEX ((doc->>'city')) failed: %v", err)
	}

	// Verify TableSchema expression index metadata
	tbl, err := eng.GetTable("users")
	if err != nil || tbl == nil {
		t.Fatalf("table users not found: %v", err)
	}
	idx, found := tbl.FindExpressionIndex("doc", "$.city")
	if !found || idx == nil {
		t.Fatalf("FindExpressionIndex(doc, $.city) not found: %+v", tbl.Indexes)
	}
	if !idx.IsExpression() || idx.ExprColumn != "doc" || idx.ExprPath != "$.city" {
		t.Fatalf("unexpected index schema: %+v", idx)
	}

	// 2. Create expression index with ANSI / MSSQL syntax: ((JSON_VALUE(doc, '$.nested.country')))
	res, err := coord.ExecuteSQL("CREATE INDEX idx_user_country ON users ((JSON_VALUE(doc, '$.nested.country')));")
	if err != nil || !res.Success {
		t.Fatalf("CREATE INDEX ((JSON_VALUE(...))) failed: %v, res: %+v", err, res)
	}
	tbl, err = eng.GetTable("users")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	idxCountry, found := tbl.FindExpressionIndex("doc", "$.nested.country")
	if !found || idxCountry == nil {
		t.Fatalf("FindExpressionIndex(doc, $.nested.country) not found: %+v", tbl.Indexes)
	}

	// 3. Test IndexScan direct lookup on the expression index
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	keySeoul := storage.StringValue("Seoul")
	cur, err := tx.IndexScan("users", "idx_user_city", &keySeoul, &keySeoul)
	if err != nil {
		t.Fatalf("IndexScan failed: %v", err)
	}
	defer cur.Close()
	var foundPks []string
	for cur.Next() {
		rec, rerr := cur.Record()
		if rerr != nil {
			t.Fatalf("cur.Record failed: %v", rerr)
		}
		foundPks = append(foundPks, rec["id"].String())
	}
	if len(foundPks) != 1 || foundPks[0] != "1" {
		t.Fatalf("expected PK ['1'], got %v", foundPks)
	}

	// 4. Test UNIQUE expression index
	_, err = coord.ExecuteSQL("CREATE UNIQUE INDEX idx_uniq_email ON users ((doc->>'email'));")
	if err != nil {
		t.Fatalf("CREATE UNIQUE INDEX failed: %v", err)
	}

	// Insert duplicate email -> MUST fail with unique violation
	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES (4, '{"city": "Incheon", "email": "alice@sqlion.org"}');`)
	if err == nil {
		t.Fatalf("expected unique violation on duplicate email alice@sqlion.org, but insert succeeded")
	}

	// Insert documents without email (NULL key) -> MUST succeed (multiple NULLs allowed in unique index)
	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES (5, '{"city": "Daegu"}');`)
	if err != nil {
		t.Fatalf("expected insert with null email to succeed, got: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO users VALUES (6, '{"city": "Daejeon"}');`)
	if err != nil {
		t.Fatalf("expected second insert with null email to succeed, got: %v", err)
	}

	// 5. Test UPDATE synchronization in expression index
	_, err = coord.ExecuteSQL(`UPDATE users SET doc = '{"city": "Suwon", "email": "alice_new@sqlion.org"}' WHERE id = 1;`)
	if err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}

	tx2, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx2.Rollback()

	// Old city "Seoul" should no longer match
	curOld, err := tx2.IndexScan("users", "idx_user_city", &keySeoul, &keySeoul)
	if err != nil {
		t.Fatalf("IndexScan old key failed: %v", err)
	}
	defer curOld.Close()
	if curOld.Next() {
		r, _ := curOld.Record()
		t.Fatalf("expected 0 rows for old key 'Seoul', got row %+v", r)
	}

	// New city "Suwon" should match
	keySuwon := storage.StringValue("Suwon")
	curNew, err := tx2.IndexScan("users", "idx_user_city", &keySuwon, &keySuwon)
	if err != nil {
		t.Fatalf("IndexScan new key failed: %v", err)
	}
	defer curNew.Close()
	if !curNew.Next() {
		t.Fatalf("expected 1 row for new key 'Suwon'")
	}
	rNew, _ := curNew.Record()
	if rNew["id"].String() != "1" {
		t.Fatalf("expected PK '1' for new key 'Suwon', got: %+v", rNew)
	}

	// 6. Test DELETE synchronization in expression index
	_, err = coord.ExecuteSQL(`DELETE FROM users WHERE id = 1;`)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	tx3, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx3.Rollback()
	curDel, err := tx3.IndexScan("users", "idx_user_city", &keySuwon, &keySuwon)
	if err != nil {
		t.Fatalf("IndexScan deleted key failed: %v", err)
	}
	defer curDel.Close()
	if curDel.Next() {
		r, _ := curDel.Record()
		t.Fatalf("expected 0 rows after delete for 'Suwon', got row %+v", r)
	}
}

