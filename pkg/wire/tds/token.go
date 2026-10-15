package tds

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TDS Token Identifiers
const (
	TokenReturnStatus byte = 0x79
	TokenColMetaData  byte = 0x81
	TokenOrder        byte = 0xA9
	TokenError        byte = 0xAA
	TokenInfo         byte = 0xAB
	TokenReturnValue  byte = 0xAC
	TokenLoginAck     byte = 0xAD
	TokenRow          byte = 0xD1
	TokenNBCRow       byte = 0xD2
	TokenEnvChange    byte = 0xE3
	TokenDone         byte = 0xFD
	TokenDoneProc     byte = 0xFE
	TokenDoneInProc   byte = 0xFF
)

// TDS Data Types
const (
	TypeNull       byte = 0x1F
	TypeGuid       byte = 0x24
	TypeInt1       byte = 0x30
	TypeBit        byte = 0x32
	TypeInt2       byte = 0x34
	TypeInt4       byte = 0x38
	TypeDateTime4  byte = 0x3A
	TypeFlt4       byte = 0x3B
	TypeMoney4     byte = 0x7A
	TypeDateTime   byte = 0x3D
	TypeFlt8       byte = 0x3E
	TypeMoney      byte = 0x3C
	TypeInt8       byte = 0x7F
	TypeIntN       byte = 0x26
	TypeBitN       byte = 0x68
	TypeDecimalN   byte = 0x6A
	TypeNumericN   byte = 0x6C
	TypeFltN       byte = 0x6D
	TypeMoneyN     byte = 0x6E
	TypeDateTimeN  byte = 0x6F
	TypeDateN      byte = 0x28
	TypeTimeN      byte = 0x29
	TypeDateTime2N byte = 0x2A
	TypeDateTimeOffsetN byte = 0x2B
	TypeBigVarChar byte = 0xA7
	TypeVarChar    byte = 0xA7
	TypeNVarChar   byte = 0xE7
	TypeBigBinary  byte = 0xAD
	TypeBigVarBin  byte = 0xA5
)

// Done Status Flags
const (
	DoneFinal    uint16 = 0x0000
	DoneMore     uint16 = 0x0001
	DoneError    uint16 = 0x0002
	DoneInXact   uint16 = 0x0004
	DoneCount    uint16 = 0x0010
	DoneAttn     uint16 = 0x0020
	DoneSrvError uint16 = 0x0100
)

// EnvChange Types
const (
	EnvChangeDatabase          byte = 1
	EnvChangeLanguage          byte = 2
	EnvChangeCharacterSet      byte = 3
	EnvChangePacketSize        byte = 4
	EnvChangeCollation         byte = 7
	EnvChangeBeginTransaction  byte = 8
	EnvChangeCommitTransaction byte = 9
	EnvChangeRollbackTransaction byte = 10
	EnvChangeRouting           byte = 20
)

// Prelogin Option Tokens
const (
	PreloginOptionVersion         byte = 0x00
	PreloginOptionEncryption      byte = 0x01
	PreloginOptionInstOpt         byte = 0x02
	PreloginOptionThreadID        byte = 0x03
	PreloginOptionMARS            byte = 0x04
	PreloginOptionTraceID         byte = 0x05
	PreloginOptionFedAuthRequired byte = 0x06
	PreloginOptionNonce           byte = 0x07
	PreloginOptionTerminator      byte = 0xFF
)

// Prelogin Encryption Flags
const (
	EncryptOff    byte = 0x00
	EncryptOn     byte = 0x01
	EncryptNotSup byte = 0x02
	EncryptReq    byte = 0x03
)

// Default Collation: SQL_Latin1_General_CP1_CI_AS (0x09 0x04 0xD0 0x00 0x34)
var DefaultCollation = []byte{0x09, 0x04, 0xD0, 0x00, 0x34}

// ColumnInfo holds metadata for a result set column.
type ColumnInfo struct {
	Name      string
	Type      byte
	Size      int
	Scale     int
	Precision int
	Nullable  bool
	IsPLP     bool
}

