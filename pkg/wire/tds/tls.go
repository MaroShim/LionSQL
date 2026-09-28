package tds

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// bufferedConn wraps a net.Conn with a prepended io.Reader to replay peeked bytes.
type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

// TDSHandshakeConn wraps a net.Conn to encapsulate and decapsulate TLS handshake records
// within TDS 0x12 (PRELOGIN) packets during TDS 7.x encryption negotiation.
type TDSHandshakeConn struct {
	net.Conn
	mu            sync.Mutex
	handshakeDone atomic.Bool
	readBuf       bytes.Buffer
	spid          uint16
}

// NewTDSHandshakeConn creates a new TDS handshake encapsulation connection wrapper.
func NewTDSHandshakeConn(conn net.Conn, spid uint16) *TDSHandshakeConn {
	return &TDSHandshakeConn{
		Conn: conn,
		spid: spid,
	}
}

// FinishHandshake switches the connection from TDS packet encapsulation to raw transport passthrough.
func (c *TDSHandshakeConn) FinishHandshake() {
	c.handshakeDone.Store(true)
}

func (c *TDSHandshakeConn) Read(b []byte) (int, error) {
	if c.handshakeDone.Load() {
		c.mu.Lock()
		if c.readBuf.Len() > 0 {
			n, err := c.readBuf.Read(b)
			c.mu.Unlock()
			return n, err
		}
		c.mu.Unlock()
		return c.Conn.Read(b)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for c.readBuf.Len() == 0 {
		pkt, err := ReadPacket(c.Conn)
		if err != nil {
			return 0, err
		}
		if pkt.Type != PacketPrelogin {
			return 0, fmt.Errorf("expected TDS PacketPrelogin (0x12) during TLS handshake, got 0x%02X", pkt.Type)
		}
		c.readBuf.Write(pkt.Payload)
	}
	return c.readBuf.Read(b)
}

func (c *TDSHandshakeConn) Write(b []byte) (int, error) {
	if c.handshakeDone.Load() {
		return c.Conn.Write(b)
	}
	// Wrap TLS handshake payload into TDS PacketPrelogin (0x12)
	if err := WritePacket(c.Conn, PacketPrelogin, c.spid, b); err != nil {
		return 0, err
	}
	return len(b), nil
}
