package tds

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"

	"lionsql/pkg/storage"
)

// sendNamedRPC sends an RPCRequest addressed by procedure name (as go-mssqldb, tedious and pymssql do).
func sendNamedRPC(t *testing.T, conn net.Conn, procName string, paramsPayload []byte) []byte {
	t.Helper()
	var rpcBuf bytes.Buffer
	nameUTF16 := EncodeUTF16LE(procName)
	_ = binary.Write(&rpcBuf, binary.LittleEndian, uint16(len(nameUTF16)/2))
	rpcBuf.Write(nameUTF16)
	_ = binary.Write(&rpcBuf, binary.LittleEndian, uint16(0))
	rpcBuf.Write(paramsPayload)

	if err := WritePacket(conn, PacketRPC, 1, rpcBuf.Bytes()); err != nil {
		t.Fatalf("failed to send named RPC packet: %v", err)
	}
	resp, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read named RPC response: %v", err)
	}
	return resp.Payload
}

// returnValue is a decoded RETURNVALUE (0xAC) token for an INTN output parameter.
type returnValue struct {
	ordinal uint16
	name    string
	size    int
	value   int64
	isNull  bool
}

// findIntReturnValue decodes the first RETURNVALUE token whose TYPE_INFO is INTN.
func findIntReturnValue(t *testing.T, resp []byte) returnValue {
	t.Helper()
	for i := 0; i < len(resp); i++ {
		if resp[i] != TokenReturnValue || i+4 > len(resp) {
			continue
		}
		pos := i + 1
		ordinal := binary.LittleEndian.Uint16(resp[pos:])
		pos += 2
		nameChars := int(resp[pos])
		pos++
		if pos+nameChars*2+1+4+2+2 > len(resp) {
			continue
		}
		name := DecodeUTF16LE(resp[pos : pos+nameChars*2])
		pos += nameChars * 2
		pos++    // status
		pos += 4 // usertype
		pos += 2 // flags
		if resp[pos] != TypeIntN {
			continue
		}
		pos++
		size := int(resp[pos])
		pos++
		dataLen := int(resp[pos])
		pos++
		rv := returnValue{ordinal: ordinal, name: name, size: size}
		if dataLen == 0 {
			rv.isNull = true
			return rv
		}
		if pos+dataLen > len(resp) {
			continue
		}
		switch dataLen {
		case 1:
			rv.value = int64(int8(resp[pos]))
		case 2:
			rv.value = int64(int16(binary.LittleEndian.Uint16(resp[pos:])))
		case 4:
			rv.value = int64(int32(binary.LittleEndian.Uint32(resp[pos:])))
		case 8:
			rv.value = int64(binary.LittleEndian.Uint64(resp[pos:]))
		}
		return rv
	}
	t.Fatalf("no INTN RETURNVALUE token found in response: %x", resp)
	return returnValue{}
}

func setupOutputProc(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = sendSQLBatch(t, conn, `
CREATE PROCEDURE AddWithDefault
    @a INT,
    @b INT = 42,
    @sum INT OUTPUT
AS
BEGIN
    SET @sum = @a + @b;
END;
`)
}

func TestTDS_RPCDirectProc_OutputParamReturnValue(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)
	setupOutputProc(t, conn)

	// All parameters supplied by name: @sum = @a + @b = 5 + 7 = 12
	var params bytes.Buffer
	params.Write(buildIntParam("@a", false, 5))
	params.Write(buildIntParam("@b", false, 7))
	params.Write(buildIntParam("@sum", true, 0))
	resp := sendNamedRPC(t, conn, "AddWithDefault", params.Bytes())

	if !bytes.Contains(resp, []byte{TokenDoneProc}) {
		t.Fatalf("expected DONEPROC (0xFE), got: %x", resp)
	}
	rv := findIntReturnValue(t, resp)
	if rv.name != "@sum" || rv.ordinal != 2 || rv.size != 4 || rv.isNull || rv.value != 12 {
		t.Fatalf("unexpected RETURNVALUE: %+v", rv)
	}
}