// BuildPreloginResponse generates a PRELOGIN reply packet payload.
func BuildPreloginResponse(encryptSupport byte) []byte {
	// Structure:
	// Header entries: [OptionType (1B)][Offset (2B BE)][Length (2B BE)]... [0xFF]
	// Followed by option data payloads at their respective offsets.

	var buf bytes.Buffer

	// Version: 6 bytes (Major 15, Minor 0, Build 2000 (0x07D0), SubBuild 0 (0x0000))
	versionData := []byte{15, 0, 0x07, 0xD0, 0x00, 0x00}
	// Encryption: 1 byte
	encryptData := []byte{encryptSupport}
	// MARS: 1 byte (0 = disabled)
	marsData := []byte{0x00}

	// Calculate offsets
	// Header: 3 options * 5 bytes + 1 terminator = 16 bytes
	headerLen := uint16(3*5 + 1)

	vOffset := headerLen
	vLen := uint16(len(versionData))

	eOffset := vOffset + vLen
	eLen := uint16(len(encryptData))

	mOffset := eOffset + eLen
	mLen := uint16(len(marsData))

	// Option 0: Version
	buf.WriteByte(PreloginOptionVersion)
	_ = binary.Write(&buf, binary.BigEndian, vOffset)
	_ = binary.Write(&buf, binary.BigEndian, vLen)

	// Option 1: Encryption
	buf.WriteByte(PreloginOptionEncryption)
	_ = binary.Write(&buf, binary.BigEndian, eOffset)
	_ = binary.Write(&buf, binary.BigEndian, eLen)

	// Option 2: MARS
	buf.WriteByte(PreloginOptionMARS)
	_ = binary.Write(&buf, binary.BigEndian, mOffset)
	_ = binary.Write(&buf, binary.BigEndian, mLen)

	// Terminator
	buf.WriteByte(PreloginOptionTerminator)

	// Payloads
	buf.Write(versionData)
	buf.Write(encryptData)
	buf.Write(marsData)

	return buf.Bytes()
}

// ParsePrelogin parses options from a PRELOGIN payload into a map of option types to raw bytes.
func ParsePrelogin(payload []byte) map[byte][]byte {
	options := make(map[byte][]byte)
	idx := 0
	type optHeader struct {
		optType byte
		offset  uint16
		length  uint16
	}
	var headers []optHeader
	for idx < len(payload) {
		optType := payload[idx]
		if optType == PreloginOptionTerminator {
			break
		}
		if idx+5 > len(payload) {
			break
		}
		offset := binary.BigEndian.Uint16(payload[idx+1 : idx+3])
		length := binary.BigEndian.Uint16(payload[idx+3 : idx+5])
		headers = append(headers, optHeader{optType: optType, offset: offset, length: length})
		idx += 5
	}
	for _, h := range headers {
		start := int(h.offset)
		end := start + int(h.length)
		if start >= 0 && end <= len(payload) && start <= end {
			options[h.optType] = payload[start:end]
		}
	}
	return options
}

// BuildLoginAck builds a LOGINACK token (0xAD) byte slice.
func BuildLoginAck(progName string, major, minor byte, build uint16) []byte {
	var buf bytes.Buffer
	buf.WriteByte(TokenLoginAck)

	var payload bytes.Buffer
	payload.WriteByte(0x01) // Interface: SQL Server

	// TDS Version: 7.4 (0x74000004)
	_ = binary.Write(&payload, binary.BigEndian, uint32(0x74000004))

	// ProgName: B_VARCHAR (1 byte character length + UTF-16LE)
	u16Name := EncodeUTF16LE(progName)
	payload.WriteByte(byte(len(progName)))
	payload.Write(u16Name)

	// Server Version
	payload.WriteByte(major)
	payload.WriteByte(minor)
	_ = binary.Write(&payload, binary.BigEndian, build)

	// Token length (uint16 LE)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())

	return buf.Bytes()
}

