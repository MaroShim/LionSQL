package tds

import (
	"bytes"
	"testing"
)

func TestTDS_Print_SingleStatement(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)

	payload := sendSQLBatch(t, conn, "PRINT 'Hello from TDS';")

	if !bytes.Contains(payload, []byte{TokenInfo}) {
		t.Fatalf("expected TokenInfo (0xAB) in response, got: %v", payload)
	}
	if !bytes.Contains(payload, EncodeUTF16LE("Hello from TDS")) {
		t.Fatalf("expected payload to contain UTF-16LE 'Hello from TDS'")
	}
	if !bytes.Contains(payload, []byte{TokenDone}) {
		t.Fatalf("expected TokenDone in response")
	}
}

func TestTDS_Print_MultiStatementBatch(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)

	payload := sendSQLBatch(t, conn, "PRINT 'Batch Step 1'; SELECT 100 AS num; PRINT 'Batch Step 2';")

	if !bytes.Contains(payload, []byte{TokenInfo}) {
		t.Fatalf("expected TokenInfo (0xAB) in multi-statement batch response")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("Batch Step 1")) {
		t.Fatalf("expected payload to contain 'Batch Step 1'")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("Batch Step 2")) {
		t.Fatalf("expected payload to contain 'Batch Step 2'")
	}
	if !bytes.Contains(payload, []byte{TokenRow}) {
		t.Fatalf("expected TokenRow in response for SELECT 100 AS num")
	}
	if !bytes.Contains(payload, []byte{TokenDone}) {
		t.Fatalf("expected TokenDone in response")
	}
}

func TestTDS_Print_InsideProcedure(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)

	procDDL := `CREATE PROCEDURE sp_test_print AS BEGIN PRINT 'Inside Proc Message'; SELECT 777 AS val; END;`
	ddlResp := sendSQLBatch(t, conn, procDDL)
	if !bytes.Contains(ddlResp, []byte{TokenDone}) {
		t.Fatalf("CREATE PROCEDURE failed")
	}

	execResp := sendSQLBatch(t, conn, "EXEC sp_test_print;")
	if !bytes.Contains(execResp, []byte{TokenInfo}) {
		t.Fatalf("expected TokenInfo in EXEC response")
	}
	if !bytes.Contains(execResp, EncodeUTF16LE("Inside Proc Message")) {
		t.Fatalf("expected payload to contain 'Inside Proc Message'")
	}
	if !bytes.Contains(execResp, []byte{TokenRow}) {
		t.Fatalf("expected TokenRow in EXEC response")
	}
}

func TestTDS_Print_RaiserrorInfo(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)

	payload := sendSQLBatch(t, conn, "RAISERROR('Info severity 10', 10, 1);")

	if !bytes.Contains(payload, []byte{TokenInfo}) {
		t.Fatalf("expected TokenInfo (0xAB) for RAISERROR with severity 10")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("Info severity 10")) {
		t.Fatalf("expected payload to contain 'Info severity 10'")
	}
}
