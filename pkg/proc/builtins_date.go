package proc

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"lionsql/pkg/storage"
)

var commonDateFormats = []string{
	"2006-01-02 15:04:05.999999999 -07:00",
	"2006-01-02 15:04:05.9999999 -07:00",
	"2006-01-02 15:04:05 -07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02",
	"2006/01/02 15:04:05",
	"2006/01/02",
	"01/02/2006 15:04:05",
	"01/02/2006",
}

func parseDateValue(v storage.Value) (time.Time, bool) {
	if v.IsNull {
		return time.Time{}, false
	}
	if v.Type == storage.TypeDateTime && !v.TVal.IsZero() {
		return v.TVal, true
	}
	str := strings.TrimSpace(valToString(v))
	if str == "" {
		return time.Time{}, false
	}
	for _, layout := range commonDateFormats {
		if t, err := time.ParseInLocation(layout, str, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func normalizeDatePart(dp string) string {
	s := strings.ToLower(strings.TrimSpace(dp))
	switch s {
	case "year", "yy", "yyyy":
		return "year"
	case "quarter", "qq", "q":
		return "quarter"
	case "month", "mm", "m":
		return "month"
	case "day", "dd", "d":
		return "day"
	case "weekday", "dw", "w":
		return "weekday"
	case "dayofyear", "dy", "y":
		return "dayofyear"
	case "week", "wk", "ww":
		return "week"
	case "hour", "hh":
		return "hour"
	case "minute", "mi", "n":
		return "minute"
	case "second", "ss", "s":
		return "second"
	case "millisecond", "ms":
		return "millisecond"
	default:
		return s
	}
}

func evalDatePart(part string, t time.Time) storage.Value {
	switch part {
	case "year":
		return storage.IntValue(int64(t.Year()))
	case "quarter":
		return storage.IntValue(int64((t.Month()-1)/3 + 1))
	case "month":
		return storage.IntValue(int64(t.Month()))
	case "day":
		return storage.IntValue(int64(t.Day()))
	case "weekday":
		return storage.IntValue(int64(t.Weekday()) + 1)
	case "dayofyear":
		return storage.IntValue(int64(t.YearDay()))
	case "week":
		_, w := t.ISOWeek()
		return storage.IntValue(int64(w))
	case "hour":
		return storage.IntValue(int64(t.Hour()))
	case "minute":
		return storage.IntValue(int64(t.Minute()))
	case "second":
		return storage.IntValue(int64(t.Second()))
	case "millisecond":
		return storage.IntValue(int64(t.Nanosecond() / 1e6))
	default:
		return storage.NullValue(storage.TypeInt)
	}
}

func evalDateDiff(part string, t1, t2 time.Time) storage.Value {
	switch part {
	case "year":
		return storage.IntValue(int64(t2.Year() - t1.Year()))
	case "quarter":
		q1 := (t1.Year() * 4) + int((t1.Month()-1)/3)
		q2 := (t2.Year() * 4) + int((t2.Month()-1)/3)
		return storage.IntValue(int64(q2 - q1))
	case "month":
		m1 := t1.Year()*12 + int(t1.Month())
		m2 := t2.Year()*12 + int(t2.Month())
		return storage.IntValue(int64(m2 - m1))
	case "day":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Hours() / 24))
	case "week":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Hours() / (24 * 7)))
	case "hour":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Hours()))
	case "minute":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Minutes()))
	case "second":
		diff := t2.Sub(t1)
		return storage.IntValue(int64(diff.Seconds()))
	case "millisecond":
		diff := t2.Sub(t1)
		return storage.IntValue(diff.Milliseconds())
	default:
		return storage.NullValue(storage.TypeInt)
	}
}