// BuildEnvChangeDatabase builds an ENVCHANGE token (0xE3) for database switch.
func BuildEnvChangeDatabase(newDB, oldDB string) []byte {
	var payload bytes.Buffer
	payload.WriteByte(EnvChangeDatabase)

	// NewValue: B_VARCHAR (1B char count + UTF-16LE)
	payload.WriteByte(byte(len(newDB)))
	payload.Write(EncodeUTF16LE(newDB))

	// OldValue: B_VARCHAR
	payload.WriteByte(byte(len(oldDB)))
	payload.Write(EncodeUTF16LE(oldDB))

	var buf bytes.Buffer
	buf.WriteByte(TokenEnvChange)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildEnvChangePacketSize builds an ENVCHANGE token (0xE3) for packet size change.
func BuildEnvChangePacketSize(newSize, oldSize string) []byte {
	var payload bytes.Buffer
	payload.WriteByte(EnvChangePacketSize)

	payload.WriteByte(byte(len(newSize)))
	payload.Write(EncodeUTF16LE(newSize))

	payload.WriteByte(byte(len(oldSize)))
	payload.Write(EncodeUTF16LE(oldSize))

	var buf bytes.Buffer
	buf.WriteByte(TokenEnvChange)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildEnvChangeCollation builds an ENVCHANGE token (0xE3) for SQL collation.
func BuildEnvChangeCollation(collation []byte) []byte {
	var payload bytes.Buffer
	payload.WriteByte(EnvChangeCollation)

	// NewValue: 1 byte length + collation bytes
	payload.WriteByte(byte(len(collation)))
	payload.Write(collation)

	// OldValue: 1 byte length = 0 (empty B_VARCHAR)
	payload.WriteByte(0)

	var buf bytes.Buffer
	buf.WriteByte(TokenEnvChange)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildEnvChangeBeginTransaction builds an ENVCHANGE token (0xE3) for BEGIN TRANSACTION (Type 8).
func BuildEnvChangeBeginTransaction(txDescriptor uint64) []byte {
	var payload bytes.Buffer
	payload.WriteByte(EnvChangeBeginTransaction)

	// NewValue: B_VARBYTE (1 byte length = 8 + 8 bytes uint64 LE)
	payload.WriteByte(8)
	_ = binary.Write(&payload, binary.LittleEndian, txDescriptor)

	// OldValue: B_VARBYTE (1 byte length = 0)
	payload.WriteByte(0)

	var buf bytes.Buffer
	buf.WriteByte(TokenEnvChange)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildEnvChangeCommitTransaction builds an ENVCHANGE token (0xE3) for COMMIT TRANSACTION (Type 9).
func BuildEnvChangeCommitTransaction(txDescriptor uint64) []byte {
	var payload bytes.Buffer
	payload.WriteByte(EnvChangeCommitTransaction)

	// NewValue: B_VARBYTE (1 byte length = 0)
	payload.WriteByte(0)

	// OldValue: B_VARBYTE (1 byte length = 8 + 8 bytes uint64 LE)
	payload.WriteByte(8)
	_ = binary.Write(&payload, binary.LittleEndian, txDescriptor)

	var buf bytes.Buffer
	buf.WriteByte(TokenEnvChange)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildEnvChangeRollbackTransaction builds an ENVCHANGE token (0xE3) for ROLLBACK TRANSACTION (Type 10).
func BuildEnvChangeRollbackTransaction(txDescriptor uint64) []byte {
	var payload bytes.Buffer
	payload.WriteByte(EnvChangeRollbackTransaction)

	// NewValue: B_VARBYTE (1 byte length = 0)
	payload.WriteByte(0)

	// OldValue: B_VARBYTE (1 byte length = 8 + 8 bytes uint64 LE)
	payload.WriteByte(8)
	_ = binary.Write(&payload, binary.LittleEndian, txDescriptor)

	var buf bytes.Buffer
	buf.WriteByte(TokenEnvChange)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildDone builds a DONE (0xFD) or DONEINPROC (0xFF) token.
func BuildDone(tokenType byte, status uint16, curCmd uint16, rowCount uint64) []byte {
	var buf bytes.Buffer
	buf.WriteByte(tokenType)
	_ = binary.Write(&buf, binary.LittleEndian, status)
	_ = binary.Write(&buf, binary.LittleEndian, curCmd)
	_ = binary.Write(&buf, binary.LittleEndian, rowCount)
	return buf.Bytes()
}

// BuildDoneProc builds a DONEPROC token (0xFE) indicating stored procedure or RPC completion.
func BuildDoneProc(status uint16, curCmd uint16, rowCount uint64) []byte {
	return BuildDone(TokenDoneProc, status, curCmd, rowCount)
}

// BuildReturnStatus builds a RETURNSTATUS token (0x79) with 4-byte LE return value.
func BuildReturnStatus(val int32) []byte {
	buf := make([]byte, 5)
	buf[0] = TokenReturnStatus
	binary.LittleEndian.PutUint32(buf[1:5], uint32(val))
	return buf
}


// BuildReturnValueInt builds a RETURNVALUE token (0xAC) for integer output parameters (such as statement @handle).
func BuildReturnValueInt(paramOrdinal uint16, paramName string, val int32) []byte {
	var payload bytes.Buffer

	// ParamOrdinal: uint16 LE
	_ = binary.Write(&payload, binary.LittleEndian, paramOrdinal)

	// ParamName: B_VARCHAR (1 byte char length + UTF-16LE characters)
	if paramName != "" {
		payload.WriteByte(byte(len(paramName)))
		payload.Write(EncodeUTF16LE(paramName))
	} else {
		payload.WriteByte(0)
	}

	// StatusFlags (1B): 0x01 = Output parameter
	payload.WriteByte(0x01)

	// UserType (4B LE): 0
	_ = binary.Write(&payload, binary.LittleEndian, uint32(0))

	// Flags (2B LE): 0
	_ = binary.Write(&payload, binary.LittleEndian, uint16(0))

	// TypeInfo: TypeInt4 (0x38), fixed length 4 bytes
	payload.WriteByte(TypeInt4)

	// Value: int32 LE
	_ = binary.Write(&payload, binary.LittleEndian, val)

	var buf bytes.Buffer
	buf.WriteByte(TokenReturnValue)
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildError builds an ERROR token (0xAA).
func BuildError(number int32, state byte, class byte, msg, serverName, procName string, lineNum int32) []byte {
	var payload bytes.Buffer

	_ = binary.Write(&payload, binary.LittleEndian, number)
	payload.WriteByte(state)
	payload.WriteByte(class) // Severity

	// MsgText: US_VARCHAR (uint16 char length + UTF-16LE)
	_ = binary.Write(&payload, binary.LittleEndian, uint16(len(msg)))
	payload.Write(EncodeUTF16LE(msg))

	// ServerName: B_VARCHAR
	payload.WriteByte(byte(len(serverName)))
	payload.Write(EncodeUTF16LE(serverName))

	// ProcName: B_VARCHAR
	payload.WriteByte(byte(len(procName)))
	payload.Write(EncodeUTF16LE(procName))

	// LineNumber: int32 LE (TDS 7.2+)
	_ = binary.Write(&payload, binary.LittleEndian, lineNum)

	var buf bytes.Buffer
	buf.WriteByte(TokenError)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildInfo builds an INFO token (0xAB).
func BuildInfo(number int32, state byte, class byte, msg, serverName, procName string, lineNum int32) []byte {
	var payload bytes.Buffer

	_ = binary.Write(&payload, binary.LittleEndian, number)
	payload.WriteByte(state)
	payload.WriteByte(class)

	_ = binary.Write(&payload, binary.LittleEndian, uint16(len(msg)))
	payload.Write(EncodeUTF16LE(msg))

	payload.WriteByte(byte(len(serverName)))
	payload.Write(EncodeUTF16LE(serverName))

	payload.WriteByte(byte(len(procName)))
	payload.Write(EncodeUTF16LE(procName))

	_ = binary.Write(&payload, binary.LittleEndian, lineNum)

	var buf bytes.Buffer
	buf.WriteByte(TokenInfo)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(payload.Len()))
	buf.Write(payload.Bytes())
	return buf.Bytes()
}

// BuildColMetaData builds a COLMETADATA token (0x81).
func BuildColMetaData(cols []ColumnInfo) []byte {
	var buf bytes.Buffer
	buf.WriteByte(TokenColMetaData)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(cols)))

	for _, col := range cols {
		// UserType: uint32 LE (0)
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0))

		// Flags: uint16 LE (0x0001 if nullable)
		var flags uint16
		if col.Nullable {
			flags |= 0x0001
		}
		_ = binary.Write(&buf, binary.LittleEndian, flags)

		// TypeInfo
		buf.WriteByte(col.Type)

		switch col.Type {
		case TypeIntN:
			// MaxLength: 1B (e.g. 8 bytes for int64/int4)
			buf.WriteByte(byte(col.Size))
		case TypeFltN:
			// MaxLength: 1B (e.g. 8 bytes)
			buf.WriteByte(8)
		case TypeBitN:
			// MaxLength: 1B (1)
			buf.WriteByte(1)
		case TypeDateTimeN:
			// MaxLength: 1B (8)
			buf.WriteByte(8)
		case TypeDateN:
			// [MS-TDS] 2.2.5.6: DATENTYPE MUST NOT have a TYPE_VARLEN
		case TypeTimeN:
			// [MS-TDS] 2.2.5.6: TIMENTYPE scale (1 byte, 0..7). Default 7 (100ns precision).
			scale := byte(7)
			if col.Scale > 0 && col.Scale <= 7 {
				scale = byte(col.Scale)
			}
			buf.WriteByte(scale)
		case TypeDateTime2N:
			// [MS-TDS] 2.2.5.6: DATETIME2NTYPE scale (1 byte, 0..7). Default 7 (100ns precision).
			scale := byte(7)
			if col.Scale > 0 && col.Scale <= 7 {
				scale = byte(col.Scale)
			}
			buf.WriteByte(scale)
		case TypeDateTimeOffsetN:
			// [MS-TDS] 2.2.5.6: DATETIMEOFFSETNTYPE scale (1 byte, 0..7). Default 7 (100ns precision).
			scale := byte(7)
			if col.Scale > 0 && col.Scale <= 7 {
				scale = byte(col.Scale)
			}
			buf.WriteByte(scale)
		case TypeGuid:
			// [MS-TDS] 2.2.5.5.1.1: GUIDTYPE is BYTELEN_TYPE with MaxLength 16 (0x10)
			buf.WriteByte(16)
		case TypeNVarChar:
			if col.IsPLP || col.Size == -1 {
				_ = binary.Write(&buf, binary.LittleEndian, uint16(0xFFFF))
			} else {
				maxLen := uint16(col.Size * 2)
				if col.Size <= 0 || col.Size > 4000 {
					maxLen = 8000
				}
				_ = binary.Write(&buf, binary.LittleEndian, maxLen)
			}
			buf.Write(DefaultCollation)
		case TypeVarChar:
			if col.IsPLP || col.Size == -1 {
				_ = binary.Write(&buf, binary.LittleEndian, uint16(0xFFFF))
			} else {
				maxLen := uint16(col.Size)
				if col.Size <= 0 || col.Size > 8000 {
					maxLen = 8000
				}
				_ = binary.Write(&buf, binary.LittleEndian, maxLen)
			}
			buf.Write(DefaultCollation)
		case TypeBigVarBin:
			if col.IsPLP || col.Size == -1 {
				_ = binary.Write(&buf, binary.LittleEndian, uint16(0xFFFF))
			} else {
				maxLen := uint16(col.Size)
				if col.Size <= 0 || col.Size > 8000 {
					maxLen = 8000
				}
				_ = binary.Write(&buf, binary.LittleEndian, maxLen)
			}
		default:
			// Fallback to NVarChar
			if col.IsPLP || col.Size == -1 {
				_ = binary.Write(&buf, binary.LittleEndian, uint16(0xFFFF))
			} else {
				_ = binary.Write(&buf, binary.LittleEndian, uint16(8000))
			}
			buf.Write(DefaultCollation)
		}

		// ColName: B_VARCHAR (1B char count + UTF-16LE)
		buf.WriteByte(byte(len(col.Name)))
		buf.Write(EncodeUTF16LE(col.Name))
	}

	return buf.Bytes()
}

var tdsRowBufferPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 1024))
	},
}

