package tds

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"testing"

	"sqlion/pkg/auth"
	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage/memengine"
)

func obfuscateTestPassword(pw string) []byte {
	u16 := EncodeUTF16LE(pw)
	out := make([]byte, len(u16))
	for i, b := range u16 {
		swapped := ((b >> 4) & 0x0F) | ((b << 4) & 0xF0)
		out[i] = swapped ^ 0xA5
	}
	return out
}

func sendTestLogin7(t *testing.T, conn net.Conn, userName, password string) *Packet {
	t.Helper()

	// 1. Send Prelogin
	preloginPayload := BuildPreloginResponse(EncryptNotSup)
	if err := WritePacket(conn, PacketPrelogin, 0, preloginPayload); err != nil {
		t.Fatalf("failed to send Prelogin packet: %v", err)
	}

	// Read Prelogin response
	respPkt, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read Prelogin response: %v", err)
	}
	if respPkt.Type != PacketTabular {
		t.Fatalf("expected PacketTabular (0x04) in prelogin response, got 0x%02X", respPkt.Type)
	}

	// 2. Build Login7
	var loginBuf bytes.Buffer
	_ = binary.Write(&loginBuf, binary.LittleEndian, uint32(0)) // placeholder
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

	appName := "sqlcmd"
	dbName := "master"
	headerLen := uint16(94)

	u16User := EncodeUTF16LE(userName)
	u16Pass := obfuscateTestPassword(password)
	u16App := EncodeUTF16LE(appName)
	u16DB := EncodeUTF16LE(dbName)

	ibUser := headerLen
	cchUser := uint16(len(userName))

	ibPass := ibUser + uint16(len(u16User))
	cchPass := uint16(len(password))

	ibApp := ibPass + uint16(len(u16Pass))
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
	_ = binary.Write(&loginBuf, binary.LittleEndian, ibPass)
	_ = binary.Write(&loginBuf, binary.LittleEndian, cchPass)
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
	loginBuf.Write(u16Pass)
	loginBuf.Write(u16App)
	loginBuf.Write(u16DB)

	totalLen := uint32(loginBuf.Len())
	rawLogin := loginBuf.Bytes()
	binary.LittleEndian.PutUint32(rawLogin[:4], totalLen)

	if err := WritePacket(conn, PacketLogin7, 0, rawLogin); err != nil {
		t.Fatalf("failed to send Login7 packet: %v", err)
	}

	loginResp, err := ReadPacket(conn)
	if err != nil {
		t.Fatalf("failed to read Login7 response: %v", err)
	}
	return loginResp
}

func TestTDS_AuthenticationEnforcement(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()
	cat := catalog.New(eng)

	authSvc := auth.NewAuthService(auth.Config{})
	_, _ = authSvc.EnsureDefaultSuperuser("admin@sqlion.local", "UnifiedMasterSecret999!")

	coord := engine.NewCoordinator(cat, eng, engine.Config{
		AuthService: authSvc,
	})
	coord.SetAuthService(authSvc, true) // enforce auth

	srv := NewServerWithCoordinator("127.0.0.1:0", cat, eng, nil, coord)
	defer srv.Close()

	// 1. sa with correct superuser password succeeds
	t.Run("SaCorrectPassword", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()
		go srv.handleConnection(serverConn)

		resp := sendTestLogin7(t, clientConn, "sa", "UnifiedMasterSecret999!")
		if !bytes.Contains(resp.Payload, []byte{TokenLoginAck}) {
			t.Fatalf("expected LOGINACK for sa with correct password")
		}
	})

	// 2. sa with wrong password fails with error 18456
	t.Run("SaWrongPassword", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()
		go srv.handleConnection(serverConn)

		resp := sendTestLogin7(t, clientConn, "sa", "BadPassword!")
		if !bytes.Contains(resp.Payload, []byte{TokenError}) {
			t.Fatalf("expected TokenError (0xAA) for sa with wrong password")
		}
		if !strings.Contains(string(resp.Payload), "18456") && !bytes.Contains(resp.Payload, []byte{0x18, 0x48}) {
			// check for 18456 in LE: 18456 = 0x4818 -> bytes [0x18, 0x48]
			if !bytes.Contains(resp.Payload, []byte{0x18, 0x48}) {
				t.Fatalf("expected error number 18456 in error token")
			}
		}
	})

	// 3. postgres with correct superuser password succeeds
	t.Run("PostgresCorrectPassword", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()
		go srv.handleConnection(serverConn)

		resp := sendTestLogin7(t, clientConn, "postgres", "UnifiedMasterSecret999!")
		if !bytes.Contains(resp.Payload, []byte{TokenLoginAck}) {
			t.Fatalf("expected LOGINACK for postgres with correct password")
		}
	})

	// 4. admin with correct superuser password succeeds
	t.Run("AdminCorrectPassword", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()
		go srv.handleConnection(serverConn)

		resp := sendTestLogin7(t, clientConn, "admin", "UnifiedMasterSecret999!")
		if !bytes.Contains(resp.Payload, []byte{TokenLoginAck}) {
			t.Fatalf("expected LOGINACK for admin with correct password")
		}
	})
}
