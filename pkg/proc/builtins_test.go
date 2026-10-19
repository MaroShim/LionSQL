package proc

import (
	"strings"
	"testing"
	"time"

	"sqlion/pkg/storage"
)

func TestBuiltin_DateTime(t *testing.T) {
	// GETDATE / NOW
	nowVal := EvalBuiltin("GETDATE", nil)
	if nowVal.Type != storage.TypeDateTime || nowVal.TVal.IsZero() {
		t.Fatalf("Unexpected GETDATE format: %v", nowVal)
	}

	// YEAR, MONTH, DAY
	d := storage.StringValue("2026-09-23 15:30:00")
	if y := EvalBuiltin("YEAR", []storage.Value{d}); y.IVal != 2026 {
		t.Errorf("Expected year 2026, got %d", y.IVal)
	}
	if m := EvalBuiltin("MONTH", []storage.Value{d}); m.IVal != 9 {
		t.Errorf("Expected month 9, got %d", m.IVal)
	}
	if day := EvalBuiltin("DAY", []storage.Value{d}); day.IVal != 23 {
		t.Errorf("Expected day 23, got %d", day.IVal)
	}

	// DATEPART
	if dp := EvalBuiltin("DATEPART", []storage.Value{storage.StringValue("hour"), d}); dp.IVal != 15 {
		t.Errorf("Expected hour 15, got %d", dp.IVal)
	}
	if dp := EvalBuiltin("DATEPART", []storage.Value{storage.StringValue("minute"), d}); dp.IVal != 30 {
		t.Errorf("Expected minute 30, got %d", dp.IVal)
	}

	// DATEDIFF
	d1 := storage.StringValue("2026-01-01 00:00:00")
	d2 := storage.StringValue("2026-01-10 12:00:00")
	diffDay := EvalBuiltin("DATEDIFF", []storage.Value{storage.StringValue("day"), d1, d2})
	if diffDay.IVal != 9 {
		t.Errorf("Expected 9 days diff, got %d", diffDay.IVal)
	}

	diffMonth := EvalBuiltin("DATEDIFF", []storage.Value{storage.StringValue("month"), storage.StringValue("2025-10-01"), storage.StringValue("2026-02-01")})
	if diffMonth.IVal != 4 {
		t.Errorf("Expected 4 months diff, got %d", diffMonth.IVal)
	}

	// DATEADD
	added := EvalBuiltin("DATEADD", []storage.Value{storage.StringValue("day"), storage.IntValue(5), storage.StringValue("2026-01-01 00:00:00")})
	if added.SVal != "2026-01-06 00:00:00" {
		t.Errorf("Expected 2026-01-06 00:00:00, got %s", added.SVal)
	}

	addedYear := EvalBuiltin("DATEADD", []storage.Value{storage.StringValue("year"), storage.IntValue(2), storage.StringValue("2026-01-01 00:00:00")})
	if addedYear.SVal != "2028-01-01 00:00:00" {
		t.Errorf("Expected 2028-01-01 00:00:00, got %s", addedYear.SVal)
	}
}

func TestBuiltin_Strings(t *testing.T) {
	// CONCAT (with nulls)
	cat := EvalBuiltin("CONCAT", []storage.Value{
		storage.StringValue("Hello"),
		storage.NullValue(storage.TypeVarChar),
		storage.StringValue(" "),
		storage.IntValue(2026),
	})
	if cat.SVal != "Hello 2026" {
		t.Errorf("Expected 'Hello 2026', got %q", cat.SVal)
	}

	// LEFT and RIGHT
	str := storage.StringValue("Antigravity")
	if left := EvalBuiltin("LEFT", []storage.Value{str, storage.IntValue(4)}); left.SVal != "Anti" {
		t.Errorf("Expected 'Anti', got %q", left.SVal)
	}
	if right := EvalBuiltin("RIGHT", []storage.Value{str, storage.IntValue(7)}); right.SVal != "gravity" {
		t.Errorf("Expected 'gravity', got %q", right.SVal)
	}

	// LTRIM / RTRIM / TRIM
	spaced := storage.StringValue("  hello  ")
	if l := EvalBuiltin("LTRIM", []storage.Value{spaced}); l.SVal != "hello  " {
		t.Errorf("Expected 'hello  ', got %q", l.SVal)
	}
	if r := EvalBuiltin("RTRIM", []storage.Value{spaced}); r.SVal != "  hello" {
		t.Errorf("Expected '  hello', got %q", r.SVal)
	}
	if tr := EvalBuiltin("TRIM", []storage.Value{spaced}); tr.SVal != "hello" {
		t.Errorf("Expected 'hello', got %q", tr.SVal)
	}

	// REVERSE
	if rev := EvalBuiltin("REVERSE", []storage.Value{storage.StringValue("abcdef")}); rev.SVal != "fedcba" {
		t.Errorf("Expected 'fedcba', got %q", rev.SVal)
	}

	// CHARINDEX
	target := storage.StringValue("SQLion Database Engine")
	if ci := EvalBuiltin("CHARINDEX", []storage.Value{storage.StringValue("Data"), target}); ci.IVal != 8 {
		t.Errorf("Expected index 8 for 'Data', got %d", ci.IVal)
	}
	if ci := EvalBuiltin("CHARINDEX", []storage.Value{storage.StringValue("xyz"), target}); ci.IVal != 0 {
		t.Errorf("Expected index 0 for non-existing, got %d", ci.IVal)
	}

	// SPACE and REPLICATE
	if sp := EvalBuiltin("SPACE", []storage.Value{storage.IntValue(4)}); sp.SVal != "    " {
		t.Errorf("Expected 4 spaces, got %q", sp.SVal)
	}
	if rep := EvalBuiltin("REPLICATE", []storage.Value{storage.StringValue("ab"), storage.IntValue(3)}); rep.SVal != "ababab" {
		t.Errorf("Expected 'ababab', got %q", rep.SVal)
	}

	// CONCAT_WS
	cws := EvalBuiltin("CONCAT_WS", []storage.Value{
		storage.StringValue(", "),
		storage.StringValue("Apple"),
		storage.NullValue(storage.TypeVarChar),
		storage.StringValue("Banana"),
		storage.StringValue("Cherry"),
	})
	if cws.SVal != "Apple, Banana, Cherry" {
		t.Errorf("Expected 'Apple, Banana, Cherry', got %q", cws.SVal)
	}

	// QUOTENAME
	if q := EvalBuiltin("QUOTENAME", []storage.Value{storage.StringValue("my_table")}); q.SVal != "[my_table]" {
		t.Errorf("Expected '[my_table]', got %q", q.SVal)
	}
	if q := EvalBuiltin("QUOTENAME", []storage.Value{storage.StringValue("col]name")}); q.SVal != "[col]]name]" {
		t.Errorf("Expected '[col]]name]', got %q", q.SVal)
	}
	if q := EvalBuiltin("QUOTENAME", []storage.Value{storage.StringValue("abc"), storage.StringValue("'")}); q.SVal != "'abc'" {
		t.Errorf("Expected ''abc'', got %q", q.SVal)
	}
	if q := EvalBuiltin("QUOTENAME", []storage.Value{storage.StringValue("o'reilly"), storage.StringValue("'")}); q.SVal != "'o''reilly'" {
		t.Errorf("Expected ''o''reilly'', got %q", q.SVal)
	}

	// ASCII and CHAR
	if a := EvalBuiltin("ASCII", []storage.Value{storage.StringValue("A")}); a.IVal != 65 {
		t.Errorf("Expected 65, got %d", a.IVal)
	}
	if ch := EvalBuiltin("CHAR", []storage.Value{storage.IntValue(65)}); ch.SVal != "A" {
		t.Errorf("Expected 'A', got %q", ch.SVal)
	}
}