func TestTDS_RPCDirectProc_OmittedParamUsesDeclaredDefault(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)
	setupOutputProc(t, conn)

	// @b omitted: declared default 42 applies, @sum = 8 + 42 = 50
	var params bytes.Buffer
	params.Write(buildIntParam("@a", false, 8))
	params.Write(buildIntParam("@sum", true, 0))
	resp := sendNamedRPC(t, conn, "AddWithDefault", params.Bytes())

	rv := findIntReturnValue(t, resp)
	if rv.name != "@sum" || rv.ordinal != 1 || rv.value != 50 {
		t.Fatalf("expected @sum=50 using default @b=42, got %+v", rv)
	}
}

func TestTDS_RPCDirectProc_PositionalParams(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)
	setupOutputProc(t, conn)

	// Unnamed positional parameters bind by declaration order: (@a, @b, @sum)
	var params bytes.Buffer
	params.Write(buildIntParam("", false, 1))
	params.Write(buildIntParam("", false, 2))
	params.Write(buildIntParam("", true, 0))
	resp := sendNamedRPC(t, conn, "AddWithDefault", params.Bytes())

	rv := findIntReturnValue(t, resp)
	if rv.ordinal != 2 || rv.value != 3 {
		t.Fatalf("expected positional @sum=3, got %+v", rv)
	}
}

func TestTDS_RPCDirectProc_NoOutputParamEmitsNoReturnValue(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)
	_ = sendSQLBatch(t, conn, `CREATE PROCEDURE NoOut @a INT AS BEGIN SET @a = @a + 1; END;`)

	var params bytes.Buffer
	params.Write(buildIntParam("@a", false, 1))
	resp := sendNamedRPC(t, conn, "NoOut", params.Bytes())

	if bytes.Contains(resp, []byte{TokenReturnValue}) {
		t.Fatalf("unexpected RETURNVALUE for call without OUTPUT parameters: %x", resp)
	}
}

// findReturnStatus locates the TokenReturnStatus (0x79) token in the response stream and returns its int32 value and offset.
func findReturnStatus(t *testing.T, resp []byte) (int32, int) {
	t.Helper()
	for i := 0; i <= len(resp)-5; i++ {
		if resp[i] == TokenReturnStatus {
			val := int32(binary.LittleEndian.Uint32(resp[i+1 : i+5]))
			return val, i
		}
	}
	t.Fatalf("no RETURNSTATUS (0x79) token found in response: %x", resp)
	return 0, -1
}

// findTokenOffset returns the byte index of the first occurrence of token.
func findTokenOffset(resp []byte, token byte) int {
	for i, b := range resp {
		if b == token {
			return i
		}
	}
	return -1
}

func TestTDS_RPCDirectProc_ReturnStatus(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)
	_ = sendSQLBatch(t, conn, `
CREATE PROCEDURE GetAnswer AS
BEGIN
    RETURN 42;
END;
`)

	resp := sendNamedRPC(t, conn, "GetAnswer", nil)
	val, offset := findReturnStatus(t, resp)
	if val != 42 {
		t.Fatalf("expected RETURNSTATUS=42, got %d", val)
	}
	doneOffset := findTokenOffset(resp, TokenDoneProc)
	if doneOffset < 0 || offset >= doneOffset {
		t.Fatalf("expected RETURNSTATUS (offset %d) to precede DONEPROC (offset %d)", offset, doneOffset)
	}
}

func TestTDS_RPCDirectProc_ReturnStatusBeforeReturnValue(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)
	_ = sendSQLBatch(t, conn, `
CREATE PROCEDURE AddAndReturn
    @a INT,
    @b INT,
    @sum INT OUTPUT
AS
BEGIN
    SET @sum = @a + @b;
    RETURN 99;
END;
`)

	var params bytes.Buffer
	params.Write(buildIntParam("@a", false, 5))
	params.Write(buildIntParam("@b", false, 7))
	params.Write(buildIntParam("@sum", true, 0))
	resp := sendNamedRPC(t, conn, "AddAndReturn", params.Bytes())

	val, statusOffset := findReturnStatus(t, resp)
	if val != 99 {
		t.Fatalf("expected RETURNSTATUS=99, got %d", val)
	}

	rv := findIntReturnValue(t, resp)
	if rv.value != 12 {
		t.Fatalf("expected @sum=12, got %d", rv.value)
	}

	rvOffset := findTokenOffset(resp, TokenReturnValue)
	doneOffset := findTokenOffset(resp, TokenDoneProc)

	if statusOffset >= rvOffset {
		t.Fatalf("ordering violation: RETURNSTATUS (offset %d) must precede RETURNVALUE (offset %d)", statusOffset, rvOffset)
	}
	if rvOffset >= doneOffset {
		t.Fatalf("ordering violation: RETURNVALUE (offset %d) must precede DONEPROC (offset %d)", rvOffset, doneOffset)
	}
}