// writeColumnValue serializes a non-NULL column value according to its column type.
func writeColumnValue(buf *bytes.Buffer, col ColumnInfo, val any) {
	switch col.Type {
	case TypeIntN:
		intVal := toInt64(val)
		if col.Size == 1 {
			buf.WriteByte(1)
			buf.WriteByte(byte(intVal))
		} else if col.Size == 2 {
			buf.WriteByte(2)
			_ = binary.Write(buf, binary.LittleEndian, int16(intVal))
		} else if col.Size == 4 {
			buf.WriteByte(4)
			_ = binary.Write(buf, binary.LittleEndian, int32(intVal))
		} else {
			buf.WriteByte(8)
			_ = binary.Write(buf, binary.LittleEndian, intVal)
		}

	case TypeFltN:
		fltVal := toFloat64(val)
		buf.WriteByte(8)
		_ = binary.Write(buf, binary.LittleEndian, fltVal)

	case TypeBitN:
		bitVal := toBool(val)
		buf.WriteByte(1)
		if bitVal {
			buf.WriteByte(1)
		} else {
			buf.WriteByte(0)
		}

	case TypeDateTimeN:
		// Encode as DATETIME (4 bytes days since 1900-01-01, 4 bytes 300ths of second)
		t := toTime(val)
		days, ticks := encodeDateTime(t)
		buf.WriteByte(8)
		_ = binary.Write(buf, binary.LittleEndian, days)
		_ = binary.Write(buf, binary.LittleEndian, ticks)

	case TypeDateN:
		t := toTime(val)
		tUTC := t.UTC()
		days := daysSince0001(tUTC.Year(), int(tUTC.Month()), tUTC.Day())
		buf.WriteByte(3)
		buf.WriteByte(byte(days))
		buf.WriteByte(byte(days >> 8))
		buf.WriteByte(byte(days >> 16))

	case TypeTimeN:
		scale := byte(7)
		if col.Scale > 0 && col.Scale <= 7 {
			scale = byte(col.Scale)
		}
		data := encodeTime(toTime(val), scale)
		buf.WriteByte(byte(len(data)))
		buf.Write(data)

	case TypeDateTime2N:
		scale := byte(7)
		if col.Scale > 0 && col.Scale <= 7 {
			scale = byte(col.Scale)
		}
		data := encodeDateTime2(toTime(val), scale)
		buf.WriteByte(byte(len(data)))
		buf.Write(data)

	case TypeDateTimeOffsetN:
		scale := byte(7)
		if col.Scale > 0 && col.Scale <= 7 {
			scale = byte(col.Scale)
		}
		data := encodeDateTimeOffset(toTime(val), scale)
		buf.WriteByte(byte(len(data)))
		buf.Write(data)

	case TypeGuid:
		guidBytes := encodeGUID(val)
		buf.WriteByte(16)
		buf.Write(guidBytes)

	case TypeNVarChar:
		if col.IsPLP || col.Size == -1 {
			writePLPValue(buf, EncodeUTF16LE(fmt.Sprintf("%v", val)))
		} else {
			strVal := fmt.Sprintf("%v", val)
			u16 := EncodeUTF16LE(strVal)
			_ = binary.Write(buf, binary.LittleEndian, uint16(len(u16)))
			buf.Write(u16)
		}

	case TypeVarChar:
		if col.IsPLP || col.Size == -1 {
			writePLPValue(buf, []byte(fmt.Sprintf("%v", val)))
		} else {
			strVal := fmt.Sprintf("%v", val)
			_ = binary.Write(buf, binary.LittleEndian, uint16(len(strVal)))
			buf.WriteString(strVal)
		}

	case TypeBigVarBin:
		var raw []byte
		if b, ok := val.([]byte); ok {
			raw = b
		} else {
			raw = []byte(fmt.Sprintf("%v", val))
		}
		if col.IsPLP || col.Size == -1 {
			writePLPValue(buf, raw)
		} else {
			_ = binary.Write(buf, binary.LittleEndian, uint16(len(raw)))
			buf.Write(raw)
		}

	default:
		if col.IsPLP || col.Size == -1 {
			writePLPValue(buf, EncodeUTF16LE(fmt.Sprintf("%v", val)))
		} else {
			strVal := fmt.Sprintf("%v", val)
			u16 := EncodeUTF16LE(strVal)
			_ = binary.Write(buf, binary.LittleEndian, uint16(len(u16)))
			buf.Write(u16)
		}
	}
}

