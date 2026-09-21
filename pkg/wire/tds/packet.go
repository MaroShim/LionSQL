package tds

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"unicode/utf16"
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

// WritePacket writes a payload chunked into TDS frames to w with the specified packet type.
func WritePacket(w io.Writer, pType byte, spid uint16, payload []byte) error {
	if len(payload) == 0 {
		var hdr [HeaderSize]byte
		hdr[0] = pType
		hdr[1] = StatusEOM
		binary.BigEndian.PutUint16(hdr[2:4], HeaderSize)
		binary.BigEndian.PutUint16(hdr[4:6], spid)
		hdr[6] = 1 // PacketID
		hdr[7] = 0 // Window
		_, err := w.Write(hdr[:])
		return err
	}

	maxPayloadPerPacket := DefaultPacketSize - HeaderSize
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
		var hdr [HeaderSize]byte
		hdr[0] = pType
		hdr[1] = status
		binary.BigEndian.PutUint16(hdr[2:4], packetLen)
		binary.BigEndian.PutUint16(hdr[4:6], spid)
		hdr[6] = packetID
		hdr[7] = 0

		if _, err := w.Write(hdr[:]); err != nil {
			return err
		}
		if _, err := w.Write(payload[offset : offset+chunkLen]); err != nil {
			return err
		}

		offset += chunkLen
		packetID++
	}

	return nil
}

// EncodeUTF16LE converts a Go UTF-8 string to a UTF-16LE byte slice.
func EncodeUTF16LE(s string) []byte {
	runes := []rune(s)
	u16 := utf16.Encode(runes)
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(buf[i*2:], v)
	}
	return buf
}

// DecodeUTF16LE converts a UTF-16LE byte slice to a Go UTF-8 string.
func DecodeUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u16 := make([]uint16, len(b)/2)
	for i := 0; i < len(u16); i++ {
		u16[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u16))
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
