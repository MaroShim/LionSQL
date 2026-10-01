package api_test

import (
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestExtendedDataTypes_DDL_DML_EndToEnd(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	// 1. Create table with UUID, VARBINARY, DATE, and TIME columns
	ddl := `
CREATE TABLE device_events (
    id INT PRIMARY KEY,
    event_id UUID,
    payload VARBINARY,
    event_date DATE,
    event_time TIME
);`
	res, err := server.ExecuteSQL(ddl)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("CREATE TABLE unsucessful: %v", res.Messages)
	}

	// 2. Insert records using standard literals
	ins1 := `INSERT INTO device_events (id, event_id, payload, event_date, event_time)
VALUES (1, 'A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11', '0xDEADBEEF', '2026-10-01', '14:30:00');`
	res, err = server.ExecuteSQL(ins1)
	if err != nil {
		t.Fatalf("INSERT 1 failed: %v", err)
	}

	ins2 := `INSERT INTO device_events (id, event_id, payload, event_date, event_time)
VALUES (2, 'b1ffcd88-8b0a-3de7-aa5c-5aa8ac270b22', '0xCAFEBABE', '2026-09-15', '09:15:30');`
	res, err = server.ExecuteSQL(ins2)
	if err != nil {
		t.Fatalf("INSERT 2 failed: %v", err)
	}

	// 3. Select all records and verify formatting
	selAll := `SELECT id, event_id, payload, event_date, event_time FROM device_events ORDER BY id;`
	res, err = server.ExecuteSQL(selAll)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}

	row1 := res.Rows[0]
	if row1["event_id"] != "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11" {
		t.Errorf("expected lowercase canonical UUID, got %v", row1["event_id"])
	}
	if row1["payload"] != "0xdeadbeef" {
		t.Errorf("expected 0xdeadbeef, got %v", row1["payload"])
	}
	if row1["event_date"] != "2026-10-01" {
		t.Errorf("expected 2026-10-01, got %v", row1["event_date"])
	}
	if row1["event_time"] != "14:30:00" {
		t.Errorf("expected 14:30:00, got %v", row1["event_time"])
	}

	// 4. Test WHERE filtering on Date and UUID
	selFilter := `SELECT id FROM device_events WHERE event_date = '2026-10-01';`
	res, err = server.ExecuteSQL(selFilter)
	if err != nil {
		t.Fatalf("SELECT WHERE date failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["id"] != int64(1) {
		t.Errorf("expected row id 1 for date filter, got %+v", res.Rows)
	}
}