func TestBuiltin_Math(t *testing.T) {
	// POWER
	if p := EvalBuiltin("POWER", []storage.Value{storage.IntValue(2), storage.IntValue(8)}); p.IVal != 256 {
		t.Errorf("Expected 256, got %v", p)
	}

	// SQRT
	if s := EvalBuiltin("SQRT", []storage.Value{storage.IntValue(49)}); s.FVal != 7.0 {
		t.Errorf("Expected 7.0, got %v", s)
	}

	// SIGN
	if s := EvalBuiltin("SIGN", []storage.Value{storage.IntValue(-42)}); s.IVal != -1 {
		t.Errorf("Expected -1, got %d", s.IVal)
	}
	if s := EvalBuiltin("SIGN", []storage.Value{storage.IntValue(100)}); s.IVal != 1 {
		t.Errorf("Expected 1, got %d", s.IVal)
	}
	if s := EvalBuiltin("SIGN", []storage.Value{storage.IntValue(0)}); s.IVal != 0 {
		t.Errorf("Expected 0, got %d", s.IVal)
	}

	// PI
	if pi := EvalBuiltin("PI", nil); pi.FVal < 3.14 || pi.FVal > 3.15 {
		t.Errorf("Unexpected PI: %f", pi.FVal)
	}

	// ROUND, CEIL, FLOOR
	num := storage.FloatValue(12.3456)
	if r := EvalBuiltin("ROUND", []storage.Value{num, storage.IntValue(2)}); r.FVal != 12.35 {
		t.Errorf("Expected 12.35, got %f", r.FVal)
	}
	if c := EvalBuiltin("CEILING", []storage.Value{num}); c.IVal != 13 {
		t.Errorf("Expected 13, got %d", c.IVal)
	}
	if fl := EvalBuiltin("FLOOR", []storage.Value{num}); fl.IVal != 12 {
		t.Errorf("Expected 12, got %d", fl.IVal)
	}
}

func TestBuiltin_Logic(t *testing.T) {
	// IIF
	iifTrue := EvalBuiltin("IIF", []storage.Value{storage.BoolValue(true), storage.StringValue("YES"), storage.StringValue("NO")})
	if iifTrue.SVal != "YES" {
		t.Errorf("Expected 'YES', got %s", iifTrue.SVal)
	}

	iifFalse := EvalBuiltin("IIF", []storage.Value{storage.IntValue(0), storage.StringValue("YES"), storage.StringValue("NO")})
	if iifFalse.SVal != "NO" {
		t.Errorf("Expected 'NO', got %s", iifFalse.SVal)
	}

	// CHOOSE
	c1 := EvalBuiltin("CHOOSE", []storage.Value{storage.IntValue(2), storage.StringValue("First"), storage.StringValue("Second"), storage.StringValue("Third")})
	if c1.SVal != "Second" {
		t.Errorf("Expected 'Second', got %s", c1.SVal)
	}
	cOut := EvalBuiltin("CHOOSE", []storage.Value{storage.IntValue(99), storage.StringValue("A"), storage.StringValue("B")})
	if !cOut.IsNull {
		t.Errorf("Expected null for out of bounds choose")
	}

	// ISNULL / COALESCE
	coal := EvalBuiltin("COALESCE", []storage.Value{storage.NullValue(storage.TypeVarChar), storage.NullValue(storage.TypeInt), storage.IntValue(777)})
	if coal.IVal != 777 {
		t.Errorf("Expected 777, got %d", coal.IVal)
	}

	// NULLIF
	n1 := EvalBuiltin("NULLIF", []storage.Value{storage.IntValue(10), storage.IntValue(10)})
	if !n1.IsNull {
		t.Errorf("Expected NULL for NULLIF(10, 10), got %v", n1)
	}
	n2 := EvalBuiltin("NULLIF", []storage.Value{storage.IntValue(10), storage.IntValue(20)})
	if n2.IsNull || n2.IVal != 10 {
		t.Errorf("Expected 10 for NULLIF(10, 20), got %v", n2)
	}
	n3 := EvalBuiltin("NULLIF", []storage.Value{storage.StringValue("same"), storage.StringValue("same")})
	if !n3.IsNull {
		t.Errorf("Expected NULL for NULLIF('same', 'same'), got %v", n3)
	}

	// ISNUMERIC
	if isNum := EvalBuiltin("ISNUMERIC", []storage.Value{storage.IntValue(123)}); isNum.IVal != 1 {
		t.Errorf("Expected 1 for int ISNUMERIC, got %d", isNum.IVal)
	}
	if isNum := EvalBuiltin("ISNUMERIC", []storage.Value{storage.StringValue("3.14159")}); isNum.IVal != 1 {
		t.Errorf("Expected 1 for float string ISNUMERIC, got %d", isNum.IVal)
	}
	if isNum := EvalBuiltin("ISNUMERIC", []storage.Value{storage.StringValue("-42")}); isNum.IVal != 1 {
		t.Errorf("Expected 1 for negative string ISNUMERIC, got %d", isNum.IVal)
	}
	if isNum := EvalBuiltin("ISNUMERIC", []storage.Value{storage.StringValue("not_a_number")}); isNum.IVal != 0 {
		t.Errorf("Expected 0 for non-numeric string ISNUMERIC, got %d", isNum.IVal)
	}
	if isNum := EvalBuiltin("ISNUMERIC", []storage.Value{storage.NullValue(storage.TypeVarChar)}); isNum.IVal != 0 {
		t.Errorf("Expected 0 for NULL ISNUMERIC, got %d", isNum.IVal)
	}

	// IsBuiltinFunction verification
	for _, fn := range []string{"CONCAT_WS", "QUOTENAME", "ASCII", "CHAR", "NULLIF", "ISNUMERIC"} {
		if !IsBuiltinFunction(fn) {
			t.Errorf("Expected IsBuiltinFunction(%q) to be true", fn)
		}
	}
}

func TestBuiltin_UUID(t *testing.T) {
	u1 := EvalBuiltin("NEWID", nil)
	u2 := EvalBuiltin("NEWID", nil)
	if u1.Type != storage.TypeVarChar || len(u1.SVal) != 36 {
		t.Fatalf("Invalid UUID format: %s", u1.SVal)
	}
	if u1.SVal == u2.SVal {
		t.Fatalf("NEWID should produce distinct UUIDs: %s vs %s", u1.SVal, u2.SVal)
	}
	// Check RFC 4122 v4 format: 8-4-4-4-12, version nibble 4, variant nibble 8, 9, A, or B
	parts := strings.Split(u1.SVal, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		t.Fatalf("Malformed UUID structure: %s", u1.SVal)
	}
	if parts[2][0] != '4' {
		t.Errorf("Expected version 4, got %c in %s", parts[2][0], u1.SVal)
	}
}

func TestBuiltin_UUIDv7(t *testing.T) {
	u1 := EvalBuiltin("NEWID7", nil)
	u2 := EvalBuiltin("UUIDV7", nil)
	pgU := EvalBuiltin("GEN_RANDOM_UUID", []storage.Value{storage.IntValue(7)})

	for _, u := range []storage.Value{u1, u2, pgU} {
		if u.Type != storage.TypeUUID || len(u.SVal) != 36 {
			t.Fatalf("Invalid UUIDv7 format or type: %v", u)
		}
		parts := strings.Split(u.SVal, "-")
		if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
			t.Fatalf("Malformed UUIDv7 structure: %s", u.SVal)
		}
		// Verify RFC 9562 Version 7: parts[2][0] == '7'
		if parts[2][0] != '7' {
			t.Errorf("Expected version 7, got %c in %s", parts[2][0], u.SVal)
		}
		// Verify RFC 9562 Variant 1: parts[3][0] in [8, 9, A, B]
		vChar := parts[3][0]
		if vChar != '8' && vChar != '9' && vChar != 'A' && vChar != 'B' &&
			vChar != 'a' && vChar != 'b' {
			t.Errorf("Expected variant 1 (8, 9, A, B), got %c in %s", vChar, u.SVal)
		}
	}

	// Verify timestamp in upper 48 bits (first 8+4 = 12 hex chars) is close to current time
	hexTs := strings.ReplaceAll(u1.SVal[:13], "-", "")
	var tsMs int64
	for i := 0; i < len(hexTs); i++ {
		c := hexTs[i]
		var val int64
		if c >= '0' && c <= '9' {
			val = int64(c - '0')
		} else if c >= 'A' && c <= 'F' {
			val = int64(c - 'A' + 10)
		} else if c >= 'a' && c <= 'f' {
			val = int64(c - 'a' + 10)
		}
		tsMs = (tsMs << 4) | val
	}
	nowMs := time.Now().UnixMilli()
	if diff := tsMs - nowMs; diff < -5000 || diff > 5000 {
		t.Errorf("Timestamp drift too large: tsMs=%d nowMs=%d diff=%dms", tsMs, nowMs, diff)
	}

	// Verify Strict Monotonicity within high frequency loop (1,000 UUIDs)
	const count = 1000
	uuids := make([]string, count)
	for i := 0; i < count; i++ {
		uuids[i] = GenerateUUIDv7()
	}
	for i := 0; i < count-1; i++ {
		if strings.Compare(uuids[i], uuids[i+1]) >= 0 {
			t.Fatalf("UUIDv7 monotonicity violated at index %d: %s >= %s", i, uuids[i], uuids[i+1])
		}
	}
}

