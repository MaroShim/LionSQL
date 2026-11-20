package tds

import (
	"bytes"
	"strings"
	"testing"
)

func TestTDS_AuditRound14_Regression(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)

	// -------------------------------------------------------------------------
	// DEFECT 1: Large binary / string literal (> 8000 bytes) serialization must use PLP
	// -------------------------------------------------------------------------
	t.Run("DEFECT1_LargeBinary_PLP_Mode", func(t *testing.T) {
		// 12000 bytes hex = 24000 chars, exceeds 8000 bytes.
		h := "0x" + strings.Repeat("41", 12000)
		payload := sendSQLBatch(t, conn, "SELECT "+h+" AS large_bin;")

		// Column metadata should specify colSize = -1 (0xFFFF) for PLP (TypeBigVarBin 0xA5)
		if !bytes.Contains(payload, []byte{TokenColMetaData}) {
			t.Fatalf("expected TokenColMetaData (0x81)")
		}

		// Verify TokenColMetaData has 0xFFFF (PLP indicator) for the column length
		colIdx := bytes.Index(payload, []byte{TokenColMetaData})
		metaPayload := payload[colIdx:]

		// [MS-TDS] COLMETADATA: Token(1) + Count(2) + UserType(4) + Flags(2) + TypeInfo
		// TypeBigVarBin (0xA5) followed by MaxLength (2 bytes uint16: 0xFFFF)
		if !bytes.Contains(metaPayload, []byte{TypeBigVarBin, 0xFF, 0xFF}) {
			t.Fatalf("expected TypeBigVarBin (0xA5) with MaxLength 0xFFFF (PLP), got metadata: %x", metaPayload[:min(len(metaPayload), 30)])
		}
	})

	// -------------------------------------------------------------------------
	// DEFECT 2: Table-less scalar query with unsatisfied condition (WHERE 1=0)
	// must still return TokenColMetaData with column names
	// -------------------------------------------------------------------------
	t.Run("DEFECT2_ScalarSelect_Where1Equals0_ReturnsColMetaData", func(t *testing.T) {
		payload := sendSQLBatch(t, conn, "SELECT 1 AS col_a, 'str' AS col_b WHERE 1=0;")

		if !bytes.Contains(payload, []byte{TokenColMetaData}) {
			t.Fatalf("expected TokenColMetaData (0x81) for WHERE 1=0 scalar query, but none was sent (empty metadata)")
		}

		colIdx := bytes.Index(payload, []byte{TokenColMetaData})
		metaPayload := payload[colIdx:]
		// Column count should be 2 (uint16 = 0x02, 0x00)
		if len(metaPayload) < 3 || metaPayload[1] != 2 || metaPayload[2] != 0 {
			t.Fatalf("expected 2 columns in TokenColMetaData, got: %x", metaPayload[:min(len(metaPayload), 10)])
		}
	})

	// -------------------------------------------------------------------------
	// DEFECT 3: ALTER TABLE RENAME COLUMN on indexed column must synchronize index columns
	// -------------------------------------------------------------------------
	t.Run("DEFECT3_AlterTable_RenameColumn_SynchronizesIndex", func(t *testing.T) {
		_ = sendSQLBatch(t, conn, "CREATE TABLE t_ren (id INT PRIMARY KEY, old_name VARCHAR(20));")
		_ = sendSQLBatch(t, conn, "CREATE INDEX idx_old ON t_ren (old_name);")

		// RENAME COLUMN
		payload := sendSQLBatch(t, conn, "ALTER TABLE t_ren RENAME COLUMN old_name TO new_name;")
		if bytes.Contains(payload, []byte{TokenError}) {
			t.Fatalf("ALTER TABLE RENAME COLUMN failed unexpectedly: %s", string(payload))
		}

		// Query using the new column name
		resPayload := sendSQLBatch(t, conn, "SELECT new_name FROM t_ren;")
		if bytes.Contains(resPayload, []byte{TokenError}) {
			t.Fatalf("SELECT new_name failed after RENAME COLUMN: %s", string(resPayload))
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