func evalDateAdd(part string, n int64, t time.Time) (time.Time, bool) {
	switch part {
	case "year":
		return t.AddDate(int(n), 0, 0), true
	case "quarter":
		return t.AddDate(0, int(n*3), 0), true
	case "month":
		return t.AddDate(0, int(n), 0), true
	case "day", "dayofyear", "weekday":
		return t.AddDate(0, 0, int(n)), true
	case "week":
		return t.AddDate(0, 0, int(n*7)), true
	case "hour":
		return t.Add(time.Duration(n) * time.Hour), true
	case "minute":
		return t.Add(time.Duration(n) * time.Minute), true
	case "second":
		return t.Add(time.Duration(n) * time.Second), true
	case "millisecond":
		return t.Add(time.Duration(n) * time.Millisecond), true
	default:
		return t, false
	}
}

func evalDateTrunc(part string, t time.Time) time.Time {
	switch part {
	case "year":
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
	case "quarter":
		qMonth := time.Month(((int(t.Month())-1)/3)*3 + 1)
		return time.Date(t.Year(), qMonth, 1, 0, 0, 0, 0, t.Location())
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	case "week":
		weekday := int(t.Weekday())
		truncatedDay := t.AddDate(0, 0, -weekday)
		return time.Date(truncatedDay.Year(), truncatedDay.Month(), truncatedDay.Day(), 0, 0, 0, 0, t.Location())
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	case "hour":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	case "minute":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location())
	case "second":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, t.Location())
	case "millisecond":
		return t.Truncate(time.Millisecond)
	default:
		return t
	}
}

func evalDateBucket(part string, width int64, t time.Time, origin time.Time, hasOrigin bool) time.Time {
	if width <= 0 {
		width = 1
	}

	if !hasOrigin {
		switch part {
		case "second":
			secsFromMidnight := int64(t.Hour()*3600 + t.Minute()*60 + t.Second())
			bucketSec := (secsFromMidnight / width) * width
			dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			return dayStart.Add(time.Duration(bucketSec) * time.Second)
		case "minute":
			bucketWidthSec := width * 60
			secsFromMidnight := int64(t.Hour()*3600 + t.Minute()*60 + t.Second())
			bucketSec := (secsFromMidnight / bucketWidthSec) * bucketWidthSec
			dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			return dayStart.Add(time.Duration(bucketSec) * time.Second)
		case "hour":
			bucketWidthSec := width * 3600
			secsFromMidnight := int64(t.Hour()*3600 + t.Minute()*60 + t.Second())
			bucketSec := (secsFromMidnight / bucketWidthSec) * bucketWidthSec
			dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
			return dayStart.Add(time.Duration(bucketSec) * time.Second)
		case "day":
			diffDays := int64(t.YearDay() - 1)
			bucketDay := (diffDays / width) * width
			yearStart := time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
			return yearStart.AddDate(0, 0, int(bucketDay))
		case "week":
			weekday := int(t.Weekday())
			return time.Date(t.Year(), t.Month(), t.Day()-weekday, 0, 0, 0, 0, t.Location())
		case "month":
			m := int64(t.Month() - 1)
			bucketM := (m / width) * width
			return time.Date(t.Year(), time.Month(bucketM+1), 1, 0, 0, 0, 0, t.Location())
		case "year":
			diffYears := int64(t.Year())
			bucketY := (diffYears / width) * width
			return time.Date(int(bucketY), 1, 1, 0, 0, 0, 0, t.Location())
		default:
			return evalDateTrunc(part, t)
		}
	}

	// Custom origin provided: align relative to custom origin
	switch part {
	case "year":
		diff := int64(t.Year() - origin.Year())
		bucketIdx := diff / width
		if diff < 0 && diff%width != 0 {
			bucketIdx--
		}
		return time.Date(origin.Year()+int(bucketIdx*width), origin.Month(), origin.Day(), origin.Hour(), origin.Minute(), origin.Second(), 0, t.Location())
	case "quarter":
		q1 := int64((origin.Year() * 4) + int((origin.Month()-1)/3))
		q2 := int64((t.Year() * 4) + int((t.Month()-1)/3))
		diff := q2 - q1
		bucketIdx := diff / width
		if diff < 0 && diff%width != 0 {
			bucketIdx--
		}
		return origin.AddDate(0, int(bucketIdx*width*3), 0)
	case "month":
		diff := int64((t.Year()-origin.Year())*12 + int(t.Month()-origin.Month()))
		bucketIdx := diff / width
		if diff < 0 && diff%width != 0 {
			bucketIdx--
		}
		totalMonths := bucketIdx * width
		return origin.AddDate(0, int(totalMonths), 0)
	case "week":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketWidthSec := width * (7 * 24 * 3600)
		bucketIdx := diffSec / bucketWidthSec
		if diffSec < 0 && diffSec%bucketWidthSec != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*bucketWidthSec) * time.Second)
	case "day":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketWidthSec := width * (24 * 3600)
		bucketIdx := diffSec / bucketWidthSec
		if diffSec < 0 && diffSec%bucketWidthSec != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*bucketWidthSec) * time.Second)
	case "hour":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketWidthSec := width * 3600
		bucketIdx := diffSec / bucketWidthSec
		if diffSec < 0 && diffSec%bucketWidthSec != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*bucketWidthSec) * time.Second)
	case "minute":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketWidthSec := width * 60
		bucketIdx := diffSec / bucketWidthSec
		if diffSec < 0 && diffSec%bucketWidthSec != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*bucketWidthSec) * time.Second)
	case "second":
		diffSec := int64(t.Sub(origin).Seconds())
		bucketIdx := diffSec / width
		if diffSec < 0 && diffSec%width != 0 {
			bucketIdx--
		}
		return origin.Add(time.Duration(bucketIdx*width) * time.Second)
	default:
		return evalDateTrunc(part, t)
	}
}

