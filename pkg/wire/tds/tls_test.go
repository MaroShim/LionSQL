package tds

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/wire"
)

// setupTLSTestServer initializes an in-memory storage engine and a TDS server with TLS configured.
func setupTLSTestServer(t *testing.T, requireSSL bool) *Server {
	t.Helper()
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	cat := catalog.New(eng)

	server := NewServer("127.0.0.1:0", cat, eng)

	tlsCfg, err := wire.NewSelfSignedTLSConfig()
	if err != nil {
		t.Fatalf("failed to generate self-signed TLS config: %v", err)
	}
	server.SetTLSConfig(tlsCfg)
	server.SetRequireSSL(requireSSL)

	return server
}

func connectTLSClient(t *testing.T, srv *Server) net.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	go srv.handleConnection(serverConn)
	t.Cleanup(func() {
		_ = clientConn.Close()
	})
	return clientConn
}

// performTDS7TLSHandshake executes a complete TDS 7.x PRELOGIN negotiation, encapsulated TLS handshake, and LOGIN7.
func performTDS7TLSHandshake(t *testing.T, conn net.Conn, clientEncrypt byte) net.Conn {
	t.Helper()

	// 1. Send Prelogin with client's requested encryption option
	preloginPayload := BuildPreloginResponse(clientEncrypt)
	if err := WritePacket(conn, PacketPrelogin, 0, preloginPayload); err != nil {
		t.Fatalf("failed to send Prelogin packet: %v", err)
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
		t.Fatalf("expected EncryptOn or EncryptReq from server, got 0x%02X", serverEncryptOption)
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

	// 4. Send Login7 over encrypted connection
	var loginBuf bytes.Buffer
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	_ = binary.Write(&loginBuf, binary.BigEndian, uint32(0x74000004)) // TDS 7.4
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(4096))
	_ = binary.Write(&loginBuf, binary.BigEndian, uint32(0x00000001))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1234))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	loginBuf.WriteByte(0x20)
	loginBuf.WriteByte(0x03)
	loginBuf.WriteByte(0x00)
	loginBuf.WriteByte(0x00)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))

	headerOffsets := make([]byte, 50)
	loginBuf.Write(headerOffsets)

	hostBytes := EncodeUTF16LE("localhost")
	userBytes := EncodeUTF16LE("sa")
	dbBytes := EncodeUTF16LE("master")

	dataStart := uint16(loginBuf.Len())
	offset := dataStart

	binary.LittleEndian.PutUint16(loginBuf.Bytes()[36:38], offset)
	binary.LittleEndian.PutUint16(loginBuf.Bytes()[38:40], uint16(len("localhost")))
	offset += uint16(len(hostBytes))

	binary.LittleEndian.PutUint16(loginBuf.Bytes()[40:42], offset)
	binary.LittleEndian.PutUint16(loginBuf.Bytes()[42:44], uint16(len("sa")))
	offset += uint16(len(userBytes))

	binary.LittleEndian.PutUint16(loginBuf.Bytes()[64:66], offset)
	binary.LittleEndian.PutUint16(loginBuf.Bytes()[66:68], uint16(len("master")))

	loginBuf.Write(hostBytes)
	loginBuf.Write(userBytes)
	loginBuf.Write(dbBytes)

	totalLen := uint32(loginBuf.Len())
	binary.LittleEndian.PutUint32(loginBuf.Bytes()[:4], totalLen)

	if err := WritePacket(tlsClient, PacketLogin7, 0, loginBuf.Bytes()); err != nil {
		t.Fatalf("failed to send Login7 packet over TLS: %v", err)
	}

	// 5. Read Login7 response over TLS
	loginResp, err := ReadPacket(tlsClient)
	if err != nil {
		t.Fatalf("failed to read Login7 response over TLS: %v", err)
	}
	if loginResp.Type != PacketTabular {
		t.Fatalf("expected PacketTabular (0x04) in login response, got 0x%02X", loginResp.Type)
	}

	return tlsClient
}

func TestTDS_TLS7_EncryptedSession(t *testing.T) {
	server := setupTLSTestServer(t, false)
	defer server.Close()

	conn := connectTLSClient(t, server)
	tlsConn := performTDS7TLSHandshake(t, conn, EncryptOn)

	// Send a SQL batch over TLS: SELECT 42 AS val
	query := "SELECT 42 AS val"
	u16Query := EncodeUTF16LE(query)

	var batchPayload bytes.Buffer
	_ = binary.Write(&batchPayload, binary.LittleEndian, uint32(len(u16Query)+22))
	_ = binary.Write(&batchPayload, binary.LittleEndian, uint32(18))
	_ = binary.Write(&batchPayload, binary.LittleEndian, uint16(0x0002))
	_ = binary.Write(&batchPayload, binary.LittleEndian, uint64(0))
	_ = binary.Write(&batchPayload, binary.LittleEndian, uint32(1))
	batchPayload.Write(u16Query)

	if err := WritePacket(tlsConn, PacketSQLBatch, 1, batchPayload.Bytes()); err != nil {
		t.Fatalf("failed to send SQL batch over TLS: %v", err)
	}

	resp, err := ReadPacket(tlsConn)
	if err != nil {
		t.Fatalf("failed to read query response over TLS: %v", err)
	}
	if resp.Type != PacketTabular {
		t.Fatalf("expected PacketTabular response, got 0x%02X", resp.Type)
	}
	if len(resp.Payload) == 0 {
		t.Fatalf("empty query response payload over TLS")
	}

	// Verify the payload contains TokenDone (0xFD) or ColMetaData
	if !bytes.Contains(resp.Payload, []byte{TokenColMetaData}) && !bytes.Contains(resp.Payload, []byte{TokenDone}) {
		t.Fatalf("expected ColMetaData or Done token in response over TLS")
	}
}

