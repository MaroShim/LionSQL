package tds

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"net"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// setupADSTestServer creates a TDS server without pre-configured TLS,
// verifying LionSQL's automatic self-signed TLS certificate generation on-demand.
func setupADSTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	cat := catalog.New(eng)
	server := NewServer("127.0.0.1:0", cat, eng)

	cleanup := func() {
		server.Close()
		eng.Close()
	}
	return server, cleanup
}

func connectADSClient(t *testing.T, srv *Server) net.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	go srv.handleConnection(serverConn)
	t.Cleanup(func() {
		_ = clientConn.Close()
	})
	return clientConn
}

// performADSTLSHandshake simulates Azure Data Studio / SqlClient v5+ connection
// which defaults to Encrypt=Mandatory (EncryptReq=0x01) with TrustServerCertificate=true.
func performADSTLSHandshake(t *testing.T, conn net.Conn, clientEncrypt byte) net.Conn {
	t.Helper()

	// 1. Send Prelogin packet requesting client encryption
	preloginPayload := BuildPreloginResponse(clientEncrypt)
	if err := WritePacket(conn, PacketPrelogin, 0, preloginPayload); err != nil {
		t.Fatalf("failed to send ADS Prelogin packet: %v", err)
	}

	// 2. Read Prelogin response from server
	respPkt, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read Prelogin response: %v", err)
	}
	if respPkt.Type != PacketTabular {
		t.Fatalf("expected PacketTabular (0x04) in prelogin response, got 0x%02X", respPkt.Type)
	}

	opts := ParsePrelogin(respPkt.Payload)
	serverEnc, ok := opts[PreloginOptionEncryption]
	if !ok || len(serverEnc) == 0 {
		t.Fatalf("missing encryption option in server prelogin response")
	}

	serverEncryptOption := serverEnc[0]
	if serverEncryptOption != EncryptOn && serverEncryptOption != EncryptReq {
		t.Fatalf("expected EncryptOn (0x01) or EncryptReq (0x03) from server, got 0x%02X", serverEncryptOption)
	}

	// 3. Upgrade via encapsulated TDS 7.x TLS Handshake
	clientHandshakeConn := NewTDSHandshakeConn(conn, 0)
	tlsClient := tls.Client(clientHandshakeConn, &tls.Config{
		InsecureSkipVerify: true,
	})

	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("client TDS TLS handshake failed: %v", err)
	}
	clientHandshakeConn.FinishHandshake()

	// 4. Send Login7 over encrypted connection using standard 94-byte header
	var loginBuf bytes.Buffer
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	_ = binary.Write(&loginBuf, binary.BigEndian, uint32(0x74000004)) // TDS 7.4
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(4096))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1234))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	loginBuf.WriteByte(0x20)
	loginBuf.WriteByte(0x03)
	loginBuf.WriteByte(0x00)
	loginBuf.WriteByte(0x00)
	_ = binary.Write(&loginBuf, binary.LittleEndian, int32(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1033))

	userName := "sa"
	appName := "azuredatastudio"
	dbName := "master"
	headerLen := uint16(94)

	u16User := EncodeUTF16LE(userName)
	u16App := EncodeUTF16LE(appName)
	u16DB := EncodeUTF16LE(dbName)

	ibUser := headerLen
	cchUser := uint16(len(userName))

	ibApp := ibUser + uint16(len(u16User))
	cchApp := uint16(len(appName))

	ibDB := ibApp + uint16(len(u16App))
	cchDB := uint16(len(dbName))

	// HostName (36..40)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// UserName (40..44)
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibUser)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchUser)
	// Password (44..48)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// AppName (48..52)
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibApp)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchApp)
	// ServerName (52..56)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// Extension (56..60)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// CltIntName (60..64)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// Language (64..68)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// Database (68..72)
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibDB)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchDB)
	// ClientID (72..78 - 6 bytes MAC)
	loginBuf.Write([]byte{0x00, 0x15, 0x5D, 0x12, 0x34, 0x56})
	// SSPI (78..82)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// AtchDBFile (82..86)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// ChangePassword (86..90)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	// cbSSPILong (90..94)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))

	loginBuf.Write(u16User)
	loginBuf.Write(u16App)
	loginBuf.Write(u16DB)

	totalLen := uint32(loginBuf.Len())
	rawLogin := loginBuf.Bytes()
	binary.LittleEndian.PutUint32(rawLogin[:4], totalLen)

	if err := WritePacket(tlsClient, PacketLogin7, 0, rawLogin); err != nil {
		t.Fatalf("failed to send Login7 packet over TLS: %v", err)
	}

	loginResp, err := ReadPacket(tlsClient)
	if err != nil {
		t.Fatalf("failed to read Login7 response over TLS: %v", err)
	}
	if loginResp.Type != PacketTabular {
		t.Fatalf("expected PacketTabular (0x04) in login response, got 0x%02X", loginResp.Type)
	}

	return tlsClient
}

