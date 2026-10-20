package tds

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// TDS Packet Types
const (
	PacketSQLBatch           byte = 0x01
	PacketPreTDS7Login       byte = 0x02
	PacketRPC                byte = 0x03
	PacketTabular            byte = 0x04
	PacketAttention          byte = 0x06
	PacketBulkLoad           byte = 0x07
	PacketTransactionManager byte = 0x0E
	PacketLogin7             byte = 0x10
	PacketSSPI               byte = 0x11
	PacketPrelogin           byte = 0x12
)

// TDS Status Flags
const (
	StatusNormal                  byte = 0x00
	StatusEOM                     byte = 0x01 // End of Message
	StatusIgnoreEvent             byte = 0x02
	StatusResetConnection         byte = 0x08
	StatusResetConnectionSkipTran byte = 0x10
)

const (
	HeaderSize        = 8
	DefaultPacketSize = 4096
)

// Packet represents a full logical TDS message (reassembled from one or more physical packets).
type Packet struct {
	Type     byte
	Status   byte
	SPID     uint16
	PacketID byte
	Payload  []byte
}

// ReadPacket reads a complete logical TDS message from r, reassembling multi-packet frames if necessary.
func ReadPacket(r io.Reader) (*Packet, error) {
	var fullPayload []byte
	var firstType byte
	var spid uint16
	var packetID byte

	for {
		var hdr [HeaderSize]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, err
		}

		pType := hdr[0]
		status := hdr[1]
		length := binary.BigEndian.Uint16(hdr[2:4])
		pSPID := binary.BigEndian.Uint16(hdr[4:6])
		pID := hdr[6]

		if length < HeaderSize {
			return nil, fmt.Errorf("invalid TDS packet length: %d (minimum %d)", length, HeaderSize)
		}

		payloadLen := int(length) - HeaderSize
		chunk := make([]byte, payloadLen)
		if _, err := io.ReadFull(r, chunk); err != nil {
			return nil, fmt.Errorf("failed to read TDS packet payload: %w", err)
		}

		if len(fullPayload) == 0 {
			firstType = pType
			spid = pSPID
			packetID = pID
		}
		fullPayload = append(fullPayload, chunk...)

		if status&StatusEOM != 0 {
			break
		}
	}

	return &Packet{
		Type:     firstType,
		Status:   StatusEOM,
		SPID:     spid,
		PacketID: packetID,
		Payload:  fullPayload,
	}, nil
}

var tdsFramePool = sync.Pool{
	New: func() any {
		b := make([]byte, DefaultPacketSize)
		return &b
	},
}

// WritePacket writes a payload chunked into TDS frames to w with the default packet size (4096).
func WritePacket(w io.Writer, pType byte, spid uint16, payload []byte) error {
	return WritePacketWithSize(w, pType, spid, payload, DefaultPacketSize)
}

// WritePacketWithSize writes a payload chunked into TDS frames to w with the specified packet size.
// It bundles the 8-byte TDS header and payload chunk into a single contiguous frame buffer.
// When packetSize == DefaultPacketSize (4096), it reuses buffers via sync.Pool to execute
// a single atomic write per packet (halving OS syscall overhead).
func WritePacketWithSize(w io.Writer, pType byte, spid uint16, payload []byte, packetSize uint32) error {
	if packetSize < 512 || packetSize > 32768 {
		packetSize = DefaultPacketSize
	}

	var frame []byte
	var bufPtr *[]byte
	if packetSize == DefaultPacketSize {
		bufPtr = tdsFramePool.Get().(*[]byte)
		defer tdsFramePool.Put(bufPtr)
		frame = *bufPtr
	} else {
		frame = make([]byte, packetSize)
	}

	if len(payload) == 0 {
		frame[0] = pType
		frame[1] = StatusEOM
		binary.BigEndian.PutUint16(frame[2:4], HeaderSize)
		binary.BigEndian.PutUint16(frame[4:6], spid)
		frame[6] = 1 // PacketID
		frame[7] = 0 // Window
		_, err := w.Write(frame[:HeaderSize])
		return err
	}

	maxPayloadPerPacket := int(packetSize) - HeaderSize
	var packetID byte = 1
	offset := 0

	for offset < len(payload) {
		remaining := len(payload) - offset
		chunkLen := remaining
		if chunkLen > maxPayloadPerPacket {
			chunkLen = maxPayloadPerPacket
		}

		isLast := (offset + chunkLen) >= len(payload)
		var status byte = StatusNormal
		if isLast {
			status = StatusEOM
		}

		packetLen := uint16(HeaderSize + chunkLen)
		frame[0] = pType
		frame[1] = status
		binary.BigEndian.PutUint16(frame[2:4], packetLen)
		binary.BigEndian.PutUint16(frame[4:6], spid)
		frame[6] = packetID
		frame[7] = 0

		copy(frame[HeaderSize:HeaderSize+chunkLen], payload[offset:offset+chunkLen])

		if _, err := w.Write(frame[:packetLen]); err != nil {
			return err
		}

		offset += chunkLen
		packetID++
	}

	return nil
}