func TestBuiltin_StringAndDateExpansion(t *testing.T) {
	// 1. STUFF
	res := EvalBuiltin("STUFF", []storage.Value{
		storage.StringValue("abcdef"),
		storage.IntValue(2),
		storage.IntValue(3),
		storage.StringValue("ijklmn"),
	})
	if res.SVal != "aijklmnef" {
		t.Errorf("STUFF failed: expected 'aijklmnef', got %q", res.SVal)
	}

	// 2. CHARINDEX
	ci := EvalBuiltin("CHARINDEX", []storage.Value{
		storage.StringValue("SQL"),
		storage.StringValue("Hello SQLion"),
	})
	if ci.IVal != 7 {
		t.Errorf("CHARINDEX failed: expected 7, got %d", ci.IVal)
	}

	// 3. REPLACE
	rep := EvalBuiltin("REPLACE", []storage.Value{
		storage.StringValue("foo bar foo"),
		storage.StringValue("foo"),
		storage.StringValue("baz"),
	})
	if rep.SVal != "baz bar baz" {
		t.Errorf("REPLACE failed: expected 'baz bar baz', got %q", rep.SVal)
	}

	// 4. REVERSE
	rev := EvalBuiltin("REVERSE", []storage.Value{
		storage.StringValue("Database"),
	})
	if rev.SVal != "esabataD" {
		t.Errorf("REVERSE failed: expected 'esabataD', got %q", rev.SVal)
	}

	// 5. DATEADD
	da := EvalBuiltin("DATEADD", []storage.Value{
		storage.StringValue("day"),
		storage.IntValue(10),
		storage.StringValue("2026-01-01 00:00:00"),
	})
	if da.SVal != "2026-01-11 00:00:00" {
		t.Errorf("DATEADD failed: expected '2026-01-11 00:00:00', got %q", da.SVal)
	}

	// 6. DATEDIFF
	dd := EvalBuiltin("DATEDIFF", []storage.Value{
		storage.StringValue("day"),
		storage.StringValue("2026-01-01"),
		storage.StringValue("2026-01-15"),
	})
	if dd.IVal != 14 {
		t.Errorf("DATEDIFF failed: expected 14, got %d", dd.IVal)
	}
}

func TestBuiltin_DateTruncAndBucket(t *testing.T) {
	ts := storage.StringValue("2026-09-23 15:37:45")

	testsTrunc := []struct {
		part     string
		expected string
	}{
		{"year", "2026-01-01 00:00:00"},
		{"quarter", "2026-07-01 00:00:00"},
		{"month", "2026-09-01 00:00:00"},
		{"day", "2026-09-23 00:00:00"},
		{"hour", "2026-09-23 15:00:00"},
		{"minute", "2026-09-23 15:37:00"},
		{"second", "2026-09-23 15:37:45"},
	}

	for _, tt := range testsTrunc {
		res := EvalBuiltin("DATETRUNC", []storage.Value{storage.StringValue(tt.part), ts})
		if res.SVal != tt.expected {
			t.Errorf("DATETRUNC(%s) = %q, expected %q", tt.part, res.SVal, tt.expected)
		}
	}

	// 5-minute bucket: 15:37:45 falls into 15:35:00
	b5 := EvalBuiltin("DATE_BUCKET", []storage.Value{
		storage.StringValue("minute"),
		storage.IntValue(5),
		ts,
	})
	if b5.SVal != "2026-09-23 15:35:00" {
		t.Errorf("DATE_BUCKET(minute, 5) = %q, expected '2026-09-23 15:35:00'", b5.SVal)
	}

	// 15-minute bucket: 15:37:45 falls into 15:30:00
	b15 := EvalBuiltin("DATE_BUCKET", []storage.Value{
		storage.StringValue("minute"),
		storage.IntValue(15),
		ts,
	})
	if b15.SVal != "2026-09-23 15:30:00" {
		t.Errorf("DATE_BUCKET(minute, 15) = %q, expected '2026-09-23 15:30:00'", b15.SVal)
	}

	// 2-hour bucket: 15:37:45 falls into 14:00:00
	b2h := EvalBuiltin("DATE_BUCKET", []storage.Value{
		storage.StringValue("hour"),
		storage.IntValue(2),
		ts,
	})
	if b2h.SVal != "2026-09-23 14:00:00" {
		t.Errorf("DATE_BUCKET(hour, 2) = %q, expected '2026-09-23 14:00:00'", b2h.SVal)
	}
}

func TestBuiltin_GrepAndAwk(t *testing.T) {
	// 1. GREP regex match
	msg := storage.StringValue("2026-10-02 15:30:00 [ERROR] worker-1: connection pool timeout")
	g1 := EvalBuiltin("GREP", []storage.Value{msg, storage.StringValue("ERROR|FATAL")})
	if !g1.BVal {
		t.Errorf("GREP expected true for 'ERROR|FATAL', got false")
	}

	g2 := EvalBuiltin("GREP", []storage.Value{msg, storage.StringValue("worker-[0-9]+")})
	if !g2.BVal {
		t.Errorf("GREP expected true for 'worker-[0-9]+', got false")
	}

	g3 := EvalBuiltin("GREP", []storage.Value{msg, storage.StringValue("CRITICAL")})
	if g3.BVal {
		t.Errorf("GREP expected false for 'CRITICAL', got true")
	}

	// REGEXP_LIKE alias
	r1 := EvalBuiltin("REGEXP_LIKE", []storage.Value{msg, storage.StringValue("ERROR|FATAL")})
	if !r1.BVal {
		t.Errorf("REGEXP_LIKE expected true, got false")
	}

	// 2. AWK default whitespace delimiter
	logLine := storage.StringValue("192.168.1.100 - user42 [02/Oct/2026] 200 4096")
	awk1 := EvalBuiltin("AWK", []storage.Value{logLine, storage.IntValue(1)})
	if awk1.SVal != "192.168.1.100" {
		t.Errorf("AWK($1) expected '192.168.1.100', got %q", awk1.SVal)
	}

	awk3 := EvalBuiltin("AWK", []storage.Value{logLine, storage.IntValue(3)})
	if awk3.SVal != "user42" {
		t.Errorf("AWK($3) expected 'user42', got %q", awk3.SVal)
	}

	awk5 := EvalBuiltin("AWK", []storage.Value{logLine, storage.IntValue(5)})
	if awk5.SVal != "200" {
		t.Errorf("AWK($5) expected '200', got %q", awk5.SVal)
	}

	// 3. AWK custom delimiter (e.g. CSV, colon)
	csvLine := storage.StringValue("apple,banana,cherry,date")
	awkCsv := EvalBuiltin("AWK", []storage.Value{csvLine, storage.StringValue(","), storage.IntValue(3)})
	if awkCsv.SVal != "cherry" {
		t.Errorf("AWK with comma expected 'cherry', got %q", awkCsv.SVal)
	}

	// 4. SPLIT_PART alias
	sp := EvalBuiltin("SPLIT_PART", []storage.Value{csvLine, storage.StringValue(","), storage.IntValue(2)})
	if sp.SVal != "banana" {
		t.Errorf("SPLIT_PART expected 'banana', got %q", sp.SVal)
	}

	// 5. Out of bounds index
	oob := EvalBuiltin("AWK", []storage.Value{csvLine, storage.StringValue(","), storage.IntValue(10)})
	if oob.SVal != "" {
		t.Errorf("AWK out of bounds expected empty string, got %q", oob.SVal)
	}
}