func TestTDS_RPCDirectProc_ReturnStatusWithResultSet(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)
	_ = sendSQLBatch(t, conn, `
CREATE PROCEDURE SelectAndReturn AS
BEGIN
    SELECT 100 AS val;
    RETURN 77;
END;
`)

	resp := sendNamedRPC(t, conn, "SelectAndReturn", nil)
	val, statusOffset := findReturnStatus(t, resp)
	if val != 77 {
		t.Fatalf("expected RETURNSTATUS=77, got %d", val)
	}

	colOffset := findTokenOffset(resp, TokenColMetaData)
	doneInProcOffset := findTokenOffset(resp, TokenDoneInProc)
	doneOffset := findTokenOffset(resp, TokenDoneProc)

	if colOffset < 0 || doneInProcOffset < 0 || doneOffset < 0 {
		t.Fatalf("missing tokens: col=%d doneInProc=%d done=%d", colOffset, doneInProcOffset, doneOffset)
	}
	if !(colOffset < doneInProcOffset && doneInProcOffset < statusOffset && statusOffset < doneOffset) {
		t.Fatalf("ordering violation: COL(%d) < DONEINPROC(%d) < RETURNSTATUS(%d) < DONEPROC(%d)",
			colOffset, doneInProcOffset, statusOffset, doneOffset)
	}
}

func TestTDS_RPCDirectProc_DefaultReturnStatusZero(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)
	_ = sendSQLBatch(t, conn, `
CREATE PROCEDURE NoExplicitReturn AS
BEGIN
    DECLARE @x INT = 1;
END;
`)

	resp := sendNamedRPC(t, conn, "NoExplicitReturn", nil)
	val, _ := findReturnStatus(t, resp)
	if val != 0 {
		t.Fatalf("expected default RETURNSTATUS=0, got %d", val)
	}
}

func TestTDS_RPCTextFallback_ReturnStatus(t *testing.T) {
	srv := setupTestServer(t)
	conn := connectClient(t, srv)
	performHandshake(t, conn)
	_ = sendSQLBatch(t, conn, `
CREATE PROCEDURE FallbackProc AS
BEGIN
    RETURN 1234;
END;
`)

	// Invoke via sp_executesql (which routes through executeRPCAndRespond)
	var params bytes.Buffer
	params.Write(buildNVarCharParam("@stmt", false, "EXEC FallbackProc;"))
	resp := sendRPC(t, conn, 10, 0, params.Bytes())

	val, statusOffset := findReturnStatus(t, resp)
	if val != 1234 {
		t.Fatalf("expected RETURNSTATUS=1234 from sp_executesql fallback, got %d", val)
	}
	doneOffset := findTokenOffset(resp, TokenDoneProc)
	if doneOffset < 0 || statusOffset >= doneOffset {
		t.Fatalf("expected RETURNSTATUS (offset %d) to precede DONEPROC (offset %d)", statusOffset, doneOffset)
	}
}