// WriteUTF16LEDirect encodes string s as UTF-16LE directly into buf, avoiding intermediate slice allocations.
// It returns the number of bytes written.
func WriteUTF16LEDirect(buf *bytes.Buffer, s string) int {
	if len(s) == 0 {
		return 0
	}

	// ASCII fast-path: check if all characters are 7-bit ASCII
	isASCII := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			isASCII = false
			break
		}
	}
	if isASCII {
		buf.Grow(len(s) * 2)
		for i := 0; i < len(s); i++ {
			buf.WriteByte(s[i])
			buf.WriteByte(0)
		}
		return len(s) * 2
	}

	// Non-ASCII path: direct encoding without intermediate []rune / []uint16 allocations
	buf.Grow(len(s) * 2)
	bytesWritten := 0
	for _, r := range s {
		switch {
		case 0 <= r && r < 0xD800, 0xE000 <= r && r <= 0xFFFF:
			buf.WriteByte(byte(r))
			buf.WriteByte(byte(r >> 8))
			bytesWritten += 2
		case 0x10000 <= r && r <= 0x10FFFF:
			r1, r2 := utf16.EncodeRune(r)
			buf.WriteByte(byte(r1))
			buf.WriteByte(byte(r1 >> 8))
			buf.WriteByte(byte(r2))
			buf.WriteByte(byte(r2 >> 8))
			bytesWritten += 4
		default:
			buf.WriteByte(0xFD)
			buf.WriteByte(0xFF) // unicode.ReplacementChar (0xFFFD)
			bytesWritten += 2
		}
	}
	return bytesWritten
}

// EncodeUTF16LE converts a Go UTF-8 string to a UTF-16LE byte slice.
func EncodeUTF16LE(s string) []byte {
	if len(s) == 0 {
		return make([]byte, 0)
	}

	// ASCII fast-path: check if all characters are 7-bit ASCII
	isASCII := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			isASCII = false
			break
		}
	}
	if isASCII {
		buf := make([]byte, len(s)*2)
		for i := 0; i < len(s); i++ {
			buf[i*2] = s[i]
			buf[i*2+1] = 0
		}
		return buf
	}

	// Non-ASCII path: direct encoding without intermediate []rune / []uint16 allocations
	buf := make([]byte, 0, len(s)*2)
	for _, r := range s {
		switch {
		case 0 <= r && r < 0xD800, 0xE000 <= r && r <= 0xFFFF:
			buf = append(buf, byte(r), byte(r>>8))
		case 0x10000 <= r && r <= 0x10FFFF:
			r1, r2 := utf16.EncodeRune(r)
			buf = append(buf, byte(r1), byte(r1>>8), byte(r2), byte(r2>>8))
		default:
			buf = append(buf, 0xFD, 0xFF) // unicode.ReplacementChar (0xFFFD)
		}
	}
	return buf
}

// DecodeUTF16LE converts a UTF-16LE byte slice to a Go UTF-8 string.
func DecodeUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return ""
	}

	// ASCII fast-path: check if all 16-bit code units are 7-bit ASCII
	isASCII := true
	for i := 0; i < len(b); i += 2 {
		if b[i+1] != 0 || b[i] >= 0x80 {
			isASCII = false
			break
		}
	}
	if isASCII {
		var sb strings.Builder
		sb.Grow(len(b) / 2)
		for i := 0; i < len(b); i += 2 {
			sb.WriteByte(b[i])
		}
		return sb.String()
	}

	// Non-ASCII path: direct decode without intermediate []uint16 and []rune slice allocations
	var sb strings.Builder
	sb.Grow(len(b))
	var utf8Buf [utf8.UTFMax]byte
	nWords := len(b) / 2
	for i := 0; i < nWords; i++ {
		u := binary.LittleEndian.Uint16(b[i*2:])
		ru := rune(u)
		if utf16.IsSurrogate(ru) {
			if i+1 < nWords {
				u2 := binary.LittleEndian.Uint16(b[(i+1)*2:])
				r := utf16.DecodeRune(ru, rune(u2))
				if r != unicode.ReplacementChar {
					i++ // consumed surrogate pair
					n := utf8.EncodeRune(utf8Buf[:], r)
					sb.Write(utf8Buf[:n])
					continue
				}
			}
			n := utf8.EncodeRune(utf8Buf[:], unicode.ReplacementChar)
			sb.Write(utf8Buf[:n])
		} else {
			n := utf8.EncodeRune(utf8Buf[:], ru)
			sb.Write(utf8Buf[:n])
		}
	}
	return sb.String()
}

// ReadBVarChar reads a length-prefixed (uint8 character count) UTF-16LE string.
func ReadBVarChar(r *bytes.Reader) (string, error) {
	charCount, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	byteCount := int(charCount) * 2
	buf := make([]byte, byteCount)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return DecodeUTF16LE(buf), nil
}

// ReadUsVarChar reads a length-prefixed (uint16 character count) UTF-16LE string.
func ReadUsVarChar(r *bytes.Reader) (string, error) {
	var charCount uint16
	if err := binary.Read(r, binary.LittleEndian, &charCount); err != nil {
		return "", err
	}
	byteCount := int(charCount) * 2
	buf := make([]byte, byteCount)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return DecodeUTF16LE(buf), nil
}
