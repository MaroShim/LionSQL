package proc

import (
	"strings"
	"testing"

	"sqlion/pkg/storage"
)

func TestBuiltin_DateTime(t *testing.T) {
	// GETDATE / NOW
	nowVal := EvalBuiltin("GETDATE", nil)
	if nowVal.Type != storage.TypeVarChar || len(nowVal.SVal) != 19 {
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