func formatDateCustom(t time.Time, format string) string {
	switch strings.ToLower(format) {
	case "d":
		return t.Format("2006-01-02")
	case "s":
		return t.Format("2006-01-02T15:04:05")
	}
	var sb strings.Builder
	runes := []rune(format)
	for i := 0; i < len(runes); {
		r := runes[i]
		j := i + 1
		for j < len(runes) && runes[j] == r {
			j++
		}
		tokenLen := j - i
		matched := true
		switch r {
		case 'y', 'Y':
			if tokenLen >= 4 {
				sb.WriteString(t.Format("2006"))
			} else {
				sb.WriteString(t.Format("06"))
			}
		case 'M':
			if tokenLen >= 4 {
				sb.WriteString(t.Format("January"))
			} else if tokenLen == 3 {
				sb.WriteString(t.Format("Jan"))
			} else if tokenLen == 2 {
				sb.WriteString(t.Format("01"))
			} else {
				sb.WriteString(t.Format("1"))
			}
		case 'd':
			if tokenLen >= 4 {
				sb.WriteString(t.Format("Monday"))
			} else if tokenLen == 3 {
				sb.WriteString(t.Format("Mon"))
			} else if tokenLen == 2 {
				sb.WriteString(t.Format("02"))
			} else {
				sb.WriteString(t.Format("2"))
			}
		case 'H':
			if tokenLen >= 2 {
				sb.WriteString(t.Format("15"))
			} else {
				sb.WriteString(fmt.Sprintf("%d", t.Hour()))
			}
		case 'h':
			h12 := t.Hour() % 12
			if h12 == 0 {
				h12 = 12
			}
			if tokenLen >= 2 {
				sb.WriteString(fmt.Sprintf("%02d", h12))
			} else {
				sb.WriteString(fmt.Sprintf("%d", h12))
			}
		case 'm':
			if tokenLen >= 2 {
				sb.WriteString(t.Format("04"))
			} else {
				sb.WriteString(fmt.Sprintf("%d", t.Minute()))
			}
		case 's':
			if tokenLen >= 2 {
				sb.WriteString(t.Format("05"))
			} else {
				sb.WriteString(fmt.Sprintf("%d", t.Second()))
			}
		case 'f', 'F':
			ms := t.Nanosecond() / 1_000_000
			if tokenLen >= 3 {
				sb.WriteString(fmt.Sprintf("%03d", ms))
			} else {
				sb.WriteString(fmt.Sprintf("%d", ms))
			}
		case 't':
			if t.Hour() >= 12 {
				sb.WriteString("PM")
			} else {
				sb.WriteString("AM")
			}
		default:
			matched = false
		}
		if matched {
			i = j
		} else {
			sb.WriteRune(r)
			i++
		}
	}
	return sb.String()
}