func TestCustomScalarFunctionSPI(t *testing.T) {
	// Register custom Go function: DOUBLE_INT
	RegisterScalarFunction("DOUBLE_INT", func(args []storage.Value) storage.Value {
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		return storage.IntValue(args[0].IVal * 2)
	})

	if !HasScalarFunction("double_int") {
		t.Fatalf("expected HasScalarFunction to be true (case-insensitive)")
	}

	res := EvalBuiltin("double_int", []storage.Value{storage.IntValue(21)})
	if res.IVal != 42 {
		t.Fatalf("expected DOUBLE_INT(21) = 42, got %d", res.IVal)
	}

	// Register string transform: GREET
	RegisterScalarFunction("GREET", func(args []storage.Value) storage.Value {
		if len(args) == 0 {
			return storage.StringValue("Hello, World!")
		}
		return storage.StringValue("Hello, " + args[0].SVal + "!")
	})

	greetRes := EvalBuiltin("greet", []storage.Value{storage.StringValue("Gopher")})
	if greetRes.SVal != "Hello, Gopher!" {
		t.Fatalf("expected 'Hello, Gopher!', got %q", greetRes.SVal)
	}

	// Unregister
	UnregisterScalarFunction("DOUBLE_INT")
	if HasScalarFunction("double_int") {
		t.Fatalf("expected HasScalarFunction to be false after unregistering")
	}
}