func TestTDS_TLS8_DirectTLSSession(t *testing.T) {
	server := setupTLSTestServer(t, false)
	defer server.Close()

	// Direct TLS connection (TDS 8.0 style: first bytes are TLS ClientHello)
	clientConn := connectTLSClient(t, server)
	tlsClient := tls.Client(clientConn, &tls.Config{
		InsecureSkipVerify: true,
	})

	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("direct TLS handshake failed: %v", err)
	}

	// In TDS 8.0, client sends PRELOGIN over the established TLS tunnel
	preloginPayload := BuildPreloginResponse(EncryptOn)
	if err := WritePacket(tlsClient, PacketPrelogin, 0, preloginPayload); err != nil {
		t.Fatalf("failed to send Prelogin over direct TLS: %v", err)
	}

	respPkt, err := ReadPacket(tlsClient)
	if err != nil {
		t.Fatalf("failed to read Prelogin response over direct TLS: %v", err)
	}
	if respPkt.Type != PacketTabular {
		t.Fatalf("expected PacketTabular in prelogin response, got 0x%02X", respPkt.Type)
	}

	opts := ParsePrelogin(respPkt.Payload)
	if enc, ok := opts[PreloginOptionEncryption]; !ok || len(enc) == 0 || enc[0] != EncryptOn {
		t.Fatalf("expected EncryptOn in TDS 8.0 Prelogin response")
	}

	// Send Login7 over direct TLS
	var loginBuf bytes.Buffer
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	_ = binary.Write(&loginBuf, binary.BigEndian, uint32(0x74000004))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(4096))
	_ = binary.Write(&loginBuf, binary.BigEndian, uint32(0x00000001))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(1234))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	loginBuf.WriteByte(0x20)
	loginBuf.WriteByte(0x03)
	loginBuf.WriteByte(0x00)
	loginBuf.WriteByte(0x00)
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0))

	headerOffsets := make([]byte, 50)
	loginBuf.Write(headerOffsets)
	binary.LittleEndian.PutUint32(loginBuf.Bytes()[:4], uint32(loginBuf.Len()))

	if err := WritePacket(tlsClient, PacketLogin7, 0, loginBuf.Bytes()); err != nil {
		t.Fatalf("failed to send Login7 packet over direct TLS: %v", err)
	}

	loginResp, err := ReadPacket(tlsClient)
	if err != nil {
		t.Fatalf("failed to read Login7 response over direct TLS: %v", err)
	}
	if loginResp.Type != PacketTabular {
		t.Fatalf("expected PacketTabular in login response, got 0x%02X", loginResp.Type)
	}
}

func TestTDS_RequireSSL_RejectsPlaintext(t *testing.T) {
	// Server with requireSSL = true
	server := setupTLSTestServer(t, true)
	defer server.Close()

	conn := connectTLSClient(t, server)

	// Client requests EncryptNotSup (plaintext only)
	preloginPayload := BuildPreloginResponse(EncryptNotSup)
	if err := WritePacket(conn, PacketPrelogin, 0, preloginPayload); err != nil {
		t.Fatalf("failed to send Prelogin: %v", err)
	}

	// Server must close or reject connection
	respPkt, err := ReadPacket(conn)
	if err == nil && respPkt != nil {
		// If a response was sent, verify it does not allow plaintext
		opts := ParsePrelogin(respPkt.Payload)
		if enc, ok := opts[PreloginOptionEncryption]; ok && len(enc) > 0 {
			if enc[0] == EncryptNotSup || enc[0] == EncryptOff {
				t.Fatalf("server permitted plaintext when requireSSL is enabled")
			}
		}
	}
}

func TestTDS_TLS_ConcurrentClients(t *testing.T) {
	server := setupTLSTestServer(t, false)
	defer server.Close()

	const numWorkers = 8
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()

			if workerID%2 == 0 {
				// TDS 7.x TLS Handshake
				conn := connectTLSClient(t, server)
				tlsConn := performTDS7TLSHandshake(t, conn, EncryptOn)
				query := fmt.Sprintf("SELECT %d AS id", workerID)
				u16Query := EncodeUTF16LE(query)

				var batchPayload bytes.Buffer
				_ = binary.Write(&batchPayload, binary.LittleEndian, uint32(len(u16Query)+22))
				_ = binary.Write(&batchPayload, binary.LittleEndian, uint32(18))
				_ = binary.Write(&batchPayload, binary.LittleEndian, uint16(0x0002))
				_ = binary.Write(&batchPayload, binary.LittleEndian, uint64(0))
				_ = binary.Write(&batchPayload, binary.LittleEndian, uint32(1))
				batchPayload.Write(u16Query)

				if err := WritePacket(tlsConn, PacketSQLBatch, uint16(workerID+1), batchPayload.Bytes()); err != nil {
					t.Errorf("worker %d send batch failed: %v", workerID, err)
					return
				}

				resp, err := ReadPacket(tlsConn)
				if err != nil {
					t.Errorf("worker %d read response failed: %v", workerID, err)
					return
				}
				if len(resp.Payload) == 0 {
					t.Errorf("worker %d empty payload", workerID)
				}
			} else {
				// TDS 8.0 Direct TLS
				clientConn := connectTLSClient(t, server)
				tlsClient := tls.Client(clientConn, &tls.Config{InsecureSkipVerify: true})
				if err := tlsClient.Handshake(); err != nil {
					t.Errorf("worker %d direct TLS handshake failed: %v", workerID, err)
					return
				}

				preloginPayload := BuildPreloginResponse(EncryptOn)
				_ = WritePacket(tlsClient, PacketPrelogin, 0, preloginPayload)
				_, _ = ReadPacket(tlsClient)
			}
		}(i)
	}

	wg.Wait()
}
