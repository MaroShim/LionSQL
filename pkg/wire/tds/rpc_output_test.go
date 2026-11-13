package tds

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
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