// writePLPValue streams partially length-prefixed LOB payloads per [MS-TDS] 2.2.5.2.
// It writes an 8-byte total length, followed by 4-byte chunk lengths and chunks (up to 8000 bytes each),
// terminated by a 4-byte zero chunk (0x00000000).
func writePLPValue(buf *bytes.Buffer, payload []byte) {
	totalLen := uint64(len(payload))
	_ = binary.Write(buf, binary.LittleEndian, totalLen)
	chunkSize := 8000
	for offset := 0; offset < len(payload); offset += chunkSize {
		end := offset + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		chunk := payload[offset:end]
		_ = binary.Write(buf, binary.LittleEndian, uint32(len(chunk)))
		buf.Write(chunk)
	}
	_ = binary.Write(buf, binary.LittleEndian, uint32(0)) // 0 chunk terminates PLP stream
}

// BuildRow builds a ROW token (0xD1) for a row of values matching cols.
func BuildRow(cols []ColumnInfo, values []any) []byte {
	buf := tdsRowBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= 65536 {
			tdsRowBufferPool.Put(buf)
		}
	}()
	buf.WriteByte(TokenRow)

	for i, col := range cols {
		var val any
		if i < len(values) {
			val = values[i]
		}

		if val == nil {
			switch col.Type {
			case TypeIntN, TypeFltN, TypeBitN, TypeDateTimeN, TypeDateN, TypeTimeN, TypeDateTime2N, TypeDateTimeOffsetN, TypeGuid:
				buf.WriteByte(0) // Length 0 indicates NULL
			case TypeNVarChar, TypeVarChar, TypeBigVarBin:
				if col.IsPLP || col.Size == -1 {
					_ = binary.Write(buf, binary.LittleEndian, uint64(0xFFFFFFFFFFFFFFFF))
				} else {
					_ = binary.Write(buf, binary.LittleEndian, uint16(0xFFFF)) // 0xFFFF indicates NULL
				}
			default:
				if col.IsPLP || col.Size == -1 {
					_ = binary.Write(buf, binary.LittleEndian, uint64(0xFFFFFFFFFFFFFFFF))
				} else {
					_ = binary.Write(buf, binary.LittleEndian, uint16(0xFFFF))
				}
			}
			continue
		}

		writeColumnValue(buf, col, val)
	}

	res := make([]byte, buf.Len())
	copy(res, buf.Bytes())
	return res
}