func sendADSSQLBatch(t *testing.T, conn net.Conn, sql string) []byte {
	t.Helper()
	u16 := EncodeUTF16LE(sql)
	if err := WritePacket(conn, PacketSQLBatch, 1, u16); err != nil {
		t.Fatalf("failed to send SQL batch: %v", err)
	}

	resp, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read SQL batch response: %v", err)
	}
	return resp.Payload
}

// 1. Azure Data Studio Auto-TLS Prelogin Negotiation (Encrypt=Mandatory)
func TestADS_AutoTLS_Prelogin_EncryptionMandatory(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	// EncryptReq simulates ADS/SqlClient default: Encrypt=Mandatory
	tlsConn := performADSTLSHandshake(t, conn, EncryptReq)

	// Send basic probe query over encrypted channel
	payload := sendADSSQLBatch(t, tlsConn, "SELECT 1 AS connected;")
	if !bytes.Contains(payload, EncodeUTF16LE("connected")) {
		t.Fatalf("expected 'connected' column in encrypted query response")
	}
}

// 2. Azure Data Studio Environment Detection Batch (host_platform Windows Invariant 11)
func TestADS_EnvironmentBatch_HostPlatformWindows(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptOn)

	adsEnvBatch := `DECLARE @edition sysname;
SET @edition = cast(SERVERPROPERTY(N'EDITION') as sysname);
SELECT case when @edition = N'SQL Azure' then 2 else 1 end as 'DatabaseEngineType',
@edition AS Edition,
CONVERT(bigint, SERVERPROPERTY(N'EditionID')) AS EditionID,
SERVERPROPERTY('EngineEdition') AS DatabaseEngineEdition,
SERVERPROPERTY('ProductVersion') AS ProductVersion,
@@MICROSOFTVERSION AS MicrosoftVersion,
case when serverproperty('EngineEdition') = 12 then 1 when serverproperty('EngineEdition') = 11 and @@version like 'Microsoft Azure SQL Data Warehouse%' then 1 else 0 end as IsFabricServer,
convert(sysname, SERVERPROPERTY(N'Collation')) AS Collation;
select host_platform from sys.dm_os_host_info
if @edition = N'SQL Azure'
  select 'TCP' as ConnectionProtocol
else
  exec ('select CONVERT(nvarchar(40),CONNECTIONPROPERTY(''net_transport'')) as ConnectionProtocol')`

	payload := sendADSSQLBatch(t, tlsConn, adsEnvBatch)

	if !bytes.Contains(payload, EncodeUTF16LE("DatabaseEngineType")) {
		t.Fatalf("expected 'DatabaseEngineType' in ADS result set 1")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("host_platform")) {
		t.Fatalf("expected 'host_platform' in ADS result set 2")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("Windows")) {
		t.Fatalf("Invariant 11 violation: expected 'Windows' host_platform in ADS result set 2")
	}
	if bytes.Contains(payload, EncodeUTF16LE("macOS")) || bytes.Contains(payload, EncodeUTF16LE("Darwin")) || bytes.Contains(payload, EncodeUTF16LE("Linux")) {
		t.Fatalf("Invariant 11 violation: host_platform must NEVER return macOS or Linux to ADS")
	}
	if !bytes.Contains(payload, EncodeUTF16LE("ConnectionProtocol")) {
		t.Fatalf("expected 'ConnectionProtocol' in ADS result set 3")
	}
}

// 3. Azure Data Studio Root Server Discovery Batch (#SVer + xp_msver)
func TestADS_ServerInitBatch_SVer_xp_msver(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptOn)

	smoServerInitBatch := `create table #SVer(ID int,  Name  sysname, Internal_Value int, Value nvarchar(512))
          insert #SVer exec master.dbo.xp_msver
          insert #SVer select t.*
          from sys.dm_os_host_info
          CROSS APPLY (
          VALUES
          (1001, 'HostPlatform', 0, host_platform),
          (1002, 'HostDistribution', 0, host_distribution),
          (1003, 'HostRelease', 0, host_release),
          (1004, 'HostServicePackLevel', 0, host_service_pack_level),
          (1005, 'HostArchitecture', host_architecture, cast(host_architecture as nvarchar(512)))
          ) t(ID, Name, Internal_Value, Value)
          select Name, Internal_Value, Value from #SVer
          drop table #SVer`

	payload := sendADSSQLBatch(t, tlsConn, smoServerInitBatch)
	if !bytes.Contains(payload, EncodeUTF16LE("HostPlatform")) {
		t.Fatalf("expected 'HostPlatform' in #SVer discovery response")
	}
}