func TestBuiltin_GroupsA_Through_F(t *testing.T) {
	// ----------------------------------------------------
	// Group A: Error Handling & Execution Context
	// ----------------------------------------------------
	if errNum := EvalBuiltin("ERROR_NUMBER", nil); errNum.IVal != 0 {
		t.Errorf("ERROR_NUMBER expected 0, got %d", errNum.IVal)
	}
	if errMsg := EvalBuiltin("ERROR_MESSAGE", nil); errMsg.SVal != "" {
		t.Errorf("ERROR_MESSAGE expected empty string, got %q", errMsg.SVal)
	}
	if errLine := EvalBuiltin("ERROR_LINE", nil); errLine.IVal != 0 {
		t.Errorf("ERROR_LINE expected 0, got %d", errLine.IVal)
	}
	if sev := EvalBuiltin("ERROR_SEVERITY", nil); sev.IVal != 16 {
		t.Errorf("ERROR_SEVERITY expected 16, got %d", sev.IVal)
	}
	if state := EvalBuiltin("ERROR_STATE", nil); state.IVal != 1 {
		t.Errorf("ERROR_STATE expected 1, got %d", state.IVal)
	}
	if procVal := EvalBuiltin("ERROR_PROCEDURE", nil); !procVal.IsNull {
		t.Errorf("ERROR_PROCEDURE expected NULL, got %v", procVal)
	}
	if atErr := EvalBuiltin("@@ERROR", nil); atErr.IVal != 0 {
		t.Errorf("@@ERROR expected 0, got %d", atErr.IVal)
	}

	// ----------------------------------------------------
	// Group B: String Manipulation & Search
	// ----------------------------------------------------
	// PATINDEX
	pat1 := EvalBuiltin("PATINDEX", []storage.Value{storage.StringValue("%ter%"), storage.StringValue("interesting")})
	if pat1.IVal != 3 {
		t.Errorf("PATINDEX('%%ter%%', 'interesting') expected 3, got %d", pat1.IVal)
	}
	pat2 := EvalBuiltin("PATINDEX", []storage.Value{storage.StringValue("%[0-9]%"), storage.StringValue("abc123def")})
	if pat2.IVal != 4 {
		t.Errorf("PATINDEX('%%[0-9]%%', 'abc123def') expected 4, got %d", pat2.IVal)
	}
	pat3 := EvalBuiltin("PATINDEX", []storage.Value{storage.StringValue("in%"), storage.StringValue("interesting")})
	if pat3.IVal != 1 {
		t.Errorf("PATINDEX('in%%', 'interesting') expected 1, got %d", pat3.IVal)
	}
	pat4 := EvalBuiltin("PATINDEX", []storage.Value{storage.StringValue("ter%"), storage.StringValue("interesting")})
	if pat4.IVal != 0 {
		t.Errorf("PATINDEX('ter%%', 'interesting') expected 0, got %d", pat4.IVal)
	}

	// UNICODE & NCHAR
	uni := EvalBuiltin("UNICODE", []storage.Value{storage.StringValue("A")})
	if uni.IVal != 65 {
		t.Errorf("UNICODE('A') expected 65, got %d", uni.IVal)
	}
	nch := EvalBuiltin("NCHAR", []storage.Value{storage.IntValue(65)})
	if nch.SVal != "A" {
		t.Errorf("NCHAR(65) expected 'A', got %q", nch.SVal)
	}

	// SOUNDEX & DIFFERENCE
	snd1 := EvalBuiltin("SOUNDEX", []storage.Value{storage.StringValue("Smith")})
	snd2 := EvalBuiltin("SOUNDEX", []storage.Value{storage.StringValue("Smythe")})
	if snd1.SVal != "S530" {
		t.Errorf("SOUNDEX('Smith') expected 'S530', got %q", snd1.SVal)
	}
	if snd2.SVal != "S530" {
		t.Errorf("SOUNDEX('Smythe') expected 'S530', got %q", snd2.SVal)
	}
	diff := EvalBuiltin("DIFFERENCE", []storage.Value{storage.StringValue("Smith"), storage.StringValue("Smythe")})
	if diff.IVal != 4 {
		t.Errorf("DIFFERENCE('Smith', 'Smythe') expected 4, got %d", diff.IVal)
	}

	// TRANSLATE
	trans := EvalBuiltin("TRANSLATE", []storage.Value{
		storage.StringValue("2*[3+4]/{7-2}"),
		storage.StringValue("[]{}"),
		storage.StringValue("()()"),
	})
	if trans.SVal != "2*(3+4)/(7-2)" {
		t.Errorf("TRANSLATE expected '2*(3+4)/(7-2)', got %q", trans.SVal)
	}

	// FORMAT
	fmtDate := EvalBuiltin("FORMAT", []storage.Value{
		storage.StringValue("2026-10-10 15:30:45"),
		storage.StringValue("yyyy-MM-dd"),
	})
	if fmtDate.SVal != "2026-10-10" {
		t.Errorf("FORMAT date expected '2026-10-10', got %q", fmtDate.SVal)
	}
	fmtNum := EvalBuiltin("FORMAT", []storage.Value{
		storage.FloatValue(12345.67),
		storage.StringValue("N2"),
	})
	if fmtNum.SVal != "12,345.67" {
		t.Errorf("FORMAT number expected '12,345.67', got %q", fmtNum.SVal)
	}

	// ----------------------------------------------------
	// Group C: Advanced Math & Trig Functions
	// ----------------------------------------------------
	expVal := EvalBuiltin("EXP", []storage.Value{storage.IntValue(1)})
	if expVal.FVal < 2.718 || expVal.FVal > 2.719 {
		t.Errorf("EXP(1) expected ~2.718, got %f", expVal.FVal)
	}
	logVal := EvalBuiltin("LOG", []storage.Value{storage.FloatValue(expVal.FVal)})
	if logVal.FVal < 0.99 || logVal.FVal > 1.01 {
		t.Errorf("LOG(e) expected ~1.0, got %f", logVal.FVal)
	}
	log10Val := EvalBuiltin("LOG10", []storage.Value{storage.IntValue(100)})
	if log10Val.FVal != 2.0 {
		t.Errorf("LOG10(100) expected 2.0, got %f", log10Val.FVal)
	}
	sinVal := EvalBuiltin("SIN", []storage.Value{storage.IntValue(0)})
	if sinVal.FVal != 0.0 {
		t.Errorf("SIN(0) expected 0.0, got %f", sinVal.FVal)
	}
	cosVal := EvalBuiltin("COS", []storage.Value{storage.IntValue(0)})
	if cosVal.FVal != 1.0 {
		t.Errorf("COS(0) expected 1.0, got %f", cosVal.FVal)
	}
	tanVal := EvalBuiltin("TAN", []storage.Value{storage.IntValue(0)})
	if tanVal.FVal != 0.0 {
		t.Errorf("TAN(0) expected 0.0, got %f", tanVal.FVal)
	}
	degVal := EvalBuiltin("DEGREES", []storage.Value{storage.FloatValue(3.141592653589793)})
	if degVal.FVal < 179.9 || degVal.FVal > 180.1 {
		t.Errorf("DEGREES(pi) expected 180.0, got %f", degVal.FVal)
	}
	radVal := EvalBuiltin("RADIANS", []storage.Value{storage.IntValue(180)})
	if radVal.FVal < 3.14 || radVal.FVal > 3.15 {
		t.Errorf("RADIANS(180) expected ~3.14, got %f", radVal.FVal)
	}
	randVal := EvalBuiltin("RAND", nil)
	if randVal.FVal < 0.0 || randVal.FVal >= 1.0 {
		t.Errorf("RAND expected in [0, 1), got %f", randVal.FVal)
	}

	// ----------------------------------------------------
	// Group D: Metadata & Schema Introspection
	// ----------------------------------------------------
	objID := EvalBuiltin("OBJECT_ID", []storage.Value{storage.StringValue("Employees")})
	if objID.IsNull || objID.IVal <= 0 {
		t.Errorf("OBJECT_ID('Employees') expected positive ID, got %v", objID)
	}
	colLen := EvalBuiltin("COL_LENGTH", []storage.Value{storage.StringValue("Employees"), storage.StringValue("Name")})
	if colLen.IVal <= 0 {
		t.Errorf("COL_LENGTH expected positive int, got %d", colLen.IVal)
	}
	schName := EvalBuiltin("SCHEMA_NAME", []storage.Value{storage.IntValue(1)})
	if schName.SVal != "dbo" {
		t.Errorf("SCHEMA_NAME expected 'dbo', got %q", schName.SVal)
	}
	dbName := EvalBuiltin("DB_NAME", nil)
	if dbName.SVal != "LionSQL" {
		t.Errorf("DB_NAME expected 'LionSQL', got %q", dbName.SVal)
	}
	appName := EvalBuiltin("APP_NAME", nil)
	if appName.SVal != "LionSQL" {
		t.Errorf("APP_NAME expected 'LionSQL', got %q", appName.SVal)
	}

	// ----------------------------------------------------
	// Group E: Cryptography & Checksum
	// ----------------------------------------------------
	md5Hash := EvalBuiltin("HASHBYTES", []storage.Value{storage.StringValue("MD5"), storage.StringValue("HelloWorld")})
	if md5Hash.SVal != "0x68E109F0F40CA72A15E05CC22786F8E6" {
		t.Errorf("HASHBYTES MD5 expected '0x68E109F0F40CA72A15E05CC22786F8E6', got %q", md5Hash.SVal)
	}
	sha1Hash := EvalBuiltin("HASHBYTES", []storage.Value{storage.StringValue("SHA1"), storage.StringValue("HelloWorld")})
	if !strings.HasPrefix(sha1Hash.SVal, "0x") {
		t.Errorf("HASHBYTES SHA1 expected 0x prefix, got %q", sha1Hash.SVal)
	}
	chk := EvalBuiltin("CHECKSUM", []storage.Value{storage.StringValue("Test"), storage.IntValue(123)})
	if chk.IsNull {
		t.Errorf("CHECKSUM expected non-null int, got null")
	}
	bChk := EvalBuiltin("BINARY_CHECKSUM", []storage.Value{storage.StringValue("Test")})
	if bChk.IsNull {
		t.Errorf("BINARY_CHECKSUM expected non-null int, got null")
	}

	// ----------------------------------------------------
	// Group F: Date Constructors
	// ----------------------------------------------------
	dateParts := EvalBuiltin("DATEFROMPARTS", []storage.Value{
		storage.IntValue(2026),
		storage.IntValue(10),
		storage.IntValue(10),
	})
	if dateParts.SVal != "2026-10-10" {
		t.Errorf("DATEFROMPARTS expected '2026-10-10', got %q", dateParts.SVal)
	}

	timeParts := EvalBuiltin("TIMEFROMPARTS", []storage.Value{
		storage.IntValue(14),
		storage.IntValue(30),
		storage.IntValue(15),
	})
	if timeParts.SVal != "14:30:15" {
		t.Errorf("TIMEFROMPARTS expected '14:30:15', got %q", timeParts.SVal)
	}

	dateTimeParts := EvalBuiltin("DATETIMEFROMPARTS", []storage.Value{
		storage.IntValue(2026),
		storage.IntValue(10),
		storage.IntValue(10),
		storage.IntValue(14),
		storage.IntValue(30),
		storage.IntValue(15),
		storage.IntValue(500),
	})
	if dateTimeParts.SVal != "2026-10-10 14:30:15.500" {
		t.Errorf("DATETIMEFROMPARTS expected '2026-10-10 14:30:15.500', got %q", dateTimeParts.SVal)
	}

	// Verification of IsBuiltinFunction for Groups A~F
	groupFunctions := []string{
		"ERROR_NUMBER", "ERROR_MESSAGE", "ERROR_LINE", "ERROR_SEVERITY", "ERROR_STATE", "ERROR_PROCEDURE",
		"PATINDEX", "UNICODE", "NCHAR", "SOUNDEX", "DIFFERENCE", "TRANSLATE", "FORMAT",
		"EXP", "LOG", "LOG10", "SIN", "COS", "TAN", "COT", "ASIN", "ACOS", "ATAN", "ATN2", "DEGREES", "RADIANS", "RAND",
		"OBJECT_ID", "OBJECT_NAME", "COL_LENGTH", "SCHEMA_NAME", "SCHEMA_ID", "DB_NAME", "DB_ID", "APP_NAME", "IDENT_CURRENT",
		"HASHBYTES", "CHECKSUM", "BINARY_CHECKSUM",
		"DATEFROMPARTS", "TIMEFROMPARTS", "DATETIMEFROMPARTS",
	}
	for _, fn := range groupFunctions {
		if !IsBuiltinFunction(fn) {
			t.Errorf("Expected IsBuiltinFunction(%q) to be true", fn)
		}
	}
}

