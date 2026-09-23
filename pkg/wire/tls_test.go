package wire

import (
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestTLS_SelfSignedGeneration(t *testing.T) {
	cfg, err := NewSelfSignedTLSConfig()
	if err != nil {
		t.Fatalf("NewSelfSignedTLSConfig failed: %v", err)
	}
	if len(cfg.Certificates) == 0 {
		t.Fatalf("expected certificates in TLS config")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion TLS 1.2, got %v", cfg.MinVersion)
	}
}

func TestTLS_SSLRequest_Disabled(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// Send SSLRequest (code 80877103)
	sslReq := make([]byte, 8)
	binary.BigEndian.PutUint32(sslReq[:4], 8)
	binary.BigEndian.PutUint32(sslReq[4:8], 80877103)
	if _, err := clientConn.Write(sslReq); err != nil {
		t.Fatalf("failed to send SSLRequest: %v", err)
	}

	resp := make([]byte, 1)
	if _, err := io.ReadFull(clientConn, resp); err != nil {
		t.Fatalf("failed to read SSL response: %v", err)
	}
	if resp[0] != 'N' {
		t.Fatalf("expected 'N' when SSL disabled, got %c", resp[0])
	}
}

func TestTLS_SSLRequest_Enabled(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)
	tlsCfg, err := NewSelfSignedTLSConfig()
	if err != nil {
		t.Fatalf("failed to create self-signed TLS config: %v", err)
	}
	srv.SetTLSConfig(tlsCfg)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// 1. Send SSLRequest
	sslReq := make([]byte, 8)
	binary.BigEndian.PutUint32(sslReq[:4], 8)
	binary.BigEndian.PutUint32(sslReq[4:8], 80877103)
	if _, err := clientConn.Write(sslReq); err != nil {
		t.Fatalf("failed to write SSLRequest: %v", err)
	}

	resp := make([]byte, 1)
	if _, err := io.ReadFull(clientConn, resp); err != nil {
		t.Fatalf("failed to read SSL response: %v", err)
	}
	if resp[0] != 'S' {
		t.Fatalf("expected 'S' when SSL enabled, got %c", resp[0])
	}

	// 2. Upgrade client connection to TLS
	tlsClient := tls.Client(clientConn, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("TLS client handshake failed: %v", err)
	}

	// 3. Send StartupMessage inside TLS
	startup := make([]byte, 8)
	binary.BigEndian.PutUint32(startup[:4], 8)
	binary.BigEndian.PutUint32(startup[4:8], 196608) // 3.0
	if _, err := tlsClient.Write(startup); err != nil {
		t.Fatalf("failed to write StartupMessage over TLS: %v", err)
	}

	// 4. Read server responses until ReadyForQuery ('Z')
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(tlsClient, header); err != nil {
			t.Fatalf("failed to read message header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(tlsClient, payload); err != nil {
			t.Fatalf("failed to read message payload: %v", err)
		}

		if msgType == MsgReadyForQuery {
			break
		}
	}

	// 5. Send Query over encrypted TLS stream
	query := "SELECT version();\x00"
	queryBuf := make([]byte, 1+4+len(query))
	queryBuf[0] = MsgSimpleQuery
	binary.BigEndian.PutUint32(queryBuf[1:5], uint32(4+len(query)))
	copy(queryBuf[5:], query)
	if _, err := tlsClient.Write(queryBuf); err != nil {
		t.Fatalf("failed to write query over TLS: %v", err)
	}

	// Read query results
	gotCommandComplete := false
	for {
		header := make([]byte, 5)
		if _, err := io.ReadFull(tlsClient, header); err != nil {
			t.Fatalf("failed to read query response header: %v", err)
		}
		msgType := header[0]
		msgLen := binary.BigEndian.Uint32(header[1:5])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(tlsClient, payload); err != nil {
			t.Fatalf("failed to read query response payload: %v", err)
		}

		if msgType == MsgCommandComplete {
			gotCommandComplete = true
		}
		if msgType == MsgReadyForQuery {
			break
		}
	}

	if !gotCommandComplete {
		t.Fatalf("expected CommandComplete over TLS query")
	}
}

func TestTLS_RequireSSL(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)
	tlsCfg, _ := NewSelfSignedTLSConfig()
	srv.SetTLSConfig(tlsCfg)
	srv.SetRequireSSL(true)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// Send plain StartupMessage directly without SSLRequest
	startup := make([]byte, 8)
	binary.BigEndian.PutUint32(startup[:4], 8)
	binary.BigEndian.PutUint32(startup[4:8], 196608)
	if _, err := clientConn.Write(startup); err != nil {
		t.Fatalf("failed to write StartupMessage: %v", err)
	}

	// Server should reply with ErrorResponse ('E')
	header := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, header); err != nil {
		t.Fatalf("failed to read error response: %v", err)
	}
	if header[0] != MsgErrorResponse {
		t.Fatalf("expected ErrorResponse ('E'), got %c", header[0])
	}
}

func TestTLS_GSSENCAndSSL(t *testing.T) {
	srv, _, _ := setupTestWireServer(t)
	tlsCfg, _ := NewSelfSignedTLSConfig()
	srv.SetTLSConfig(tlsCfg)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go srv.handleConnection(serverConn)

	// 1. Send GSSENCRequest (code 80877104)
	gssReq := make([]byte, 8)
	binary.BigEndian.PutUint32(gssReq[:4], 8)
	binary.BigEndian.PutUint32(gssReq[4:8], 80877104)
	if _, err := clientConn.Write(gssReq); err != nil {
		t.Fatalf("failed to write GSSENCRequest: %v", err)
	}

	resp := make([]byte, 1)
	if _, err := io.ReadFull(clientConn, resp); err != nil {
		t.Fatalf("failed to read GSS response: %v", err)
	}
	if resp[0] != 'N' {
		t.Fatalf("expected 'N' for GSS, got %c", resp[0])
	}

	// 2. Follow up with SSLRequest (code 80877103)
	sslReq := make([]byte, 8)
	binary.BigEndian.PutUint32(sslReq[:4], 8)
	binary.BigEndian.PutUint32(sslReq[4:8], 80877103)
	if _, err := clientConn.Write(sslReq); err != nil {
		t.Fatalf("failed to write SSLRequest: %v", err)
	}

	if _, err := io.ReadFull(clientConn, resp); err != nil {
		t.Fatalf("failed to read SSL response: %v", err)
	}
	if resp[0] != 'S' {
		t.Fatalf("expected 'S' for SSL, got %c", resp[0])
	}
}
