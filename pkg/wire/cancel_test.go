package wire

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestWire_RemoteCancelRequest_ActiveQueryAborted(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)
	defer eng.Close()
	defer srv.Close()

	// Create a procedure that loops until cancelled
	procDDL := `CREATE PROCEDURE sp_cancel_loop AS
BEGIN
	DECLARE @i INT = 0;
	WHILE @i < 100000000
	BEGIN
		SET @i = @i + 1;
	END;
END;`
	if _, err := srv.coordinator.ExecuteSQL(procDDL); err != nil {
		t.Fatalf("Failed to create loop procedure: %v", err)
	}

	// 1. Establish regular client connection
	c1Client, c1Server := net.Pipe()
	defer c1Client.Close()
	defer c1Server.Close()

	go srv.handleConnection(c1Server)

	// Send StartupMessage
	startup := make([]byte, 8)
	binary.BigEndian.PutUint32(startup[:4], 8)
	binary.BigEndian.PutUint32(startup[4:8], 196608) // 3.0
	if _, err := c1Client.Write(startup); err != nil {
		t.Fatalf("Failed to write startup: %v", err)
	}

	var gotBackendKeyData bool
	var pid, secretKey uint32
	header := make([]byte, 5)

	// Read until ReadyForQuery
	for {
		if _, err := io.ReadFull(c1Client, header); err != nil {
			t.Fatalf("Failed to read header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(c1Client, payload); err != nil {
			t.Fatalf("Failed to read payload: %v", err)
		}

		if msgType == MsgBackendKeyData {
			gotBackendKeyData = true
			pid = binary.BigEndian.Uint32(payload[:4])
			secretKey = binary.BigEndian.Uint32(payload[4:8])
		} else if msgType == MsgReadyForQuery {
			break
		}
	}

	if !gotBackendKeyData || pid == 0 || secretKey == 0 {
		t.Fatalf("Failed to receive BackendKeyData: pid=%d, secretKey=%d", pid, secretKey)
	}

	// 2. Start the long-running procedure on client connection in a separate goroutine
	queryErrCh := make(chan error, 1)
	queryDoneCh := make(chan struct{})
	var gotErrorCode string
	var gotErrorMessage string

	go func() {
		// Send Query ('Q') message: EXEC sp_cancel_loop;
		queryStr := "EXEC sp_cancel_loop;\x00"
		qMsg := make([]byte, 5+len(queryStr))
		qMsg[0] = MsgSimpleQuery
		binary.BigEndian.PutUint32(qMsg[1:5], uint32(4+len(queryStr)))
		copy(qMsg[5:], []byte(queryStr))

		if _, err := c1Client.Write(qMsg); err != nil {
			queryErrCh <- err
			return
		}

		// Read responses from server
		h := make([]byte, 5)
		for {
			if _, err := io.ReadFull(c1Client, h); err != nil {
				queryErrCh <- err
				return
			}
			mType := h[0]
			mLen := binary.BigEndian.Uint32(h[1:5])
			p := make([]byte, mLen-4)
			if _, err := io.ReadFull(c1Client, p); err != nil {
				queryErrCh <- err
				return
			}

			if mType == MsgErrorResponse {
				// Parse error response fields
				parts := strings.Split(string(p), "\x00")
				for _, part := range parts {
					if len(part) > 1 {
						if part[0] == 'C' {
							gotErrorCode = part[1:]
						} else if part[0] == 'M' {
							gotErrorMessage = part[1:]
						}
					}
				}
			} else if mType == MsgReadyForQuery {
				close(queryDoneCh)
				return
			}
		}
	}()

	// Give the query a brief moment to start running
	time.Sleep(30 * time.Millisecond)

	// 3. Connect via a secondary connection and send CancelRequest (80877102)
	cancelClient, cancelServer := net.Pipe()
	defer cancelClient.Close()
	defer cancelServer.Close()

	go srv.handleConnection(cancelServer)

	cancelBuf := make([]byte, 16)
	binary.BigEndian.PutUint32(cancelBuf[0:4], 16)
	binary.BigEndian.PutUint32(cancelBuf[4:8], 80877102) // CancelRequest code
	binary.BigEndian.PutUint32(cancelBuf[8:12], pid)
	binary.BigEndian.PutUint32(cancelBuf[12:16], secretKey)

	if _, err := cancelClient.Write(cancelBuf); err != nil {
		t.Fatalf("Failed to write cancel request: %v", err)
	}

	// Server must close the cancel connection immediately with EOF
	readBuf := make([]byte, 1)
	n, err := cancelClient.Read(readBuf)
	if err != io.EOF && n != 0 {
		t.Errorf("Expected EOF from CancelRequest connection, got n=%d, err=%v", n, err)
	}

	// 4. Wait for query to complete on client connection
	select {
	case <-queryDoneCh:
		// Success!
	case err := <-queryErrCh:
		t.Fatalf("Client received error during query read: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatalf("Query did not abort within 2s after CancelRequest")
	}

	// 5. Verify error code is 57014 (query_canceled) and message contains cancellation
	if gotErrorCode != "57014" {
		t.Errorf("Expected error code 57014 (query_canceled), got %q", gotErrorCode)
	}
	if !strings.Contains(strings.ToLower(gotErrorMessage), "cancel") {
		t.Errorf("Expected cancellation error message, got %q", gotErrorMessage)
	}

	// 6. Verify client connection is still healthy and can execute subsequent queries
	pingQuery := "SELECT 1;\x00"
	pMsg := make([]byte, 5+len(pingQuery))
	pMsg[0] = MsgSimpleQuery
	binary.BigEndian.PutUint32(pMsg[1:5], uint32(4+len(pingQuery)))
	copy(pMsg[5:], []byte(pingQuery))

	if _, err := c1Client.Write(pMsg); err != nil {
		t.Fatalf("Failed to write ping query after cancel: %v", err)
	}

	gotDataRow := false
	gotReady := false
	for !gotReady {
		if _, err := io.ReadFull(c1Client, header); err != nil {
			t.Fatalf("Failed to read header for ping query: %v", err)
		}
		mType := header[0]
		mLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, mLen-4)
		if _, err := io.ReadFull(c1Client, payload); err != nil {
			t.Fatalf("Failed to read payload for ping query: %v", err)
		}
		if mType == MsgDataRow {
			gotDataRow = true
		} else if mType == MsgReadyForQuery {
			gotReady = true
		}
	}

	if !gotDataRow {
		t.Errorf("Expected DataRow for SELECT 1 after cancellation")
	}
}

