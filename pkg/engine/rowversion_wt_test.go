//go:build cgo && wiredtiger

package engine_test

import (
	"bytes"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/wiredtiger"
)

func TestRowVersion_WiredTiger_InsertAndUpdateMonotonic(t *testing.T) {
	wtDir := t.TempDir()
	eng := wiredtiger.New()
	if err := eng.Open(wtDir); err != nil {
		t.Fatalf("wiredtiger.Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE wt_inv (id INT PRIMARY KEY, item VARCHAR(50), ver ROWVERSION);")
	if err != nil {
		t.Fatalf("CREATE TABLE wt_inv failed: %v", err)
	}

	// 1. Insert row 1
	_, err = coord.ExecuteSQL("INSERT INTO wt_inv (id, item) VALUES (1, 'ItemA');")
	if err != nil {
		t.Fatalf("INSERT row 1 failed: %v", err)
	}

	res1, err := coord.ExecuteSQL("SELECT id, item, ver FROM wt_inv WHERE id = 1;")
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

	// 2. Insert row 2 and verify ver2 > ver1
	_, err = coord.ExecuteSQL("INSERT INTO wt_inv (id, item) VALUES (2, 'ItemB');")
	if err != nil {
		t.Fatalf("INSERT row 2 failed: %v", err)
	}

	res2, err := coord.ExecuteSQL("SELECT id, item, ver FROM wt_inv WHERE id = 2;")
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
	_, err = coord.ExecuteSQL("UPDATE wt_inv SET item = 'ItemA_V2' WHERE id = 1;")
	if err != nil {
		t.Fatalf("UPDATE row 1 failed: %v", err)
	}

	res1Updated, err := coord.ExecuteSQL("SELECT id, item, ver FROM wt_inv WHERE id = 1;")
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