// 4. Azure Data Studio Object Explorer Introspection & Queries
func TestADS_ObjectExplorer_Databases_Tables_Columns(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptOn)

	// Step 4.1: Seed DDL & Data
	seedDDL := `CREATE TABLE customers (
		customer_id INT PRIMARY KEY,
		name VARCHAR(100),
		email VARCHAR(100),
		is_active BIT
	);
	INSERT INTO customers VALUES (1, 'Alice', 'alice@test.com', 1), (2, 'Bob', 'bob@test.com', 0);`
	sendADSSQLBatch(t, tlsConn, seedDDL)

	// Step 4.2: ADS sys.databases query with has_dbaccess
	qDatabases := "SELECT name, database_id FROM sys.databases WHERE 1 = has_dbaccess(name) ORDER BY name;"
	payloadDB := sendADSSQLBatch(t, tlsConn, qDatabases)
	if !bytes.Contains(payloadDB, EncodeUTF16LE("master")) {
		t.Fatalf("expected 'master' in sys.databases response")
	}

	// Step 4.3: ADS sys.tables query
	qTables := "SELECT t.name AS name, s.name AS schema_name, t.object_id, t.type FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.schema_id ORDER BY s.name, t.name;"
	payloadTables := sendADSSQLBatch(t, tlsConn, qTables)
	if !bytes.Contains(payloadTables, EncodeUTF16LE("customers")) {
		t.Fatalf("expected 'customers' table in sys.tables response")
	}

	// Step 4.4: ADS Data Grid TOP 1000 Query with 3-Part Name
	qTop1000 := "SELECT TOP (1000) [customer_id], [name], [email] FROM [master].[dbo].[customers];"
	payloadTop := sendADSSQLBatch(t, tlsConn, qTop1000)
	if !bytes.Contains(payloadTop, EncodeUTF16LE("Alice")) || !bytes.Contains(payloadTop, EncodeUTF16LE("Bob")) {
		t.Fatalf("expected Alice and Bob records in TOP 1000 data grid query")
	}
}

// 5. Azure Data Studio T-SQL Query Editor Lifecycle
func TestADS_QueryEditor_Execution(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptOn)

	// User creates table, inserts records, selects, and drops table in Query Editor
	queryEditorBatch := `CREATE TABLE ads_notes (id INT PRIMARY KEY, note NVARCHAR(200));
INSERT INTO ads_notes VALUES (1, N'LionSQL ADS Native Support');
SELECT note FROM ads_notes WHERE id = 1;`

	payload := sendADSSQLBatch(t, tlsConn, queryEditorBatch)
	if !bytes.Contains(payload, EncodeUTF16LE("LionSQL ADS Native Support")) {
		t.Fatalf("expected 'LionSQL ADS Native Support' in Query Editor result")
	}
}

