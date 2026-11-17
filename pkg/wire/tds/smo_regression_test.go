package tds

import (
	"bytes"
	"testing"
)

// TestSMO_ServerTriggers_Query tests the exact query ADS/SMO issues when expanding server/database objects.
func TestSMO_ServerTriggers_Query(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptReq)

	query := `
SELECT
tr.name AS [Name],
~tr.is_disabled AS [IsEnabled]
FROM
master.sys.server_triggers AS tr
LEFT OUTER JOIN master.sys.server_assembly_modules AS mod ON mod.object_id = tr.object_id
WHERE
(tr.parent_class = 100)
ORDER BY
[Name] ASC`

	payload := sendADSSQLBatch(t, tlsConn, query)

	// Ensure no TDS error token (0xAA) was returned
	if bytes.Contains(payload, []byte{TokenError}) {
		t.Fatalf("SMO server triggers query returned TDS error token (0xAA)")
	}

	// Verify columns "Name" and "IsEnabled" are present in UTF-16LE
	if !bytes.Contains(payload, EncodeUTF16LE("Name")) {
		t.Fatalf("expected column 'Name' in server_triggers response")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("IsEnabled")) {
		t.Fatalf("expected column 'IsEnabled' in server_triggers response")
	}

	// Verify TokenDone (0xFD) indicating successful batch completion
	if !bytes.Contains(payload, []byte{TokenDone}) {
		t.Fatalf("expected TokenDone (0xFD) in response")
	}
}

// TestSMO_Database_ServiceObjectives_Query verifies sys.database_service_objectives query.
func TestSMO_Database_ServiceObjectives_Query(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptReq)

	query := `SELECT database_id, edition, service_objective FROM sys.database_service_objectives`
	payload := sendADSSQLBatch(t, tlsConn, query)

	if bytes.Contains(payload, []byte{TokenError}) {
		t.Fatalf("sys.database_service_objectives query returned TDS error token (0xAA)")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("database_id")) {
		t.Fatalf("expected column 'database_id' in response")
	}
	if !bytes.Contains(payload, []byte{TokenDone}) {
		t.Fatalf("expected TokenDone (0xFD) in response")
	}
}

// TestSMO_Database_ScopedConfigurations_Query verifies sys.database_scoped_configurations query.
func TestSMO_Database_ScopedConfigurations_Query(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptReq)

	query := `SELECT configuration_id, name, value FROM sys.database_scoped_configurations`
	payload := sendADSSQLBatch(t, tlsConn, query)

	if bytes.Contains(payload, []byte{TokenError}) {
		t.Fatalf("sys.database_scoped_configurations query returned TDS error token (0xAA)")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("configuration_id")) {
		t.Fatalf("expected column 'configuration_id' in response")
	}
	if !bytes.Contains(payload, []byte{TokenDone}) {
		t.Fatalf("expected TokenDone (0xFD) in response")
	}
}

// TestSMO_Database_Languages_Query verifies sys.syslanguages query.
func TestSMO_Database_Languages_Query(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptReq)

	query := `SELECT langid, name, alias, lcid FROM sys.syslanguages ORDER BY langid`
	payload := sendADSSQLBatch(t, tlsConn, query)

	if bytes.Contains(payload, []byte{TokenError}) {
		t.Fatalf("sys.syslanguages query returned TDS error token (0xAA)")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("langid")) || !bytes.Contains(payload, EncodeUTF16LE("us_english")) {
		t.Fatalf("expected 'langid' and 'us_english' in syslanguages response")
	}
	if !bytes.Contains(payload, []byte{TokenDone}) {
		t.Fatalf("expected TokenDone (0xFD) in response")
	}
}