// BuildNBCRow builds an NBCROW token (0xD2) with a null bitmap for a row of values matching cols.
// It complies with [MS-TDS] 2.2.7.13 NBCROW:
// Token: 0xD2
// NullBitmap: ceil(len(cols)/8) bytes. Bit k (byte k/8, bit k%8) = 1 indicates column k is NULL.
// ColumnData: non-NULL columns are serialized in column order; NULL columns take 0 bytes.
func BuildNBCRow(cols []ColumnInfo, values []any) []byte {
	buf := tdsRowBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= 65536 {
			tdsRowBufferPool.Put(buf)
		}
	}()
	buf.WriteByte(TokenNBCRow)

	numCols := len(cols)
	bitmapLen := (numCols + 7) / 8
	bitmap := make([]byte, bitmapLen)

	for i := 0; i < numCols; i++ {
		var val any
		if i < len(values) {
			val = values[i]
		}
		if val == nil {
			bitmap[i/8] |= 1 << (i % 8)
		}
	}
	buf.Write(bitmap)

	for i := 0; i < numCols; i++ {
		// If bit is set, column is NULL -> 0 bytes in data stream
		if (bitmap[i/8] & (1 << (i % 8))) != 0 {
			continue
		}

		var val any
		if i < len(values) {
			val = values[i]
		}
		writeColumnValue(buf, cols[i], val)
	}

	res := make([]byte, buf.Len())
	copy(res, buf.Bytes())
	return res
}