func TestBuiltin_PostgreSQLCompatibility(t *testing.T) {
	// 1. String aliases: SUBSTR, REPEAT, CHR
	if s := EvalBuiltin("SUBSTR", []storage.Value{storage.StringValue("PostgreSQL"), storage.IntValue(1), storage.IntValue(8)}); s.SVal != "PostgreS" {
		t.Errorf("SUBSTR expected 'PostgreS', got %q", s.SVal)
	}
	if r := EvalBuiltin("REPEAT", []storage.Value{storage.StringValue("ha"), storage.IntValue(3)}); r.SVal != "hahaha" {
		t.Errorf("REPEAT expected 'hahaha', got %q", r.SVal)
	}
	if ch := EvalBuiltin("CHR", []storage.Value{storage.IntValue(65)}); ch.SVal != "A" {
		t.Errorf("CHR(65) expected 'A', got %q", ch.SVal)
	}

	// 2. STRPOS(string, substring)
	if pos := EvalBuiltin("STRPOS", []storage.Value{storage.StringValue("high"), storage.StringValue("ig")}); pos.IVal != 2 {
		t.Errorf("STRPOS('high', 'ig') expected 2, got %d", pos.IVal)
	}
	if pos := EvalBuiltin("STRPOS", []storage.Value{storage.StringValue("high"), storage.StringValue("xyz")}); pos.IVal != 0 {
		t.Errorf("STRPOS('high', 'xyz') expected 0, got %d", pos.IVal)
	}

	// 3. MD5(string) -> 32-char lowercase hex
	if md5Val := EvalBuiltin("MD5", []storage.Value{storage.StringValue("hello")}); md5Val.SVal != "5d41402abc4b2a76b9719d911017c592" {
		t.Errorf("MD5('hello') expected '5d41402abc4b2a76b9719d911017c592', got %q", md5Val.SVal)
	}

	// 4. LPAD and RPAD
	if lp := EvalBuiltin("LPAD", []storage.Value{storage.StringValue("hi"), storage.IntValue(5), storage.StringValue("xy")}); lp.SVal != "xyxhi" {
		t.Errorf("LPAD expected 'xyxhi', got %q", lp.SVal)
	}
	if rp := EvalBuiltin("RPAD", []storage.Value{storage.StringValue("hi"), storage.IntValue(5), storage.StringValue("xy")}); rp.SVal != "hixyx" {
		t.Errorf("RPAD expected 'hixyx', got %q", rp.SVal)
	}
	if lpTrunc := EvalBuiltin("LPAD", []storage.Value{storage.StringValue("hello"), storage.IntValue(3)}); lpTrunc.SVal != "hel" {
		t.Errorf("LPAD truncation expected 'hel', got %q", lpTrunc.SVal)
	}

	// 5. OCTET_LENGTH and BIT_LENGTH
	if oct := EvalBuiltin("OCTET_LENGTH", []storage.Value{storage.StringValue("jose")}); oct.IVal != 4 {
		t.Errorf("OCTET_LENGTH expected 4, got %d", oct.IVal)
	}
	if bit := EvalBuiltin("BIT_LENGTH", []storage.Value{storage.StringValue("jose")}); bit.IVal != 32 {
		t.Errorf("BIT_LENGTH expected 32, got %d", bit.IVal)
	}

	// 6. INITCAP
	if ic := EvalBuiltin("INITCAP", []storage.Value{storage.StringValue("hi tHErE")}); ic.SVal != "Hi There" {
		t.Errorf("INITCAP expected 'Hi There', got %q", ic.SVal)
	}

	// 7. TO_CHAR
	if tc := EvalBuiltin("TO_CHAR", []storage.Value{storage.StringValue("2026-10-10 15:30:45"), storage.StringValue("yyyy-MM-dd")}); tc.SVal != "2026-10-10" {
		t.Errorf("TO_CHAR expected '2026-10-10', got %q", tc.SVal)
	}

	// 8. TO_DATE and TO_TIMESTAMP
	if td := EvalBuiltin("TO_DATE", []storage.Value{storage.StringValue("2026-10-10 15:30:45")}); td.SVal != "2026-10-10" {
		t.Errorf("TO_DATE expected '2026-10-10', got %q", td.SVal)
	}
	if tt := EvalBuiltin("TO_TIMESTAMP", []storage.Value{storage.IntValue(1600000000)}); tt.SVal != "2020-09-13 12:26:40" {
		t.Errorf("TO_TIMESTAMP epoch expected '2020-09-13 12:26:40', got %q", tt.SVal)
	}

	// 9. DATE_PART and EXTRACT
	d := storage.StringValue("2026-09-23 15:30:00")
	if dp := EvalBuiltin("DATE_PART", []storage.Value{storage.StringValue("month"), d}); dp.IVal != 9 {
		t.Errorf("DATE_PART month expected 9, got %d", dp.IVal)
	}
	if ext := EvalBuiltin("EXTRACT", []storage.Value{storage.StringValue("year"), d}); ext.IVal != 2026 {
		t.Errorf("EXTRACT year expected 2026, got %d", ext.IVal)
	}

	// 10. QUOTE_IDENT and QUOTE_LITERAL
	if qi := EvalBuiltin("QUOTE_IDENT", []storage.Value{storage.StringValue("user_table")}); qi.SVal != "\"user_table\"" {
		t.Errorf("QUOTE_IDENT expected '\"user_table\"', got %q", qi.SVal)
	}
	if ql := EvalBuiltin("QUOTE_LITERAL", []storage.Value{storage.StringValue("O'Reilly")}); ql.SVal != "'O''Reilly'" {
		t.Errorf("QUOTE_LITERAL expected ''O''Reilly'', got %q", ql.SVal)
	}

	// 11. RANDOM()
	rnd := EvalBuiltin("RANDOM", nil)
	if rnd.FVal < 0.0 || rnd.FVal >= 1.0 {
		t.Errorf("RANDOM expected in [0, 1), got %f", rnd.FVal)
	}

	// Verification of IsBuiltinFunction for PG functions
	pgFuncs := []string{
		"SUBSTR", "REPEAT", "CHR", "STRPOS", "MD5", "LPAD", "RPAD",
		"OCTET_LENGTH", "BIT_LENGTH", "INITCAP", "TO_CHAR", "TO_DATE", "TO_TIMESTAMP",
		"DATE_PART", "EXTRACT", "QUOTE_IDENT", "QUOTE_LITERAL", "RANDOM",
	}
	for _, fn := range pgFuncs {
		if !IsBuiltinFunction(fn) {
			t.Errorf("Expected IsBuiltinFunction(%q) to be true", fn)
		}
	}
}

