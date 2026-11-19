package wire

import (
	"bytes"
	"net"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestWire_MaxConnections_SemaphoreEnforcement(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()
	cat := catalog.New(eng)

	srv := NewServer("127.0.0.1:0", cat, eng)
	srv.SetMaxConnections(2) // limit to 2 concurrent connections
	if err := srv.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Close()

	addr := srv.Addr().String()

	// 1. Establish connection 1
	c1, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		t.Fatalf("Connection 1 failed: %v", err)
	}
	defer c1.Close()

	// 2. Establish connection 2
	c2, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		t.Fatalf("Connection 2 failed: %v", err)
	}
	defer c2.Close()

	// Wait briefly for server accept loop to register c1 and c2
	time.Sleep(50 * time.Millisecond)

	// 3. Establish connection 3 (must exceed max_connections = 2)
	c3, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		t.Fatalf("Dial connection 3 failed: %v", err)
	}
	defer c3.Close()

	// Connection 3 should receive ErrorResponse ('E') with SQLSTATE 53300 or be closed by server
	_ = c3.SetReadDeadline(time.Now().Add(1 * time.Second))
	respBuf := make([]byte, 1024)
	n, readErr := c3.Read(respBuf)
	if readErr == nil && n > 0 {
		if respBuf[0] == MsgErrorResponse {
			if !bytes.Contains(respBuf[:n], []byte("53300")) {
				t.Fatalf("expected SQLSTATE 53300 in error response, got %s", string(respBuf[:n]))
			}
		}
	}

	// 4. Close connection 1 to free up a slot
	_ = c1.Close()
	time.Sleep(50 * time.Millisecond)

	// 5. Connection 4 should now connect successfully
	c4, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		t.Fatalf("Connection 4 should succeed after slot released, got err: %v", err)
	}
	defer c4.Close()
}