// 6. Azure Data Studio Plaintext Negotiation (Encrypt=Optional / EncryptOff)
func TestADS_Prelogin_EncryptionOptional_PlaintextLogin(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)

	// Step 1: Send Prelogin with EncryptOff (0x00)
	preloginPayload := BuildPreloginResponse(EncryptOff)
	if err := WritePacket(conn, PacketPrelogin, 0, preloginPayload); err != nil {
		t.Fatalf("failed to send Prelogin packet: %v", err)
	}

	// Step 2: Read Prelogin response (server must respond with EncryptNotSup so client doesn't do TLS)
	respPkt, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read Prelogin response: %v", err)
	}
	if respPkt.Type != PacketTabular {
		t.Fatalf("expected PacketTabular (0x04) in prelogin response, got 0x%02X", respPkt.Type)
	}
	opts := ParsePrelogin(respPkt.Payload)
	serverEnc, ok := opts[PreloginOptionEncryption]
	if !ok || len(serverEnc) == 0 {
		t.Fatalf("missing encryption option in server prelogin response")
	}
	if serverEnc[0] != EncryptNotSup {
		t.Fatalf("expected EncryptNotSup (0x02) for EncryptOff request, got 0x%02X", serverEnc[0])
	}

	// Step 3: Send Login7 directly over plaintext connection (without TLS handshake)
	var loginBuf bytes.Buffer
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	_ = binary.Write(&loginBuf, binary.BigEndian, uint32(0x74000004))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(4096))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1234))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	loginBuf.WriteByte(0x20)
	loginBuf.WriteByte(0x03)
	loginBuf.WriteByte(0x00)
	loginBuf.WriteByte(0x00)
	_ = binary.Write(&loginBuf, binary.LittleEndian, int32(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1033))

	userName := "sa"
	appName := "azuredatastudio"
	dbName := "master"
	headerLen := uint16(94)

	u16User := EncodeUTF16LE(userName)
	u16App := EncodeUTF16LE(appName)
	u16DB := EncodeUTF16LE(dbName)

	ibUser := headerLen
	cchUser := uint16(len(userName))
	ibApp := ibUser + uint16(len(u16User))
	cchApp := uint16(len(appName))
	ibDB := ibApp + uint16(len(u16App))
	cchDB := uint16(len(dbName))

	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibUser)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchUser)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibApp)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchApp)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibDB)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchDB)
	loginBuf.Write([]byte{0x00, 0x15, 0x5D, 0x12, 0x34, 0x56})
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))

	loginBuf.Write(u16User)
	loginBuf.Write(u16App)
	loginBuf.Write(u16DB)

	rawLogin := loginBuf.Bytes()
	binary.LittleEndian.PutUint32(rawLogin[:4], uint32(len(rawLogin)))

	if err := WritePacket(conn, PacketLogin7, 0, rawLogin); err != nil {
		t.Fatalf("failed to send Login7 packet: %v", err)
	}

	loginResp, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read Login7 response: %v", err)
	}
	if loginResp.Type != PacketTabular {
		t.Fatalf("expected PacketTabular (0x04) in login response, got 0x%02X", loginResp.Type)
	}

	// Step 4: Verify SQL queries execute over plaintext connection
	payload := sendADSSQLBatch(t, conn, "SELECT 12345 AS plain_test;")
	if !bytes.Contains(payload, EncodeUTF16LE("plain_test")) {
		t.Fatalf("expected 'plain_test' in plaintext SQL batch response")
	}
}

