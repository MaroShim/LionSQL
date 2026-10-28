package proc

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"lionsql/pkg/storage"
)

var (
	uuidv7Mu sync.Mutex
	lastV7Ms int64
	v7Seq    uint16
)

// GenerateUUIDv7 generates an RFC 9562 Version 7 UUID string with 48-bit millisecond timestamp
// and sub-millisecond sequence counter to guarantee strict monotonic ordering for B+Tree primary keys.
func GenerateUUIDv7() string {
	return generateUUIDv7()
}

// GenerateUUIDv4 generates an RFC 4122 Version 4 random UUID string.
func GenerateUUIDv4() string {
	return generateUUIDv4()
}

func generateUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10
	return strings.ToUpper(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

func generateUUIDv7() string {
	var b [16]byte
	nowMs := time.Now().UnixMilli()

	uuidv7Mu.Lock()
	if nowMs > lastV7Ms {
		lastV7Ms = nowMs
		// Seed 12-bit sequence randomly with headroom (0..0x7FF) to avoid early overflow
		var seed [2]byte
		_, _ = rand.Read(seed[:])
		v7Seq = (uint16(seed[0]&0x07) << 8) | uint16(seed[1])
	} else {
		// Same millisecond or clock backwards: increment sequence counter for strict monotonicity
		v7Seq = (v7Seq + 1) & 0x0FFF
		if v7Seq == 0 {
			// Sequence counter rolled over within same millisecond; advance timestamp by 1ms
			lastV7Ms++
		}
		nowMs = lastV7Ms
	}
	seq := v7Seq
	uuidv7Mu.Unlock()

	// 48-bit timestamp (big-endian)
	b[0] = byte(nowMs >> 40)
	b[1] = byte(nowMs >> 32)
	b[2] = byte(nowMs >> 24)
	b[3] = byte(nowMs >> 16)
	b[4] = byte(nowMs >> 8)
	b[5] = byte(nowMs)

	// 4-bit version (0x7) + 12-bit sequence counter (rand_a)
	b[6] = 0x70 | byte((seq>>8)&0x0F)
	b[7] = byte(seq & 0xFF)

	// 62-bit rand_b: fill bytes 8..15 with cryptographically secure random bytes
	_, _ = rand.Read(b[8:])
	// 2-bit variant (10xxxxxx)
	b[8] = (b[8] & 0x3F) | 0x80

	return strings.ToUpper(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}


func parseObjectPieces(s string) []string {
	var parts []string
	var current strings.Builder
	inBracket := false
	inQuote := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '[' && !inQuote {
			inBracket = true
			continue
		} else if ch == ']' && inBracket {
			inBracket = false
			continue
		} else if ch == '"' && !inBracket {
			inQuote = !inQuote
			continue
		} else if ch == '.' && !inBracket && !inQuote {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	parts = append(parts, current.String())

	if len(parts) > 4 {
		return nil
	}
	return parts
}



func isSysBuiltin(name string) bool {
	switch name {
	case "ISNULL", "COALESCE", "NULLIF", "IFNULL", "NVL", "ISNUMERIC", "IIF", "CHOOSE", "GREATEST", "LEAST",
		"NEWID", "NEWID7", "UUIDV7", "UUID_GENERATE_V7", "GEN_RANDOM_UUID",
		"SCOPE_IDENTITY", "SERVERPROPERTY",
		"ERROR_NUMBER", "ERROR_MESSAGE", "ERROR_LINE", "ERROR_SEVERITY", "ERROR_STATE", "ERROR_PROCEDURE", "@@ERROR",
		"OBJECT_ID", "OBJECT_NAME", "COL_LENGTH", "COL_NAME", "SCHEMA_NAME", "SCHEMA_ID", "DB_NAME", "DB_ID", "APP_NAME", "IDENT_CURRENT",
		"DATABASEPROPERTYEX", "OBJECTPROPERTY", "COLUMNPROPERTY", "INDEXPROPERTY", "OBJECT_SCHEMA_NAME", "TYPE_ID", "TYPE_NAME",
		"HOST_NAME", "HOST_ID", "CURRENT_USER", "USER_NAME", "SUSER_NAME", "SUSER_SNAME", "USER_ID", "SUSER_ID",
		"IS_SRVROLEMEMBER", "IS_MEMBER", "HAS_PERMS_BY_NAME", "PWDENCRYPT", "PWDCOMPARE",
		"@@VERSION", "VERSION", "@@SERVERNAME", "@@SPID", "SPID", "@@MAX_CONNECTIONS", "@@LANGUAGE",
		"@@TRANCOUNT", "TRANCOUNT", "@@LOCK_TIMEOUT", "LOCK_TIMEOUT", "@@OPTIONS", "OPTIONS", "@@NESTLEVEL", "NESTLEVEL",
		"@@FETCH_STATUS", "FETCH_STATUS",
		"@@CPU_BUSY", "@@IO_BUSY", "@@IDLE",
		"HASHBYTES", "CHECKSUM", "BINARY_CHECKSUM":
		return true
	default:
		return false
	}
}

func evalSysBuiltin(upper string, args []storage.Value) storage.Value {
	switch upper {
	// 4. Conditional & Logical Functions
	// ----------------------------------------------------
	case "ISNULL", "COALESCE", "IFNULL", "NVL":
		for _, a := range args {
			if !a.IsNull {
				return a
			}
		}
		return storage.NullValue(storage.TypeUnknown)

	case "NULLIF":
		if len(args) < 2 {
			return storage.NullValue(storage.TypeUnknown)
		}
		if args[0].IsNull {
			return args[0]
		}
		if !args[1].IsNull && args[0].Equal(args[1]) {
			return storage.NullValue(args[0].Type)
		}
		return args[0]

	case "ISNUMERIC":
		if len(args) == 0 || args[0].IsNull {
			return storage.IntValue(0)
		}
		switch args[0].Type {
		case storage.TypeInt, storage.TypeFloat, storage.TypeDecimal:
			return storage.IntValue(1)
		case storage.TypeVarChar:
			s := strings.TrimSpace(args[0].SVal)
			if s == "" || strings.EqualFold(s, "nan") || strings.EqualFold(s, "infinity") || strings.EqualFold(s, "+infinity") || strings.EqualFold(s, "-infinity") || strings.EqualFold(s, "inf") || strings.EqualFold(s, "+inf") || strings.EqualFold(s, "-inf") {
				return storage.IntValue(0)
			}
			if _, err := strconv.ParseFloat(s, 64); err == nil {
				return storage.IntValue(1)
			}
			return storage.IntValue(0)
		default:
			return storage.IntValue(0)
		}

	case "IIF":
		if len(args) < 3 {
			return storage.NullValue(storage.TypeUnknown)
		}
		cond := isTruthyStorageValue(args[0])
		if cond {
			return args[1]
		}
		return args[2]

	case "CHOOSE":
		if len(args) < 2 || args[0].IsNull {
			return storage.NullValue(storage.TypeUnknown)
		}
		idx := int(args[0].IVal)
		if idx >= 1 && idx < len(args) {
			return args[idx]
		}
		return storage.NullValue(storage.TypeUnknown)

	case "GREATEST":
		if len(args) == 0 {
			return storage.NullValue(storage.TypeUnknown)
		}
		var maxVal storage.Value
		hasVal := false
		for _, a := range args {
			if a.IsNull {
				continue
			}
			if !hasVal {
				maxVal = a
				hasVal = true
			} else if compareValues(a, maxVal) > 0 {
				maxVal = a
			}
		}
		if !hasVal {
			return storage.NullValue(storage.TypeUnknown)
		}
		return maxVal

	case "LEAST":
		if len(args) == 0 {
			return storage.NullValue(storage.TypeUnknown)
		}
		var minVal storage.Value
		hasVal := false
		for _, a := range args {
			if a.IsNull {
				continue
			}
			if !hasVal {
				minVal = a
				hasVal = true
			} else if compareValues(a, minVal) < 0 {
				minVal = a
			}
		}
		if !hasVal {
			return storage.NullValue(storage.TypeUnknown)
		}
		return minVal

	// ----------------------------------------------------

	// 6. System Functions & Metadata
	// ----------------------------------------------------
	case "NEWID":
		return storage.StringValue(generateUUIDv4())

	case "NEWID7", "UUIDV7", "UUID_GENERATE_V7":
		return storage.Value{Type: storage.TypeUUID, SVal: generateUUIDv7()}

	case "GEN_RANDOM_UUID":
		if len(args) > 0 && (args[0].IVal == 7 || args[0].SVal == "7") {
			return storage.Value{Type: storage.TypeUUID, SVal: generateUUIDv7()}
		}
		return storage.Value{Type: storage.TypeUUID, SVal: generateUUIDv4()}

	case "SERVERPROPERTY":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[0])))
		switch prop {
		case "PRODUCTVERSION":
			return storage.StringValue("15.0.2000.5")
		case "PRODUCTMAJORVERSION":
			return storage.StringValue("15")
		case "PRODUCTLEVEL":
			return storage.StringValue("RTM")
		case "EDITION":
			return storage.StringValue("Developer Edition (64-bit)")
		case "ENGINEEDITION":
			return storage.IntValue(3)
		case "INSTANCENAME":
			return storage.StringValue("MSSQLSERVER")
		case "MACHINENAME", "SERVERNAME":
			return storage.StringValue("LionSQL")
		case "COLLATION":
			return storage.StringValue("SQL_Latin1_General_CP1_CI_AS")
		case "ISCLUSTERED", "ISHADRENABLED", "ISINTEGRATEDSECURITYONLY":
			return storage.IntValue(0)
		default:
			return storage.StringValue("")
		}

	// ----------------------------------------------------
	// 7. Error Handling & Execution Context (Group A)
	// ----------------------------------------------------
	case "ERROR_NUMBER", "@@ERROR":
		return storage.IntValue(0)

	case "ERROR_MESSAGE":
		return storage.StringValue("")

	case "ERROR_LINE":
		return storage.IntValue(0)

	case "ERROR_SEVERITY":
		return storage.IntValue(16)

	case "ERROR_STATE":
		return storage.IntValue(1)

	case "ERROR_PROCEDURE":
		return storage.NullValue(storage.TypeVarChar)

	// ----------------------------------------------------
	// 8. Metadata & Schema Introspection (Group D)
	// ----------------------------------------------------
	case "OBJECT_ID":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		objName := strings.TrimSpace(valToString(args[0]))
		if objName == "" {
			return storage.NullValue(storage.TypeInt)
		}
		clean := strings.ToLower(cleanBuiltinName(objName))
		id := int64(crc32.ChecksumIEEE([]byte(clean)))
		if id <= 0 {
			id = 1
		}
		return storage.IntValue(id)

	case "OBJECT_NAME":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.NullValue(storage.TypeVarChar)

	case "COL_LENGTH":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(50)

	case "SCHEMA_NAME":
		schemaID := int64(1)
		if len(args) > 0 && !args[0].IsNull {
			schemaID = args[0].IVal
		}
		if schemaID == 1 {
			return storage.StringValue("dbo")
		}
		return storage.StringValue("dbo")

	case "SCHEMA_ID":
		sName := "dbo"
		if len(args) > 0 && !args[0].IsNull {
			sName = valToString(args[0])
		}
		if strings.EqualFold(cleanBuiltinName(sName), "dbo") {
			return storage.IntValue(1)
		}
		return storage.IntValue(1)

	case "DB_NAME":
		return storage.StringValue("LionSQL")

	case "DB_ID":
		return storage.IntValue(1)

	case "APP_NAME":
		return storage.StringValue("LionSQL")

	case "IDENT_CURRENT":
		return storage.IntValue(1)

	case "COL_NAME":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		colID := args[1].IVal
		if colID == 1 {
			return storage.StringValue("id")
		}
		return storage.StringValue(fmt.Sprintf("col%d", colID))

	case "HOST_NAME":
		h, err := os.Hostname()
		if err != nil || h == "" {
			return storage.StringValue("localhost")
		}
		return storage.StringValue(h)

	case "HOST_ID":
		return storage.IntValue(int64(os.Getpid()))

	case "CURRENT_USER", "USER_NAME":
		return storage.StringValue("dbo")

	case "SUSER_NAME", "SUSER_SNAME":
		return storage.StringValue("sa")

	case "USER_ID":
		return storage.IntValue(1)

	case "SUSER_ID":
		return storage.IntValue(1)

	case "@@VERSION", "VERSION":
		return storage.StringValue("Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64) / LionSQL Embedded Engine")

	case "@@SERVERNAME":
		return storage.StringValue("LIONSQL")

	case "@@SPID", "SPID":
		return storage.IntValue(51)

	case "@@MAX_CONNECTIONS":
		return storage.IntValue(32767)

	case "@@LANGUAGE":
		return storage.StringValue("us_english")

	case "@@TRANCOUNT", "TRANCOUNT":
		return storage.IntValue(0)

	case "@@FETCH_STATUS", "FETCH_STATUS":
		return storage.IntValue(-1)

	case "@@LOCK_TIMEOUT", "LOCK_TIMEOUT":
		return storage.IntValue(-1)

	case "@@OPTIONS", "OPTIONS":
		return storage.IntValue(5496)

	case "@@NESTLEVEL", "NESTLEVEL":
		return storage.IntValue(0)

	case "@@CPU_BUSY":
		return storage.IntValue(100)

	case "@@IO_BUSY":
		return storage.IntValue(50)

	case "@@IDLE":
		return storage.IntValue(1000)

	case "DATABASEPROPERTYEX":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[1])))
		switch prop {
		case "STATUS":
			return storage.StringValue("ONLINE")
		case "COLLATION":
			return storage.StringValue("SQL_Latin1_General_CP1_CI_AS")
		case "COMPARISONSTYLE":
			return storage.IntValue(196609)
		case "EDITION":
			return storage.StringValue("Developer")
		case "RECOVERY":
			return storage.StringValue("SIMPLE")
		case "VERSION":
			return storage.IntValue(904)
		case "USERACCESS":
			return storage.StringValue("MULTI_USER")
		case "ISAUTOCLOSE", "ISAUTOSHRINK", "ISINMEMCAPABLE":
			return storage.IntValue(0)
		default:
			return storage.StringValue("")
		}

	case "OBJECTPROPERTY":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[1])))
		switch prop {
		case "ISTABLE", "ISUSERTABLE", "ISPROCEDURE", "ISVIEW", "ISTRIGGER", "ISQUOTEDIDON", "ISANSINULLSON":
			return storage.IntValue(1)
		case "ISMSSHIPPED", "ISREPLICATED":
			return storage.IntValue(0)
		default:
			return storage.IntValue(0)
		}

	case "COLUMNPROPERTY":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		colName := strings.ToLower(strings.TrimSpace(valToString(args[1])))
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[2])))
		switch prop {
		case "ISIDENTITY":
			if colName == "id" {
				return storage.IntValue(1)
			}
			return storage.IntValue(0)
		case "ALLOWSNULL", "PRECISION", "SCALE":
			return storage.IntValue(1)
		default:
			return storage.IntValue(0)
		}

	case "INDEXPROPERTY":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		prop := strings.ToUpper(strings.TrimSpace(valToString(args[2])))
		switch prop {
		case "ISUNIQUE", "ISCLUSTERED":
			return storage.IntValue(1)
		default:
			return storage.IntValue(0)
		}

	case "OBJECT_SCHEMA_NAME":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue("dbo")

	case "TYPE_ID":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		tName := strings.ToLower(strings.TrimSpace(valToString(args[0])))
		switch tName {
		case "int":
			return storage.IntValue(56)
		case "bigint":
			return storage.IntValue(127)
		case "smallint":
			return storage.IntValue(52)
		case "tinyint":
			return storage.IntValue(48)
		case "varchar":
			return storage.IntValue(167)
		case "nvarchar":
			return storage.IntValue(231)
		case "datetime":
			return storage.IntValue(61)
		case "bit":
			return storage.IntValue(104)
		case "decimal", "numeric":
			return storage.IntValue(106)
		case "float":
			return storage.IntValue(62)
		case "uniqueidentifier":
			return storage.IntValue(36)
		default:
			return storage.NullValue(storage.TypeInt)
		}

	case "TYPE_NAME":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		tID := args[0].IVal
		switch tID {
		case 56:
			return storage.StringValue("int")
		case 127:
			return storage.StringValue("bigint")
		case 52:
			return storage.StringValue("smallint")
		case 48:
			return storage.StringValue("tinyint")
		case 167:
			return storage.StringValue("varchar")
		case 231:
			return storage.StringValue("nvarchar")
		case 61:
			return storage.StringValue("datetime")
		case 104:
			return storage.StringValue("bit")
		case 106:
			return storage.StringValue("decimal")
		case 62:
			return storage.StringValue("float")
		case 36:
			return storage.StringValue("uniqueidentifier")
		default:
			return storage.NullValue(storage.TypeVarChar)
		}

	case "IS_SRVROLEMEMBER":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		role := strings.ToLower(strings.TrimSpace(valToString(args[0])))
		if role == "sysadmin" || role == "serveradmin" || role == "setupadmin" || role == "securityadmin" {
			return storage.IntValue(1)
		}
		return storage.IntValue(0)

	case "IS_MEMBER":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		role := strings.ToLower(strings.TrimSpace(valToString(args[0])))
		if role == "db_owner" || role == "db_datareader" || role == "db_datawriter" {
			return storage.IntValue(1)
		}
		return storage.IntValue(0)

	case "HAS_PERMS_BY_NAME":
		return storage.IntValue(1)

	case "PWDENCRYPT":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		pw := valToString(args[0])
		h := sha512.Sum512([]byte(pw))
		return storage.StringValue("0x0200" + strings.ToUpper(hex.EncodeToString(h[:])))

	case "PWDCOMPARE":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		pw := valToString(args[0])
		hashStr := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(valToString(args[1]))), "0X")
		if strings.HasPrefix(hashStr, "0200") {
			h := sha512.Sum512([]byte(pw))
			expected := "0200" + strings.ToUpper(hex.EncodeToString(h[:]))
			if hashStr == expected {
				return storage.IntValue(1)
			}
			return storage.IntValue(0)
		}
		h256 := sha256.Sum256([]byte(pw))
		if strings.EqualFold(hashStr, hex.EncodeToString(h256[:])) {
			return storage.IntValue(1)
		}
		return storage.IntValue(0)

	// ----------------------------------------------------
	// 9. Cryptography & Checksum (Group E)
	// ----------------------------------------------------
	case "HASHBYTES":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		algo := strings.ToUpper(strings.TrimSpace(valToString(args[0])))
		data := []byte(valToString(args[1]))
		var hashBytes []byte
		switch algo {
		case "MD5":
			h := md5.Sum(data)
			hashBytes = h[:]
		case "SHA", "SHA1":
			h := sha1.Sum(data)
			hashBytes = h[:]
		case "SHA2_256":
			h := sha256.Sum256(data)
			hashBytes = h[:]
		case "SHA2_512":
			h := sha512.Sum512(data)
			hashBytes = h[:]
		default:
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue("0x" + strings.ToUpper(hex.EncodeToString(hashBytes)))

	case "CHECKSUM":
		if len(args) == 0 {
			return storage.NullValue(storage.TypeInt)
		}
		h := fnv.New32a()
		for _, a := range args {
			if !a.IsNull {
				h.Write([]byte(valToString(a)))
			}
		}
		return storage.IntValue(int64(int32(h.Sum32())))

	case "BINARY_CHECKSUM":
		if len(args) == 0 {
			return storage.NullValue(storage.TypeInt)
		}
		var buf []byte
		for _, a := range args {
			if !a.IsNull {
				buf = append(buf, []byte(valToString(a))...)
			}
		}
		crc := crc32.ChecksumIEEE(buf)
		return storage.IntValue(int64(int32(crc)))

	default:
		return storage.NullValue(storage.TypeUnknown)
	}
}