// TestSMO_Database_FullExpansion_Sequence executes the complete sequence of SMO queries during tree expansion.
func TestSMO_Database_FullExpansion_Sequence(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptReq)

	// Step 1: Create a sample table
	_ = sendADSSQLBatch(t, tlsConn, `CREATE TABLE sales (id INT PRIMARY KEY, amount FLOAT, customer NVARCHAR(50))`)

	sequences := []struct {
		name          string
		sql           string
		mustHaveCols  []string
	}{
		{
			name: "1. Databases List (ADS/SSMS Canonical)",
			sql: `
SELECT
dtb.name AS [Name],
dtb.database_id AS [ID],
CAST(case when dtb.name in ('master','model','msdb','tempdb') then 1 else dtb.is_distributor end AS bit) AS [IsSystemObject],
dtb.collation_name AS [Collation],
CAST(has_dbaccess(dtb.name) AS bit) AS [IsAccessible],
dtb.name AS [DatabaseName2]
FROM
master.sys.databases AS dtb
ORDER BY
[Name] ASC`,
			mustHaveCols: []string{"Name", "ID", "IsSystemObject", "Collation", "IsAccessible", "DatabaseName2"},
		},
		{
			name: "1-1. Databases List with Bitwise Status (ADS Real-World)",
			sql: `
SELECT
dtb.name AS [Name],
        case
        when dtb.collation_name is null then 0x200
        else 0
        end |
        case
        when 1 = dtb.is_in_standby then 0x40
        else 0
        end |
        case dtb.state
        when 1 then 0x2
        when 2 then 0x8
        when 3 then 0x4
        when 4 then 0x10
        when 5 then 0x100
        when 6 then 0x20
        else 1
        end
       AS [Status]
FROM
master.sys.databases AS dtb
WHERE
((CAST(case when dtb.name in ('master','model','msdb','tempdb') then 1 else dtb.is_distributor end AS bit)='0'))
ORDER BY
[Name] ASC`,
			mustHaveCols: []string{"Name", "Status"},
		},
		{
			name: "2. Server Triggers",
			sql: `
SELECT tr.name AS [Name], ~tr.is_disabled AS [IsEnabled]
FROM master.sys.server_triggers AS tr
LEFT OUTER JOIN master.sys.server_assembly_modules AS mod ON mod.object_id = tr.object_id
WHERE (tr.parent_class = 100) ORDER BY [Name] ASC`,
			mustHaveCols: []string{"Name", "IsEnabled"},
		},
		{
			name:         "3. Database Tables Enumeration",
			sql:          `SELECT tbl.name AS [Name], SCHEMA_NAME(tbl.schema_id) AS [Schema], tbl.object_id FROM sys.tables AS tbl ORDER BY [Schema] ASC, [Name] ASC`,
			mustHaveCols: []string{"Name", "Schema"},
		},
		{
			name: "4. Columns Enumeration",
			sql: `
SELECT clmns.name AS [Name], clmns.column_id AS [ID], usrt.name AS [DataType]
FROM sys.tables AS tbl
INNER JOIN sys.all_columns AS clmns ON clmns.object_id = tbl.object_id
LEFT OUTER JOIN sys.types AS usrt ON usrt.user_type_id = clmns.user_type_id
ORDER BY clmns.column_id ASC`,
			mustHaveCols: []string{"Name", "ID", "DataType"},
		},
		{
			name: "5. Indexes Enumeration",
			sql: `
SELECT i.name AS [Name], i.index_id AS [ID], i.is_primary_key AS [IsPrimaryKey]
FROM sys.indexes AS i
WHERE i.object_id = OBJECT_ID('sales')`,
			mustHaveCols: []string{"Name", "ID"},
		},
		{
			name:         "6. Database Service Objectives",
			sql:          `SELECT dso.database_id, dso.edition, dso.service_objective FROM sys.database_service_objectives dso`,
			mustHaveCols: []string{"database_id", "edition"},
		},
		{
			name:         "7. Database Scoped Configurations",
			sql:          `SELECT configuration_id, name, value FROM sys.database_scoped_configurations`,
			mustHaveCols: []string{"configuration_id", "name"},
		},
		{
			name:         "8. SysLanguages",
			sql:          `SELECT langid, name, alias FROM sys.syslanguages`,
			mustHaveCols: []string{"langid", "name"},
		},
	}

	for _, step := range sequences {
		t.Run(step.name, func(t *testing.T) {
			payload := sendADSSQLBatch(t, tlsConn, step.sql)
			if bytes.Contains(payload, []byte{TokenError}) {
				t.Fatalf("step %q returned TDS error token (0xAA)", step.name)
			}
			for _, col := range step.mustHaveCols {
				if !bytes.Contains(payload, EncodeUTF16LE(col)) {
					t.Fatalf("step %q response missing expected column %q", step.name, col)
				}
			}
			if !bytes.Contains(payload, []byte{TokenDone}) {
				t.Fatalf("step %q response missing TokenDone (0xFD)", step.name)
			}
		})
	}
}