func TestWire_RemoteCancelRequest_WrongKeyIgnored(t *testing.T) {
	srv, eng, _ := setupTestWireServer(t)
	defer eng.Close()
	defer srv.Close()

	c1Client, c1Server := net.Pipe()
	defer c1Client.Close()
	defer c1Server.Close()

	go srv.handleConnection(c1Server)

	// Send StartupMessage
	startup := make([]byte, 8)
	binary.BigEndian.PutUint32(startup[:4], 8)
	binary.BigEndian.PutUint32(startup[4:8], 196608) // 3.0
	if _, err := c1Client.Write(startup); err != nil {
		t.Fatalf("Failed to write startup: %v", err)
	}

	var pid, secretKey uint32
	header := make([]byte, 5)
	for {
		if _, err := io.ReadFull(c1Client, header); err != nil {
			t.Fatalf("Failed to read header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(c1Client, payload); err != nil {
			t.Fatalf("Failed to read payload: %v", err)
		}
		if msgType == MsgBackendKeyData {
			pid = binary.BigEndian.Uint32(payload[:4])
			secretKey = binary.BigEndian.Uint32(payload[4:8])
		} else if msgType == MsgReadyForQuery {
			break
		}
	}

	// Send CancelRequest with WRONG secret key
	cancelClient, cancelServer := net.Pipe()
	defer cancelClient.Close()
	defer cancelServer.Close()

	go srv.handleConnection(cancelServer)

	cancelBuf := make([]byte, 16)
	binary.BigEndian.PutUint32(cancelBuf[0:4], 16)
	binary.BigEndian.PutUint32(cancelBuf[4:8], 80877102)
	binary.BigEndian.PutUint32(cancelBuf[8:12], pid)
	binary.BigEndian.PutUint32(cancelBuf[12:16], secretKey+999) // Wrong secret key

	if _, err := cancelClient.Write(cancelBuf); err != nil {
		t.Fatalf("Failed to write cancel request: %v", err)
	}

	// Connection must close with EOF
	readBuf := make([]byte, 1)
	_, _ = cancelClient.Read(readBuf)

	// Verify target session query is NOT cancelled
	targetCS := srv.coordinator.GetSession(srv.activeConns[pid].sessID)
	if targetCS != nil && targetCS.CancelRequested {
		t.Errorf("Session should NOT be marked cancelled when wrong secret key is provided")
	}
}