func TestRegression_AUDIT_093_019_StorageValueNullSerialization(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "id", Type: TypeIntN, Size: 4, Nullable: true},
		{Name: "name", Type: TypeNVarChar, Size: 100, Nullable: true},
	}
	rowWithTypedNull := map[string]any{
		"id":   1,
		"name": unwrapTdsOutputValue(nil),
	}
	// Verify isNilTdsValue handles nil and typed storage.Value{IsNull: true}
	var buf bytes.Buffer
	WriteRowDirect(&buf, cols, map[string]any{
		"id":   1,
		"name": nil,
	})
	expectedNilBytes := append([]byte(nil), buf.Bytes()...)

	// Compare with storage.NullValue(storage.TypeVarChar)
	buf.Reset()
	WriteRowDirect(&buf, cols, map[string]any{
		"id":   storage.IntValue(1),
		"name": storage.NullValue(storage.TypeVarChar),
	})
	if !bytes.Equal(buf.Bytes(), expectedNilBytes) {
		t.Fatalf("expected storage.Value{IsNull: true} to serialize identically to nil in ROW token: got %x, want %x", buf.Bytes(), expectedNilBytes)
	}
	nbcrow1 := BuildNBCRow(cols, []any{1, nil})
	nbcrow2 := BuildNBCRow(cols, []any{storage.IntValue(1), storage.NullValue(storage.TypeVarChar)})
	if !bytes.Equal(nbcrow1, nbcrow2) {
		t.Fatalf("expected storage.Value{IsNull: true} to serialize identically to nil in NBCROW token: got %x, want %x", nbcrow2, nbcrow1)
	}

	// 1. Verify formatSQLValue with storage.Value and typed pointers
	if got := formatSQLValue(storage.NullValue(storage.TypeInt)); got != "NULL" {
		t.Fatalf("expected formatSQLValue(NullValue) == 'NULL', got %q", got)
	}
	if got := formatSQLValue((*storage.Value)(nil)); got != "NULL" {
		t.Fatalf("expected formatSQLValue((*storage.Value)(nil)) == 'NULL', got %q", got)
	}
	if got := formatSQLValue(storage.IntValue(42)); got != "42" {
		t.Fatalf("expected formatSQLValue(IntValue(42)) == '42', got %q", got)
	}
	if got := formatSQLValue(storage.StringValue("hello")); got != "'hello'" {
		t.Fatalf("expected formatSQLValue(StringValue('hello')) == ''hello'', got %q", got)
	}

	// 2. Verify inferSQLType with storage.Value
	if got := inferSQLType(storage.NullValue(storage.TypeInt)); got != "NVARCHAR(MAX)" {
		t.Fatalf("expected inferSQLType(NullValue) == 'NVARCHAR(MAX)', got %q", got)
	}
	if got := inferSQLType(storage.IntValue(42)); got != "BIGINT" {
		t.Fatalf("expected inferSQLType(IntValue(42)) == 'BIGINT', got %q", got)
	}

	// 3. Verify outputColumnInfo with raw storage.Value
	pInt := RPCParam{Name: "@val", Type: TypeInt4}
	colIntNull := outputColumnInfo(pInt, storage.NullValue(storage.TypeInt))
	if colIntNull.Type != TypeIntN {
		t.Fatalf("expected outputColumnInfo(NullValue) Type == TypeIntN, got %v", colIntNull.Type)
	}
	colIntVal := outputColumnInfo(pInt, storage.IntValue(99))
	if colIntVal.Type != TypeIntN {
		t.Fatalf("expected outputColumnInfo(IntValue) Type == TypeIntN, got %v", colIntVal.Type)
	}

	// 4. Verify WriteReturnValue with storage.NullValue and pointer
	var retBuf1, retBuf2, retBuf3 bytes.Buffer
	colNVar := ColumnInfo{Name: "@ret", Type: TypeNVarChar, Size: 100, Nullable: true}
	WriteReturnValue(&retBuf1, 1, "@ret", colNVar, nil)
	WriteReturnValue(&retBuf2, 1, "@ret", colNVar, storage.NullValue(storage.TypeVarChar))
	nullVal := storage.NullValue(storage.TypeVarChar)
	WriteReturnValue(&retBuf3, 1, "@ret", colNVar, &nullVal)
	if !bytes.Equal(retBuf1.Bytes(), retBuf2.Bytes()) {
		t.Fatalf("expected WriteReturnValue with storage.NullValue to match nil: got %x, want %x", retBuf2.Bytes(), retBuf1.Bytes())
	}
	if !bytes.Equal(retBuf1.Bytes(), retBuf3.Bytes()) {
		t.Fatalf("expected WriteReturnValue with *storage.NullValue to match nil: got %x, want %x", retBuf3.Bytes(), retBuf1.Bytes())
	}
	_ = rowWithTypedNull
}