func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

func toBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case int, int64:
		return toInt64(v) != 0
	case string:
		return strings.EqualFold(b, "true") || b == "1"
	default:
		return false
	}
}

func toTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, t)
		if err == nil {
			return parsed
		}
		parsed, err = time.Parse(time.RFC3339, t)
		if err == nil {
			return parsed
		}
		parsed, err = time.Parse("2006-01-02 15:04:05.999999999 -07:00", t)
		if err == nil {
			return parsed
		}
		parsed, err = time.Parse("2006-01-02 15:04:05.999999999", t)
		if err == nil {
			return parsed
		}
		parsed, err = time.Parse("2006-01-02 15:04:05 -07:00", t)
		if err == nil {
			return parsed
		}
		parsed, err = time.Parse("2006-01-02 15:04:05", t)
		if err == nil {
			return parsed
		}
		parsed, err = time.Parse("2006-01-02", t)
		if err == nil {
			return parsed
		}
		parsed, err = time.Parse("15:04:05.999999999", t)
		if err == nil {
			return parsed
		}
		parsed, err = time.Parse("15:04:05", t)
		if err == nil {
			return parsed
		}
		return time.Now()
	default:
		return time.Now()
	}
}

var epoch1900 = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

func encodeDateTime(t time.Time) (int32, uint32) {
	tUTC := t.UTC()
	days := int32(tUTC.Sub(epoch1900).Hours() / 24)
	midnight := time.Date(tUTC.Year(), tUTC.Month(), tUTC.Day(), 0, 0, 0, 0, time.UTC)
	ms := tUTC.Sub(midnight).Milliseconds()
	// Ticks: 300ths of a second (3.3333 ms per tick)
	ticks := uint32(math.Round(float64(ms) * 300.0 / 1000.0))
	return days, ticks
}

// daysSince0001 returns the number of days elapsed since 0001-01-01 (where 0001-01-01 is 0).
func daysSince0001(year int, month int, day int) int32 {
	m := month
	y := year
	if m <= 2 {
		y -= 1
		m += 12
	}
	era := y / 400
	yoe := y - era*400
	doy := (153*(m-3)+2)/5 + day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return int32(era*146097 + doe - 306)
}

// encodeDateTime2 encodes a time.Time into TDS 7.3 DATETIME2 binary format according to [MS-TDS] 2.2.5.5.1.5.
// It consists of time(scale) followed by date (3 bytes LE days since 0001-01-01).
func encodeDateTime2(t time.Time, scale byte) []byte {
	if scale > 7 {
		scale = 7
	}
	tUTC := t.UTC()
	midnight := time.Date(tUTC.Year(), tUTC.Month(), tUTC.Day(), 0, 0, 0, 0, time.UTC)
	nanos := tUTC.Sub(midnight).Nanoseconds()

	nanosPerTick := int64(1)
	for i := 0; i < int(9-scale); i++ {
		nanosPerTick *= 10
	}
	ticks := uint64(nanos / nanosPerTick)

	// Time length according to [MS-TDS] 2.2.5.5.1:
	// scale 0..2: 3 bytes
	// scale 3..4: 4 bytes
	// scale 5..7: 5 bytes
	timeLen := 5
	if scale <= 2 {
		timeLen = 3
	} else if scale <= 4 {
		timeLen = 4
	}

	days := daysSince0001(tUTC.Year(), int(tUTC.Month()), tUTC.Day())

	buf := make([]byte, timeLen+3)
	for i := 0; i < timeLen; i++ {
		buf[i] = byte(ticks >> (i * 8))
	}
	buf[timeLen] = byte(days)
	buf[timeLen+1] = byte(days >> 8)
	buf[timeLen+2] = byte(days >> 16)
	return buf
}

