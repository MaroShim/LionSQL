package engine_test

import (
	"bytes"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestRowVersion_SingleColumnConstraint(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Multiple ROWVERSION columns in CREATE TABLE must fail
	_, err := coord.ExecuteSQL("CREATE TABLE bad_table (id INT PRIMARY KEY, rv1 ROWVERSION, rv2 ROWVERSION);")
	if err == nil {
		t.Fatalf("expected error on multiple ROWVERSION columns in CREATE TABLE, got nil")
	}
	if !strings.Contains(err.Error(), "only have one rowversion column") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. Single ROWVERSION column in CREATE TABLE must succeed
	_, err = coord.ExecuteSQL("CREATE TABLE good_table (id INT PRIMARY KEY, rv ROWVERSION);")
	if err != nil {
		t.Fatalf("expected CREATE TABLE with single ROWVERSION to succeed, got: %v", err)
	}

	// 3. Adding second ROWVERSION column via ALTER TABLE must fail
	_, err = coord.ExecuteSQL("ALTER TABLE good_table ADD COLUMN rv2 ROWVERSION;")
	if err == nil {
		t.Fatalf("expected error on adding second ROWVERSION column via ALTER TABLE, got nil")
	}
	if !strings.Contains(err.Error(), "only have one rowversion column") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestRowVersion_InsertAndUpdateMonotonic(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE inventory (id INT PRIMARY KEY, item VARCHAR(50), ver ROWVERSION);")
	if err != nil {
		t.Fatalf("CREATE TABLE inventory failed: %v", err)
	}

	// 1. Insert row 1
	_, err = coord.ExecuteSQL("INSERT INTO inventory (id, item) VALUES (1, 'Widget');")
	if err != nil {
		t.Fatalf("INSERT row 1 failed: %v", err)
	}

	res1, err := coord.ExecuteSQL("SELECT id, item, ver FROM inventory WHERE id = 1;")
	if err != nil || len(res1.Rows) == 0 {
		t.Fatalf("SELECT row 1 failed: %v", err)
	}
	ver1Raw := res1.Rows[0]["ver"]
	var ver1Bytes []byte
	switch v := ver1Raw.(type) {
	case []byte:
		ver1Bytes = v
	case string:
		ver1Bytes = []byte(v)
	}
	if len(ver1Bytes) != 8 {
		t.Fatalf("expected 8-byte binary rowversion for row 1, got %T (%v)", ver1Raw, ver1Raw)
	}

	// Verify @@DBTS equals or exceeds ver1
	resDBTS, err := coord.ExecuteSQL("SELECT @@DBTS AS dbts;")
	if err != nil || len(resDBTS.Rows) == 0 {
		t.Fatalf("SELECT @@DBTS failed: %v", err)
	}
	dbtsRaw := resDBTS.Rows[0]["dbts"]
	var dbtsBytes []byte
	switch v := dbtsRaw.(type) {
	case []byte:
		dbtsBytes = v
	case string:
		dbtsBytes = []byte(v)
	}
	if len(dbtsBytes) != 8 {
		t.Fatalf("expected 8-byte binary @@DBTS, got %T (%v)", dbtsRaw, dbtsRaw)
	}
	if bytes.Compare(dbtsBytes, ver1Bytes) < 0 {
		t.Fatalf("expected @@DBTS >= ver1, got @@DBTS=%x, ver1=%x", dbtsBytes, ver1Bytes)
	}

	// 2. Insert row 2 and verify ver2 > ver1
	_, err = coord.ExecuteSQL("INSERT INTO inventory (id, item) VALUES (2, 'Gadget');")
	if err != nil {
		t.Fatalf("INSERT row 2 failed: %v", err)
	}

	res2, err := coord.ExecuteSQL("SELECT id, item, ver FROM inventory WHERE id = 2;")
	if err != nil || len(res2.Rows) == 0 {
		t.Fatalf("SELECT row 2 failed: %v", err)
	}
	ver2Raw := res2.Rows[0]["ver"]
	var ver2Bytes []byte
	switch v := ver2Raw.(type) {
	case []byte:
		ver2Bytes = v
	case string:
		ver2Bytes = []byte(v)
	}
	if len(ver2Bytes) != 8 {
		t.Fatalf("expected 8-byte binary rowversion for row 2, got %T (%v)", ver2Raw, ver2Raw)
	}

	if bytes.Compare(ver2Bytes, ver1Bytes) <= 0 {
		t.Fatalf("expected monotonic increase: ver2 (%x) must be > ver1 (%x)", ver2Bytes, ver1Bytes)
	}

	// 3. Update row 1 and verify ver1_updated > ver2
	_, err = coord.ExecuteSQL("UPDATE inventory SET item = 'Widget_V2' WHERE id = 1;")
	if err != nil {
		t.Fatalf("UPDATE row 1 failed: %v", err)
	}

	res1Updated, err := coord.ExecuteSQL("SELECT id, item, ver FROM inventory WHERE id = 1;")
	if err != nil || len(res1Updated.Rows) == 0 {
		t.Fatalf("SELECT row 1 after update failed: %v", err)
	}
	ver1UpdatedRaw := res1Updated.Rows[0]["ver"]
	var ver1UpdatedBytes []byte
	switch v := ver1UpdatedRaw.(type) {
	case []byte:
		ver1UpdatedBytes = v
	case string:
		ver1UpdatedBytes = []byte(v)
	}
	if len(ver1UpdatedBytes) != 8 {
		t.Fatalf("expected 8-byte binary rowversion for updated row 1, got %T (%v)", ver1UpdatedRaw, ver1UpdatedRaw)
	}

	if bytes.Compare(ver1UpdatedBytes, ver2Bytes) <= 0 {
		t.Fatalf("expected monotonic increase on update: ver1_updated (%x) must be > ver2 (%x)", ver1UpdatedBytes, ver2Bytes)
	}
}

func TestRowVersion_CatalogSysColumnsAndSysTypes(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Verify sys.types exposes rowversion with id 189
	resTypes, err := coord.ExecuteSQL("SELECT name, system_type_id, max_length FROM sys.types WHERE name = 'rowversion';")
	if err != nil || len(resTypes.Rows) == 0 {
		t.Fatalf("query sys.types for rowversion failed: %v", err)
	}
	if resTypes.Rows[0]["system_type_id"] != 189 || resTypes.Rows[0]["max_length"] != 8 {
		t.Fatalf("expected rowversion in sys.types with id=189, max_length=8, got: %+v", resTypes.Rows[0])
	}

	// 2. Create table and verify sys.columns
	_, err = coord.ExecuteSQL("CREATE TABLE test_rv_cat (id INT PRIMARY KEY, ver ROWVERSION);")
	if err != nil {
		t.Fatalf("CREATE TABLE test_rv_cat failed: %v", err)
	}

	resCols, err := coord.ExecuteSQL("SELECT name, system_type_id, user_type_id, max_length, data_type FROM sys.columns WHERE table_name = 'test_rv_cat' AND name = 'ver';")
	if err != nil || len(resCols.Rows) == 0 {
		t.Fatalf("query sys.columns for test_rv_cat failed: %v", err)
	}
	row := resCols.Rows[0]
	if row["system_type_id"] != 189 || row["user_type_id"] != 189 || row["max_length"] != 8 || row["data_type"] != "rowversion" {
		t.Fatalf("expected sys.columns mapping (189, 189, 8, rowversion), got: %+v", row)
	}
}

