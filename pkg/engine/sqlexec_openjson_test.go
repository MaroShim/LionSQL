package engine

import (
	"testing"
)

func TestOpenJson_DefaultSchemaObject(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	jsonStr := `{"name":"Alice","age":30,"isAdmin":true,"tags":["a","b"]}`
	query := "SELECT [key], [value], [type] FROM OPENJSON('" + jsonStr + "') ORDER BY [key] ASC;"

	res, err := coord.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("OPENJSON query failed: %v", err)
	}

	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res.Rows))
	}

	// age (number -> type 2)
	rowAge := res.Rows[0]
	if rowAge["key"] != "age" || rowAge["value"] != "30" || rowAge["type"] != 2 {
		t.Errorf("unexpected age row: %v", rowAge)
	}

	// isAdmin (bool -> type 3)
	rowAdmin := res.Rows[1]
	if rowAdmin["key"] != "isAdmin" || rowAdmin["value"] != "true" || rowAdmin["type"] != 3 {
		t.Errorf("unexpected isAdmin row: %v", rowAdmin)
	}

	// name (string -> type 1)
	rowName := res.Rows[2]
	if rowName["key"] != "name" || rowName["value"] != "Alice" || rowName["type"] != 1 {
		t.Errorf("unexpected name row: %v", rowName)
	}

	// tags (array -> type 4)
	rowTags := res.Rows[3]
	if rowTags["key"] != "tags" || rowTags["value"] != `["a","b"]` || rowTags["type"] != 4 {
		t.Errorf("unexpected tags row: %v", rowTags)
	}
}

func TestOpenJson_DefaultSchemaArray(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	query := "SELECT [key], [value], [type] FROM OPENJSON('[10, 20, 30]') ORDER BY CAST([key] AS INT) ASC;"
	res, err := coord.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("OPENJSON array query failed: %v", err)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["key"] != "0" || res.Rows[0]["value"] != "10" || res.Rows[0]["type"] != 2 {
		t.Errorf("unexpected row 0: %v", res.Rows[0])
	}
	if res.Rows[1]["key"] != "1" || res.Rows[1]["value"] != "20" || res.Rows[1]["type"] != 2 {
		t.Errorf("unexpected row 1: %v", res.Rows[1])
	}
	if res.Rows[2]["key"] != "2" || res.Rows[2]["value"] != "30" || res.Rows[2]["type"] != 2 {
		t.Errorf("unexpected row 2: %v", res.Rows[2])
	}
}

func TestOpenJson_ExplicitSchemaWithClause(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	query := `
SELECT id, name, age
FROM OPENJSON('[{"id": 1, "name": "Alice", "age": 25}, {"id": 2, "name": "Bob", "age": 30}]')
WITH (
    id INT '$.id',
    name VARCHAR(50) '$.name',
    age INT '$.age'
)
ORDER BY id ASC;
`
	res, err := coord.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("OPENJSON WITH query failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}

	if res.Rows[0]["id"] != int64(1) || res.Rows[0]["name"] != "Alice" || res.Rows[0]["age"] != int64(25) {
		t.Errorf("unexpected row 0: %v", res.Rows[0])
	}
	if res.Rows[1]["id"] != int64(2) || res.Rows[1]["name"] != "Bob" || res.Rows[1]["age"] != int64(30) {
		t.Errorf("unexpected row 1: %v", res.Rows[1])
	}
}

func TestOpenJson_WithClauseAsJson(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	query := `
SELECT id, profile
FROM OPENJSON('[{"id": 10, "profile": {"level": 5, "title": "Admin"}}]')
WITH (
    id INT,
    profile NVARCHAR(MAX) AS JSON
);
`
	res, err := coord.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("OPENJSON AS JSON query failed: %v", err)
	}

	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0]["id"] != int64(10) {
		t.Errorf("expected id=10, got %v", res.Rows[0]["id"])
	}
	profileStr, ok := res.Rows[0]["profile"].(string)
	if !ok || profileStr != `{"level":5,"title":"Admin"}` {
		t.Errorf("expected profile JSON string, got %v (%T)", res.Rows[0]["profile"], res.Rows[0]["profile"])
	}
}

func TestOpenJson_SessionVariableAndPath(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessID := "sess_openjson_var"

	_, err := coord.ExecuteSessionSQL(sessID, `SET @j = '{"store":{"book":[{"title":"Go","price":40},{"title":"Rust","price":50}]}}';`)
	if err != nil {
		t.Fatalf("SET @j failed: %v", err)
	}

	query := `
SELECT title, price
FROM OPENJSON(@j, '$.store.book')
WITH (
    title VARCHAR(50),
    price INT
)
ORDER BY price DESC;
`
	res, err := coord.ExecuteSessionSQL(sessID, query)
	if err != nil {
		t.Fatalf("OPENJSON with session var and path failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 books, got %d", len(res.Rows))
	}
	if res.Rows[0]["title"] != "Rust" || res.Rows[0]["price"] != int64(50) {
		t.Errorf("unexpected first book: %v", res.Rows[0])
	}
	if res.Rows[1]["title"] != "Go" || res.Rows[1]["price"] != int64(40) {
		t.Errorf("unexpected second book: %v", res.Rows[1])
	}
}