// encodeTime encodes a time.Time into TDS 7.3 TIME binary format according to [MS-TDS] 2.2.5.5.1.4.
// It consists of an unsigned integer representing number of 10^(-scale) second increments since midnight (LE).
// Scale 0..2: 3 bytes, Scale 3..4: 4 bytes, Scale 5..7: 5 bytes.
func encodeTime(t time.Time, scale byte) []byte {
	if scale > 7 {
		scale = 7
	}
	nanos := int64(t.Hour())*3600*1e9 + int64(t.Minute())*60*1e9 + int64(t.Second())*1e9 + int64(t.Nanosecond())

	nanosPerTick := int64(1)
	for i := 0; i < int(9-scale); i++ {
		nanosPerTick *= 10
	}
	ticks := uint64(nanos / nanosPerTick)

	timeLen := 5
	if scale <= 2 {
		timeLen = 3
	} else if scale <= 4 {
		timeLen = 4
	}

	buf := make([]byte, timeLen)
	for i := 0; i < timeLen; i++ {
		buf[i] = byte(ticks >> (i * 8))
	}
	return buf
}

// encodeDateTimeOffset encodes a time.Time into TDS 7.3 DATETIMEOFFSET binary format according to [MS-TDS] 2.2.5.5.1.6.
// It consists of datetime2(scale) in UTC followed by a 2-byte signed integer representing timezone offset in minutes (LE).
func encodeDateTimeOffset(t time.Time, scale byte) []byte {
	if scale > 7 {
		scale = 7
	}
	tUTC := t.In(time.UTC)
	dt2 := encodeDateTime2(tUTC, scale)

	_, offsetSec := t.Zone()
	offsetMin := int16(offsetSec / 60)

	buf := make([]byte, len(dt2)+2)
	copy(buf, dt2)
	buf[len(dt2)] = byte(offsetMin)
	buf[len(dt2)+1] = byte(offsetMin >> 8)
	return buf
}

// encodeGUID parses a UUID and encodes it in Windows GUID Mixed-Endian binary layout for TDS uniqueidentifier.
// Data1: 4 bytes Little-Endian
// Data2: 2 bytes Little-Endian
// Data3: 2 bytes Little-Endian
// Data4: 8 bytes Big-Endian (preserved as-is)
func encodeGUID(val any) []byte {
	var raw [16]byte
	switch v := val.(type) {
	case [16]byte:
		raw = v
	case []byte:
		if len(v) == 16 {
			copy(raw[:], v)
		}
	case string:
		cleaned := strings.Trim(strings.TrimSpace(v), "'\"{}")
		cleaned = strings.ReplaceAll(cleaned, "-", "")
		if len(cleaned) == 32 {
			if b, err := hex.DecodeString(cleaned); err == nil && len(b) == 16 {
				copy(raw[:], b)
			}
		}
	default:
		s := fmt.Sprintf("%v", val)
		cleaned := strings.Trim(strings.TrimSpace(s), "'\"{}")
		cleaned = strings.ReplaceAll(cleaned, "-", "")
		if len(cleaned) == 32 {
			if b, err := hex.DecodeString(cleaned); err == nil && len(b) == 16 {
				copy(raw[:], b)
			}
		}
	}

	out := make([]byte, 16)
	out[0] = raw[3]
	out[1] = raw[2]
	out[2] = raw[1]
	out[3] = raw[0]

	out[4] = raw[5]
	out[5] = raw[4]

	out[6] = raw[7]
	out[7] = raw[6]

	copy(out[8:], raw[8:16])
	return out
}

// decodeGUID decodes a 16-byte Windows GUID Mixed-Endian binary representation into canonical RFC 4122 UUID string.
func decodeGUID(wire []byte) string {
	if len(wire) < 16 {
		return ""
	}
	var raw [16]byte
	raw[0] = wire[3]
	raw[1] = wire[2]
	raw[2] = wire[1]
	raw[3] = wire[0]

	raw[4] = wire[5]
	raw[5] = wire[4]

	raw[6] = wire[7]
	raw[7] = wire[6]

	copy(raw[8:16], wire[8:16])
	return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		raw[0], raw[1], raw[2], raw[3],
		raw[4], raw[5],
		raw[6], raw[7],
		raw[8], raw[9],
		raw[10], raw[11], raw[12], raw[13], raw[14], raw[15],
	)
}
