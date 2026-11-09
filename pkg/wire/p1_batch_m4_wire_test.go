package wire

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
)

// TestP1_BatchM4_WirePortalLimit tests Defect 5-4 / Scope-097 portal capacity limit.
func TestP1_BatchM4_WirePortalLimit(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)
	defer eng.Close()
	defer srv.Close()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// Startup handshake
	startupBuf := make([]byte, 8)
	binary.BigEndian.PutUint32(startupBuf[:4], 8)
	binary.BigEndian.PutUint32(startupBuf[4:8], 196608)
	_, _ = clientConn.Write(startupBuf)

	for {
		header := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, header)
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		_, _ = io.ReadFull(clientConn, payload)
		if header[0] == MsgReadyForQuery {
			break
		}
	}

	// 1. Send Parse ('P'): stmt="stmt_test", query="SELECT 1;"
	query := "SELECT 1;\x00"
	pPayload := append([]byte("stmt_test\x00"), query...)
	pPayload = append(pPayload, 0, 0) // 0 param types
	pMsg := make([]byte, 5+len(pPayload))
	pMsg[0] = 'P'
	binary.BigEndian.PutUint32(pMsg[1:5], uint32(4+len(pPayload)))
	copy(pMsg[5:], pPayload)
	_, _ = clientConn.Write(pMsg)

	// Read ParseComplete ('1')
	pRespHeader := make([]byte, 5)
	_, _ = io.ReadFull(clientConn, pRespHeader)
	if pRespHeader[0] != '1' {
		t.Fatalf("expected ParseComplete ('1'), got %c", pRespHeader[0])
	}

	// 2. Bind 1025 distinct portals.
	// Currently (Scope-097 bug), all 1025 succeed with BindComplete ('2') without cap enforcement.
	for i := 1; i <= 1024; i++ {
		pName := fmt.Sprintf("portal_%d\x00", i)
		bPayload := append([]byte(pName), []byte("stmt_test\x00")...)
		bPayload = append(bPayload, 0, 0) // 0 format codes
		bPayload = append(bPayload, 0, 0) // 0 param values
		bPayload = append(bPayload, 0, 0) // 0 result format codes

		bMsg := make([]byte, 5+len(bPayload))
		bMsg[0] = 'B'
		binary.BigEndian.PutUint32(bMsg[1:5], uint32(4+len(bPayload)))
		copy(bMsg[5:], bPayload)
		_, _ = clientConn.Write(bMsg)

		respHeader := make([]byte, 5)
		_, _ = io.ReadFull(clientConn, respHeader)
		if respHeader[0] != '2' {
			t.Fatalf("expected BindComplete ('2') for portal %d, got %c", i, respHeader[0])
		}
	}

	// The 1025th portal should exceed MaxSessionPortals (1024) and fail with ErrorResponse ('E')
	pName1025 := "portal_1025\x00"
	bPayload := append([]byte(pName1025), []byte("stmt_test\x00")...)
	bPayload = append(bPayload, 0, 0)
	bPayload = append(bPayload, 0, 0)
	bPayload = append(bPayload, 0, 0)
	bMsg := make([]byte, 5+len(bPayload))
	bMsg[0] = 'B'
	binary.BigEndian.PutUint32(bMsg[1:5], uint32(4+len(bPayload)))
	copy(bMsg[5:], bPayload)
	_, _ = clientConn.Write(bMsg)

	respHeader := make([]byte, 5)
	_, _ = io.ReadFull(clientConn, respHeader)
	if respHeader[0] != 'E' {
		t.Fatalf("expected ErrorResponse ('E') for 1025th portal exceeding cap, got %c", respHeader[0])
	}
}