func evalDateName(part string, t time.Time) storage.Value {
	switch part {
	case "year":
		return storage.StringValue(strconv.Itoa(t.Year()))
	case "quarter":
		q := (int(t.Month())-1)/3 + 1
		return storage.StringValue(strconv.Itoa(q))
	case "month":
		return storage.StringValue(t.Month().String())
	case "day":
		return storage.StringValue(strconv.Itoa(t.Day()))
	case "weekday":
		return storage.StringValue(t.Weekday().String())
	case "dayofyear":
		return storage.StringValue(strconv.Itoa(t.YearDay()))
	case "week":
		_, w := t.ISOWeek()
		return storage.StringValue(strconv.Itoa(w))
	case "hour":
		return storage.StringValue(strconv.Itoa(t.Hour()))
	case "minute":
		return storage.StringValue(strconv.Itoa(t.Minute()))
	case "second":
		return storage.StringValue(strconv.Itoa(t.Second()))
	case "millisecond":
		return storage.StringValue(strconv.Itoa(t.Nanosecond() / 1e6))
	default:
		return storage.NullValue(storage.TypeVarChar)
	}
}


func parseOffsetLocation(s string) *time.Location {
	s = strings.TrimSpace(s)
	sign := 1
	if strings.HasPrefix(s, "-") {
		sign = -1
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if !strings.Contains(s, ":") {
		totalMins, _ := strconv.Atoi(s)
		totalMins *= sign
		hours := totalMins / 60
		mins := totalMins % 60
		if mins < 0 {
			mins = -mins
		}
		sec := totalMins * 60
		return time.FixedZone(fmt.Sprintf("%+03d:%02d", hours, mins), sec)
	}
	parts := strings.Split(s, ":")
	hours, _ := strconv.Atoi(parts[0])
	mins := 0
	if len(parts) > 1 {
		mins, _ = strconv.Atoi(parts[1])
	}
	sec := sign * (hours*3600 + mins*60)
	return time.FixedZone(fmt.Sprintf("%+03d:%02d", sign*hours, mins), sec)
}




func isDateBuiltin(name string) bool {
	switch name {
	case "GETDATE", "NOW", "CURRENT_TIMESTAMP", "GETUTCDATE", "SYSDATETIME", "SYSUTCDATETIME", "SYSDATETIMEOFFSET",
		"YEAR", "MONTH", "DAY", "DATEPART", "DATE_PART", "EXTRACT", "DATENAME", "DATEDIFF", "DATEDIFF_BIG", "DATEADD", "EOMONTH", "ISDATE",
		"DATETRUNC", "DATE_TRUNC", "DATE_BUCKET", "DATEBUCKET",
		"DATEFROMPARTS", "TIMEFROMPARTS", "DATETIMEFROMPARTS", "SMALLDATETIMEFROMPARTS", "TO_DATE", "TO_TIMESTAMP",
		"SWITCHOFFSET", "TODATETIMEOFFSET":
		return true
	default:
		return false
	}
}

func evalDateBuiltin(upper string, args []storage.Value) storage.Value {
	switch upper {
	case "GETDATE", "NOW", "CURRENT_TIMESTAMP":
		return storage.DateTimeValue(time.Now())

	case "GETUTCDATE":
		return storage.DateTimeValue(time.Now().UTC())

	case "SYSDATETIME":
		return storage.DateTimeValue(time.Now())

	case "SYSUTCDATETIME":
		return storage.DateTimeValue(time.Now().UTC())

	case "SYSDATETIMEOFFSET":
		return storage.StringValue(time.Now().Format("2006-01-02 15:04:05.0000000 -07:00"))

	case "YEAR":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.IntValue(int64(t.Year()))
		}
		return storage.NullValue(storage.TypeInt)

	case "MONTH":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.IntValue(int64(t.Month()))
		}
		return storage.NullValue(storage.TypeInt)

	case "DAY":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.IntValue(int64(t.Day()))
		}
		return storage.NullValue(storage.TypeInt)

	case "DATEPART", "DATE_PART", "EXTRACT":
		if len(args) < 2 || args[1].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		part := normalizeDatePart(valToString(args[0]))
		t, ok := parseDateValue(args[1])
		if !ok {
			return storage.NullValue(storage.TypeInt)
		}
		return evalDatePart(part, t)

	case "DATENAME":
		if len(args) < 2 || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		part := normalizeDatePart(valToString(args[0]))
		t, ok := parseDateValue(args[1])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		return evalDateName(part, t)

	case "DATEDIFF":
		if len(args) < 3 || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		part := normalizeDatePart(valToString(args[0]))
		t1, ok1 := parseDateValue(args[1])
		t2, ok2 := parseDateValue(args[2])
		if !ok1 || !ok2 {
			return storage.NullValue(storage.TypeInt)
		}
		return evalDateDiff(part, t1, t2)

	case "DATEDIFF_BIG":
		if len(args) < 3 || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeInt)
		}
		part := normalizeDatePart(valToString(args[0]))
		t1, ok1 := parseDateValue(args[1])
		t2, ok2 := parseDateValue(args[2])
		if !ok1 || !ok2 {
			return storage.NullValue(storage.TypeInt)
		}
		return evalDateDiff(part, t1, t2)

	case "DATEADD":
		if len(args) < 3 || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		part := normalizeDatePart(valToString(args[0]))
		n := args[1].IVal
		if args[1].Type == storage.TypeFloat {
			n = int64(args[1].FVal)
		} else if args[1].Type == storage.TypeVarChar {
			if parsedN, err := strconv.ParseInt(strings.TrimSpace(args[1].SVal), 10, 64); err == nil {
				n = parsedN
			} else {
				return storage.NullValue(storage.TypeVarChar)
			}
		}
		t, ok := parseDateValue(args[2])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		res, ok := evalDateAdd(part, n, t)
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(res.Format("2006-01-02 15:04:05"))

	case "DATETRUNC", "DATE_TRUNC":
		if len(args) < 2 || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		part := normalizeDatePart(valToString(args[0]))
		t, ok := parseDateValue(args[1])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		res := evalDateTrunc(part, t)
		return storage.StringValue(res.Format("2006-01-02 15:04:05"))

	case "DATE_BUCKET", "DATEBUCKET":
		if len(args) < 3 || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		part := normalizeDatePart(valToString(args[0]))
		width := args[1].IVal
		if args[1].Type == storage.TypeFloat {
			width = int64(args[1].FVal)
		} else if args[1].Type == storage.TypeVarChar {
			if w, err := strconv.ParseInt(strings.TrimSpace(args[1].SVal), 10, 64); err == nil {
				width = w
			}
		}
		t, ok := parseDateValue(args[2])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		var origin time.Time
		hasOrigin := false
		if len(args) >= 4 && !args[3].IsNull {
			if o, ok := parseDateValue(args[3]); ok {
				origin = o
				hasOrigin = true
			}
		}
		res := evalDateBucket(part, width, t, origin, hasOrigin)
		return storage.StringValue(res.Format("2006-01-02 15:04:05"))

	case "EOMONTH":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		if len(args) >= 2 && args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		t, ok := parseDateValue(args[0])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		monthsToAdd := 0
		if len(args) >= 2 {
			if args[1].Type == storage.TypeInt {
				monthsToAdd = int(args[1].IVal)
			} else if args[1].Type == storage.TypeFloat {
				monthsToAdd = int(args[1].FVal)
			} else if args[1].Type == storage.TypeVarChar {
				if m, err := strconv.Atoi(strings.TrimSpace(args[1].SVal)); err == nil {
					monthsToAdd = m
				}
			}
		}
		target := time.Date(t.Year(), t.Month()+time.Month(monthsToAdd)+1, 1, 0, 0, 0, 0, t.Location())
		lastDay := target.AddDate(0, 0, -1)
		return storage.StringValue(lastDay.Format("2006-01-02"))

	case "ISDATE":
		if len(args) == 0 || args[0].IsNull {
			return storage.IntValue(0)
		}
		_, ok := parseDateValue(args[0])
		if ok {
			return storage.IntValue(1)
		}
		return storage.IntValue(0)

	case "DATEFROMPARTS":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		y, m, d := int(args[0].IVal), int(args[1].IVal), int(args[2].IVal)
		if y < 1 || m < 1 || m > 12 || d < 1 || d > 31 {
			return storage.NullValue(storage.TypeVarChar)
		}
		return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d", y, m, d))

	case "TIMEFROMPARTS":
		if len(args) < 3 || args[0].IsNull || args[1].IsNull || args[2].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		h, m, s := int(args[0].IVal), int(args[1].IVal), int(args[2].IVal)
		frac := int64(0)
		prec := int64(0)
		if len(args) >= 4 && !args[3].IsNull {
			frac = args[3].IVal
		}
		if len(args) >= 5 && !args[4].IsNull {
			prec = args[4].IVal
		}
		if prec > 0 {
			return storage.StringValue(fmt.Sprintf("%02d:%02d:%02d.%0*d", h, m, s, int(prec), frac))
		}
		return storage.StringValue(fmt.Sprintf("%02d:%02d:%02d", h, m, s))

	case "DATETIMEFROMPARTS":
		if len(args) < 7 || args[0].IsNull || args[1].IsNull || args[2].IsNull || args[3].IsNull || args[4].IsNull || args[5].IsNull || args[6].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		y, m, d := int(args[0].IVal), int(args[1].IVal), int(args[2].IVal)
		h, min, s, ms := int(args[3].IVal), int(args[4].IVal), int(args[5].IVal), int(args[6].IVal)
		return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d.%03d", y, m, d, h, min, s, ms))

	case "SMALLDATETIMEFROMPARTS":
		if len(args) < 5 {
			return storage.NullValue(storage.TypeVarChar)
		}
		for i := 0; i < 5; i++ {
			if args[i].IsNull {
				return storage.NullValue(storage.TypeVarChar)
			}
		}
		y, m, d := int(args[0].IVal), int(args[1].IVal), int(args[2].IVal)
		hh, mm := int(args[3].IVal), int(args[4].IVal)
		if m < 1 || m > 12 || d < 1 || d > 31 || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
			return storage.NullValue(storage.TypeVarChar)
		}
		t := time.Date(y, time.Month(m), d, hh, mm, 0, 0, time.UTC)
		return storage.StringValue(t.Format("2006-01-02 15:04:00"))

	case "SWITCHOFFSET":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		t, ok := parseDateValue(args[0])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		loc := parseOffsetLocation(valToString(args[1]))
		shifted := t.In(loc)
		return storage.StringValue(shifted.Format("2006-01-02 15:04:05.0000000 -07:00"))

	case "TODATETIMEOFFSET":
		if len(args) < 2 || args[0].IsNull || args[1].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		t, ok := parseDateValue(args[0])
		if !ok {
			return storage.NullValue(storage.TypeVarChar)
		}
		loc := parseOffsetLocation(valToString(args[1]))
		res := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
		return storage.StringValue(res.Format("2006-01-02 15:04:05.0000000 -07:00"))

	case "TO_DATE":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.StringValue(t.Format("2006-01-02"))
		}
		return storage.NullValue(storage.TypeVarChar)

	case "TO_TIMESTAMP":
		if len(args) == 0 || args[0].IsNull {
			return storage.NullValue(storage.TypeVarChar)
		}
		if n, ok := toFloatNum(args[0]); ok {
			sec := int64(n)
			nsec := int64((n - float64(sec)) * 1e9)
			t := time.Unix(sec, nsec).UTC()
			return storage.StringValue(t.Format("2006-01-02 15:04:05"))
		}
		if t, ok := parseDateValue(args[0]); ok {
			return storage.StringValue(t.Format("2006-01-02 15:04:05"))
		}
		return storage.NullValue(storage.TypeVarChar)

	// ----------------------------------------------------
	default:
		return storage.NullValue(storage.TypeUnknown)
	}
}