// 7. Full Real-World Azure Data Studio Lifecycle Simulation
func TestADS_RealWorld_FullLifecycle_Simulation(t *testing.T) {
	server, cleanup := setupADSTestServer(t)
	defer cleanup()

	conn := connectADSClient(t, server)
	tlsConn := performADSTLSHandshake(t, conn, EncryptOn)

	// Step 7.1: Environment Detection Batch
	envBatch := `DECLARE @edition sysname;
SET @edition = cast(SERVERPROPERTY(N'EDITION') as sysname);
SELECT case when @edition = N'SQL Azure' then 2 else 1 end as 'DatabaseEngineType',
SERVERPROPERTY('EngineEdition') AS DatabaseEngineEdition,
SERVERPROPERTY('ProductVersion') AS ProductVersion,
@@MICROSOFTVERSION AS MicrosoftVersion;
select host_platform from sys.dm_os_host_info
if @edition = N'SQL Azure' 
  select 'TCP' as ConnectionProtocol
else
  exec ('select CONVERT(nvarchar(40),CONNECTIONPROPERTY(''net_transport'')) as ConnectionProtocol')`
	envResp := sendADSSQLBatch(t, tlsConn, envBatch)
	if !bytes.Contains(envResp, EncodeUTF16LE("DatabaseEngineType")) {
		t.Fatalf("expected DatabaseEngineType in env response")
	}

	// Step 7.2: Session Init (#SVer with Processors)
	sverBatch := `create table #SVer(ID int,  Name  sysname, Internal_Value int, Value nvarchar(512))
          insert #SVer exec master.dbo.xp_msver
          insert #SVer select t.*
          from sys.dm_os_host_info
          CROSS APPLY (
          VALUES
          (1001, 'host_platform', 0, host_platform),
          (1002, 'host_distribution', 0, host_distribution),
          (1003, 'host_release', 0, host_release),
          (1004, 'host_service_pack_level', 0, host_service_pack_level),
          (1005, 'host_sku', host_sku, ''),
          (1006, 'HardwareGeneration', '', ''),
          (1007, 'ServiceTier', '', ''),
          (1008, 'ReservedStorageSizeMB', '0', '0'),
          (1009, 'UsedStorageSizeMB', '0', '0')
          ) t(id, [name], internal_value, [value])

SELECT
(select Internal_Value from #SVer where Name = N'ProcessorCount') AS [Processors]

        drop table #SVer`
	sverResp := sendADSSQLBatch(t, tlsConn, sverBatch)
	if !bytes.Contains(sverResp, EncodeUTF16LE("Processors")) {
		t.Fatalf("expected Processors column in #SVer response, got %v", sverResp)
	}

	// Step 7.2.1: ADS sys.syslanguages query
	langBatch := `SELECT s.name AS [Name], s.alias AS [Alias], s.lcid AS [LocaleID], s.langid AS [LangID] FROM sys.syslanguages s ORDER BY [Name] ASC`
	langResp := sendADSSQLBatch(t, tlsConn, langBatch)
	if !bytes.Contains(langResp, EncodeUTF16LE("us_english")) || !bytes.Contains(langResp, EncodeUTF16LE("English")) {
		t.Fatalf("expected us_english/English in sys.syslanguages response")
	}

	// Step 7.2.2: SMO Server Facet Batch 2 (@MasterPath + IsCaseSensitive + MasterDBPath)
	facetBatch := `declare @MasterPath nvarchar(512)
declare @LogPath nvarchar(512)
declare @ErrorLog nvarchar(512)
declare @ErrorLogPath nvarchar(512)
declare @SmoRoot nvarchar(512)
SELECT
CAST(case when 'a' <> 'A' then 1 else 0 end AS bit) AS [IsCaseSensitive],
@@MAX_PRECISION AS [MaxPrecision],
@ErrorLogPath AS [ErrorLogPath],
@SmoRoot AS [RootDirectory],
@LogPath AS [MasterDBLogPath],
@MasterPath AS [MasterDBPath]`
	facetResp := sendADSSQLBatch(t, tlsConn, facetBatch)
	if !bytes.Contains(facetResp, EncodeUTF16LE("IsCaseSensitive")) || !bytes.Contains(facetResp, EncodeUTF16LE("MasterDBPath")) {
		t.Fatalf("expected IsCaseSensitive/MasterDBPath in facet response")
	}

	// Step 7.3: Seed a user table in lionsql/master
	sendADSSQLBatch(t, tlsConn, "CREATE TABLE products (product_id INT PRIMARY KEY, title VARCHAR(50), price INT);")
	sendADSSQLBatch(t, tlsConn, "INSERT INTO products VALUES (1, 'LionBook', 1500), (2, 'LionTab', 800);")

	// Step 7.4: ADS User Databases Tree Query
	dbUserBatch := `SELECT
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
[Name] ASC`
	dbUserResp := sendADSSQLBatch(t, tlsConn, dbUserBatch)
	if !bytes.Contains(dbUserResp, EncodeUTF16LE("lionsql")) {
		t.Fatalf("expected 'lionsql' in user databases response")
	}
	if !bytes.Contains(dbUserResp, EncodeUTF16LE("Name")) || !bytes.Contains(dbUserResp, EncodeUTF16LE("Status")) {
		t.Fatalf("expected Name and Status columns in user databases response")
	}

	// Step 7.5: ADS System Databases Tree Query
	dbSysBatch := `SELECT dtb.name AS [Name] FROM master.sys.databases AS dtb
WHERE ((CAST(case when dtb.name in ('master','model','msdb','tempdb') then 1 else dtb.is_distributor end AS bit)='1'))
ORDER BY [Name] ASC`
	dbSysResp := sendADSSQLBatch(t, tlsConn, dbSysBatch)
	if !bytes.Contains(dbSysResp, EncodeUTF16LE("master")) || !bytes.Contains(dbSysResp, EncodeUTF16LE("tempdb")) {
		t.Fatalf("expected master/tempdb in system databases response")
	}

	// Step 7.6: ADS Object Explorer Table Browsing Query
	tablesBatch := `SELECT t.name AS [name], s.name AS [schema_name], t.object_id, t.type
FROM sys.tables t
JOIN sys.schemas s ON t.schema_id = s.schema_id
ORDER BY s.name, t.name;`
	tablesResp := sendADSSQLBatch(t, tlsConn, tablesBatch)
	if !bytes.Contains(tablesResp, EncodeUTF16LE("products")) {
		t.Fatalf("expected 'products' table in sys.tables response")
	}

	// Step 7.7: ADS Data Grid TOP 1000 Query
	top1000Batch := `SELECT TOP 1000 [product_id], [title], [price] FROM [products];`
	top1000Resp := sendADSSQLBatch(t, tlsConn, top1000Batch)
	if !bytes.Contains(top1000Resp, EncodeUTF16LE("LionBook")) || !bytes.Contains(top1000Resp, EncodeUTF16LE("LionTab")) {
		t.Fatalf("expected LionBook and LionTab in TOP 1000 response")
	}
}