func TestBuiltin_TSQLExpansions(t *testing.T) {
	// 1. DATENAME
	d := storage.StringValue("2026-10-11 15:30:45")
	if dn := EvalBuiltin("DATENAME", []storage.Value{storage.StringValue("month"), d}); dn.SVal != "October" {
		t.Errorf("DATENAME month expected 'October', got %q", dn.SVal)
	}
	if dn := EvalBuiltin("DATENAME", []storage.Value{storage.StringValue("weekday"), d}); dn.SVal != "Sunday" {
		t.Errorf("DATENAME weekday expected 'Sunday', got %q", dn.SVal)
	}
	if dn := EvalBuiltin("DATENAME", []storage.Value{storage.StringValue("year"), d}); dn.SVal != "2026" {
		t.Errorf("DATENAME year expected '2026', got %q", dn.SVal)
	}
	if dn := EvalBuiltin("DATENAME", []storage.Value{storage.StringValue("day"), d}); dn.SVal != "11" {
		t.Errorf("DATENAME day expected '11', got %q", dn.SVal)
	}

	// 2. High-precision and timestamp constructors
	ts := EvalBuiltin("CURRENT_TIMESTAMP", nil)
	if ts.Type != storage.TypeDateTime || ts.TVal.IsZero() {
		t.Errorf("CURRENT_TIMESTAMP expected TypeDateTime, got %v", ts)
	}
	utc := EvalBuiltin("SYSUTCDATETIME", nil)
	if utc.Type != storage.TypeDateTime || utc.TVal.IsZero() {
		t.Errorf("SYSUTCDATETIME expected TypeDateTime, got %v", utc)
	}
	offset := EvalBuiltin("SYSDATETIMEOFFSET", nil)
	if offset.Type != storage.TypeVarChar || !strings.Contains(offset.SVal, ":") {
		t.Errorf("SYSDATETIMEOFFSET expected timezone offset, got %q", offset.SVal)
	}
	sdt := EvalBuiltin("SMALLDATETIMEFROMPARTS", []storage.Value{
		storage.IntValue(2026), storage.IntValue(10), storage.IntValue(11),
		storage.IntValue(15), storage.IntValue(30),
	})
	if sdt.SVal != "2026-10-11 15:30:00" {
		t.Errorf("SMALLDATETIMEFROMPARTS expected '2026-10-11 15:30:00', got %q", sdt.SVal)
	}

	// 3. DATEDIFF_BIG
	d1 := storage.StringValue("2026-01-01")
	d2 := storage.StringValue("2026-01-10")
	if diff := EvalBuiltin("DATEDIFF_BIG", []storage.Value{storage.StringValue("day"), d1, d2}); diff.IVal != 9 {
		t.Errorf("DATEDIFF_BIG expected 9, got %d", diff.IVal)
	}

	// 4. DATALENGTH vs LEN
	paddedStr := storage.StringValue("hello   ")
	if dl := EvalBuiltin("DATALENGTH", []storage.Value{paddedStr}); dl.IVal != 8 {
		t.Errorf("DATALENGTH expected 8 for 'hello   ', got %d", dl.IVal)
	}
	if dl := EvalBuiltin("DATALENGTH", []storage.Value{storage.IntValue(100)}); dl.IVal != 8 {
		t.Errorf("DATALENGTH expected 8 for int, got %d", dl.IVal)
	}

	// 5. PARSENAME
	if p := EvalBuiltin("PARSENAME", []storage.Value{storage.StringValue("dbo.Customers"), storage.IntValue(1)}); p.SVal != "Customers" {
		t.Errorf("PARSENAME piece 1 expected 'Customers', got %q", p.SVal)
	}
	if p := EvalBuiltin("PARSENAME", []storage.Value{storage.StringValue("dbo.Customers"), storage.IntValue(2)}); p.SVal != "dbo" {
		t.Errorf("PARSENAME piece 2 expected 'dbo', got %q", p.SVal)
	}
	if p := EvalBuiltin("PARSENAME", []storage.Value{storage.StringValue("dbo.Customers"), storage.IntValue(3)}); !p.IsNull {
		t.Errorf("PARSENAME piece 3 expected NULL, got %v", p)
	}
	if p := EvalBuiltin("PARSENAME", []storage.Value{storage.StringValue("[server].[db].[dbo].[Customers]"), storage.IntValue(4)}); p.SVal != "server" {
		t.Errorf("PARSENAME piece 4 expected 'server', got %q", p.SVal)
	}

	// 6. STRING_ESCAPE
	rawJSONText := storage.StringValue("Line 1\nLine 2\t\"quoted\"")
	if esc := EvalBuiltin("STRING_ESCAPE", []storage.Value{rawJSONText, storage.StringValue("json")}); esc.SVal != "Line 1\\nLine 2\\t\\\"quoted\\\"" {
		t.Errorf("STRING_ESCAPE expected escaped JSON, got %q", esc.SVal)
	}

	// 7. SQUARE
	if sq := EvalBuiltin("SQUARE", []storage.Value{storage.IntValue(5)}); sq.IVal != 25 {
		t.Errorf("SQUARE(5) expected 25, got %d", sq.IVal)
	}
	if sq := EvalBuiltin("SQUARE", []storage.Value{storage.FloatValue(2.5)}); sq.FVal != 6.25 {
		t.Errorf("SQUARE(2.5) expected 6.25, got %f", sq.FVal)
	}

	// 8. ISJSON & JSON_MODIFY
	validJSON := storage.StringValue(`{"name":"Alice","age":30}`)
	if isJ := EvalBuiltin("ISJSON", []storage.Value{validJSON}); isJ.IVal != 1 {
		t.Errorf("ISJSON valid expected 1, got %d", isJ.IVal)
	}
	if isJ := EvalBuiltin("ISJSON", []storage.Value{validJSON, storage.StringValue("OBJECT")}); isJ.IVal != 1 {
		t.Errorf("ISJSON OBJECT expected 1, got %d", isJ.IVal)
	}
	if isJ := EvalBuiltin("ISJSON", []storage.Value{validJSON, storage.StringValue("ARRAY")}); isJ.IVal != 0 {
		t.Errorf("ISJSON ARRAY expected 0, got %d", isJ.IVal)
	}
	if isJ := EvalBuiltin("ISJSON", []storage.Value{storage.StringValue("invalid json")}); isJ.IVal != 0 {
		t.Errorf("ISJSON invalid expected 0, got %d", isJ.IVal)
	}
	modified := EvalBuiltin("JSON_MODIFY", []storage.Value{validJSON, storage.StringValue("$.age"), storage.IntValue(31)})
	if !strings.Contains(modified.SVal, `"age":31`) {
		t.Errorf("JSON_MODIFY expected updated age:31, got %q", modified.SVal)
	}

	// 9. System / Security Context
	if un := EvalBuiltin("USER_NAME", nil); un.SVal != "dbo" {
		t.Errorf("USER_NAME expected 'dbo', got %q", un.SVal)
	}
	if cu := EvalBuiltin("CURRENT_USER", nil); cu.SVal != "dbo" {
		t.Errorf("CURRENT_USER expected 'dbo', got %q", cu.SVal)
	}
	if sn := EvalBuiltin("SUSER_NAME", nil); sn.SVal != "sa" {
		t.Errorf("SUSER_NAME expected 'sa', got %q", sn.SVal)
	}
	if uid := EvalBuiltin("USER_ID", nil); uid.IVal != 1 {
		t.Errorf("USER_ID expected 1, got %d", uid.IVal)
	}
	if sid := EvalBuiltin("SUSER_ID", nil); sid.IVal != 1 {
		t.Errorf("SUSER_ID expected 1, got %d", sid.IVal)
	}
	if ver := EvalBuiltin("@@VERSION", nil); !strings.Contains(ver.SVal, "Microsoft SQL Server") {
		t.Errorf("@@VERSION expected SQL Server header, got %q", ver.SVal)
	}
	if srv := EvalBuiltin("@@SERVERNAME", nil); srv.SVal != "LIONSQL" {
		t.Errorf("@@SERVERNAME expected 'LIONSQL', got %q", srv.SVal)
	}
	if spid := EvalBuiltin("@@SPID", nil); spid.IVal != 51 {
		t.Errorf("@@SPID expected 51, got %d", spid.IVal)
	}
	if col := EvalBuiltin("COL_NAME", []storage.Value{storage.IntValue(1), storage.IntValue(1)}); col.SVal != "id" {
		t.Errorf("COL_NAME expected 'id', got %q", col.SVal)
	}

	// 10. IsBuiltinFunction verification
	newFuncs := []string{
		"DATENAME", "CURRENT_TIMESTAMP", "SYSUTCDATETIME", "SYSDATETIMEOFFSET",
		"SMALLDATETIMEFROMPARTS", "DATEDIFF_BIG", "DATALENGTH", "PARSENAME",
		"STRING_ESCAPE", "SQUARE", "ISJSON", "JSON_MODIFY",
		"HOST_NAME", "HOST_ID", "CURRENT_USER", "USER_NAME", "SUSER_NAME", "SUSER_SNAME",
		"USER_ID", "SUSER_ID", "@@VERSION", "VERSION", "@@SERVERNAME", "@@SPID", "SPID",
		"@@MAX_CONNECTIONS", "@@LANGUAGE", "COL_NAME",
	}
	for _, fn := range newFuncs {
		if !IsBuiltinFunction(fn) {
			t.Errorf("Expected IsBuiltinFunction(%q) to be true", fn)
		}
	}
}

func TestBuiltin_AdvancedTSQLFunctions(t *testing.T) {
	// 1. GREATEST & LEAST
	g := EvalBuiltin("GREATEST", []storage.Value{storage.IntValue(10), storage.IntValue(50), storage.IntValue(30)})
	if g.IVal != 50 {
		t.Errorf("GREATEST(10, 50, 30) expected 50, got %d", g.IVal)
	}
	l := EvalBuiltin("LEAST", []storage.Value{storage.IntValue(10), storage.IntValue(50), storage.IntValue(30)})
	if l.IVal != 10 {
		t.Errorf("LEAST(10, 50, 30) expected 10, got %d", l.IVal)
	}
	gStr := EvalBuiltin("GREATEST", []storage.Value{storage.StringValue("apple"), storage.StringValue("zebra"), storage.StringValue("banana")})
	if gStr.SVal != "zebra" {
		t.Errorf("GREATEST strings expected 'zebra', got %q", gStr.SVal)
	}

	// 2. FORMATMESSAGE
	fm := EvalBuiltin("FORMATMESSAGE", []storage.Value{
		storage.StringValue("Order %s has %d items totaling $%s"),
		storage.StringValue("ORD-999"),
		storage.IntValue(3),
		storage.StringValue("150.00"),
	})
	if fm.SVal != "Order ORD-999 has 3 items totaling $150.00" {
		t.Errorf("FORMATMESSAGE expected formatted string, got %q", fm.SVal)
	}

	// 3. Bit Manipulation Functions
	if bc := EvalBuiltin("BIT_COUNT", []storage.Value{storage.IntValue(7)}); bc.IVal != 3 {
		t.Errorf("BIT_COUNT(7) expected 3, got %d", bc.IVal)
	}
	if gb := EvalBuiltin("GET_BIT", []storage.Value{storage.IntValue(4), storage.IntValue(2)}); gb.IVal != 1 {
		t.Errorf("GET_BIT(4, 2) expected 1, got %d", gb.IVal)
	}
	if gb := EvalBuiltin("GET_BIT", []storage.Value{storage.IntValue(4), storage.IntValue(1)}); gb.IVal != 0 {
		t.Errorf("GET_BIT(4, 1) expected 0, got %d", gb.IVal)
	}
	if sb := EvalBuiltin("SET_BIT", []storage.Value{storage.IntValue(0), storage.IntValue(3), storage.IntValue(1)}); sb.IVal != 8 {
		t.Errorf("SET_BIT(0, 3, 1) expected 8, got %d", sb.IVal)
	}
	if ls := EvalBuiltin("LEFT_SHIFT", []storage.Value{storage.IntValue(1), storage.IntValue(4)}); ls.IVal != 16 {
		t.Errorf("LEFT_SHIFT(1, 4) expected 16, got %d", ls.IVal)
	}
	if rs := EvalBuiltin("RIGHT_SHIFT", []storage.Value{storage.IntValue(16), storage.IntValue(4)}); rs.IVal != 1 {
		t.Errorf("RIGHT_SHIFT(16, 4) expected 1, got %d", rs.IVal)
	}

	// 4. Timezone & Datetimeoffset
	tDate := storage.StringValue("2026-10-11 15:30:00")
	toDto := EvalBuiltin("TODATETIMEOFFSET", []storage.Value{tDate, storage.StringValue("+09:00")})
	if !strings.Contains(toDto.SVal, "+09:00") {
		t.Errorf("TODATETIMEOFFSET expected +09:00 offset, got %q", toDto.SVal)
	}
	sw := EvalBuiltin("SWITCHOFFSET", []storage.Value{toDto, storage.StringValue("-05:00")})
	if !strings.Contains(sw.SVal, "-05:00") {
		t.Errorf("SWITCHOFFSET expected -05:00 offset, got %q", sw.SVal)
	}

	// 5. Metadata & Property Functions
	if dbProp := EvalBuiltin("DATABASEPROPERTYEX", []storage.Value{storage.StringValue("SQLion"), storage.StringValue("Status")}); dbProp.SVal != "ONLINE" {
		t.Errorf("DATABASEPROPERTYEX Status expected 'ONLINE', got %q", dbProp.SVal)
	}
	if objProp := EvalBuiltin("OBJECTPROPERTY", []storage.Value{storage.IntValue(1), storage.StringValue("IsTable")}); objProp.IVal != 1 {
		t.Errorf("OBJECTPROPERTY IsTable expected 1, got %d", objProp.IVal)
	}
	if colProp := EvalBuiltin("COLUMNPROPERTY", []storage.Value{storage.IntValue(1), storage.StringValue("id"), storage.StringValue("IsIdentity")}); colProp.IVal != 1 {
		t.Errorf("COLUMNPROPERTY IsIdentity expected 1, got %d", colProp.IVal)
	}
	if idxProp := EvalBuiltin("INDEXPROPERTY", []storage.Value{storage.IntValue(1), storage.StringValue("idx_pk"), storage.StringValue("IsUnique")}); idxProp.IVal != 1 {
		t.Errorf("INDEXPROPERTY IsUnique expected 1, got %d", idxProp.IVal)
	}
	if schema := EvalBuiltin("OBJECT_SCHEMA_NAME", []storage.Value{storage.IntValue(1)}); schema.SVal != "dbo" {
		t.Errorf("OBJECT_SCHEMA_NAME expected 'dbo', got %q", schema.SVal)
	}
	if tid := EvalBuiltin("TYPE_ID", []storage.Value{storage.StringValue("int")}); tid.IVal != 56 {
		t.Errorf("TYPE_ID('int') expected 56, got %d", tid.IVal)
	}
	if tname := EvalBuiltin("TYPE_NAME", []storage.Value{storage.IntValue(56)}); tname.SVal != "int" {
		t.Errorf("TYPE_NAME(56) expected 'int', got %q", tname.SVal)
	}

	// 6. Security & Permission Functions
	if srvRole := EvalBuiltin("IS_SRVROLEMEMBER", []storage.Value{storage.StringValue("sysadmin")}); srvRole.IVal != 1 {
		t.Errorf("IS_SRVROLEMEMBER('sysadmin') expected 1, got %d", srvRole.IVal)
	}
	if dbRole := EvalBuiltin("IS_MEMBER", []storage.Value{storage.StringValue("db_owner")}); dbRole.IVal != 1 {
		t.Errorf("IS_MEMBER('db_owner') expected 1, got %d", dbRole.IVal)
	}
	if perm := EvalBuiltin("HAS_PERMS_BY_NAME", []storage.Value{storage.StringValue("accounts"), storage.StringValue("OBJECT"), storage.StringValue("SELECT")}); perm.IVal != 1 {
		t.Errorf("HAS_PERMS_BY_NAME expected 1, got %d", perm.IVal)
	}
	enc := EvalBuiltin("PWDENCRYPT", []storage.Value{storage.StringValue("Secret123!")})
	if !strings.HasPrefix(enc.SVal, "0x0200") {
		t.Errorf("PWDENCRYPT expected 0x0200 header, got %q", enc.SVal)
	}
	if cmp := EvalBuiltin("PWDCOMPARE", []storage.Value{storage.StringValue("Secret123!"), enc}); cmp.IVal != 1 {
		t.Errorf("PWDCOMPARE valid password expected 1, got %d", cmp.IVal)
	}
	if cmp := EvalBuiltin("PWDCOMPARE", []storage.Value{storage.StringValue("WrongPassword"), enc}); cmp.IVal != 0 {
		t.Errorf("PWDCOMPARE wrong password expected 0, got %d", cmp.IVal)
	}

	// 7. Global Status Functions
	if tc := EvalBuiltin("@@TRANCOUNT", nil); tc.IVal != 0 {
		t.Errorf("@@TRANCOUNT expected 0, got %d", tc.IVal)
	}
	if lt := EvalBuiltin("@@LOCK_TIMEOUT", nil); lt.IVal != -1 {
		t.Errorf("@@LOCK_TIMEOUT expected -1, got %d", lt.IVal)
	}
	if opt := EvalBuiltin("@@OPTIONS", nil); opt.IVal != 5496 {
		t.Errorf("@@OPTIONS expected 5496, got %d", opt.IVal)
	}
	if nl := EvalBuiltin("@@NESTLEVEL", nil); nl.IVal != 0 {
		t.Errorf("@@NESTLEVEL expected 0, got %d", nl.IVal)
	}

	// 8. IsBuiltinFunction verification
	suite := []string{
		"GREATEST", "LEAST", "FORMATMESSAGE",
		"BIT_COUNT", "GET_BIT", "SET_BIT", "LEFT_SHIFT", "RIGHT_SHIFT",
		"SWITCHOFFSET", "TODATETIMEOFFSET",
		"DATABASEPROPERTYEX", "OBJECTPROPERTY", "COLUMNPROPERTY", "INDEXPROPERTY",
		"OBJECT_SCHEMA_NAME", "TYPE_ID", "TYPE_NAME",
		"IS_SRVROLEMEMBER", "IS_MEMBER", "HAS_PERMS_BY_NAME", "PWDENCRYPT", "PWDCOMPARE",
		"@@TRANCOUNT", "TRANCOUNT", "@@LOCK_TIMEOUT", "LOCK_TIMEOUT", "@@OPTIONS", "OPTIONS",
		"@@NESTLEVEL", "NESTLEVEL", "@@CPU_BUSY", "@@IO_BUSY", "@@IDLE",
	}
	for _, fn := range suite {
		if !IsBuiltinFunction(fn) {
			t.Errorf("Expected IsBuiltinFunction(%q) to be true", fn)
		}
	}
}




