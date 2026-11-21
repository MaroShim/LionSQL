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
	case "microsecond", "mcs":
		return "microsecond"
	case "nanosecond", "ns":
		return "nanosecond"
	case "tzoffset", "tz":
		return "tzoffset"
	case "epoch":
		return "epoch"
	default:
		return s
	}
}

func evalDatePart(part string, t time.Time) storage.Value {
	switch part {
	case "epoch":
		return storage.FloatValue(float64(t.UnixNano()) / 1e9)
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
	case "tzoffset":
		_, offsetSec := t.Zone()
		return storage.IntValue(int64(offsetSec / 60))
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
	case "day", "dayofyear", "weekday":
		d1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 0, 0, 0, 0, time.UTC)
		d2 := time.Date(t2.Year(), t2.Month(), t2.Day(), 0, 0, 0, 0, time.UTC)
		return storage.IntValue(int64(d2.Sub(d1) / (24 * time.Hour)))
	case "week":
		d1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -int(t1.Weekday()))
		d2 := time.Date(t2.Year(), t2.Month(), t2.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -int(t2.Weekday()))
		return storage.IntValue(int64(d2.Sub(d1) / (7 * 24 * time.Hour)))
	case "hour":
		h1 := time.Date(t1.Year(), t1.Month(), t1.Day(), t1.Hour(), 0, 0, 0, time.UTC)
		h2 := time.Date(t2.Year(), t2.Month(), t2.Day(), t2.Hour(), 0, 0, 0, time.UTC)
		return storage.IntValue(int64(h2.Sub(h1) / time.Hour))
	case "minute":
		m1 := time.Date(t1.Year(), t1.Month(), t1.Day(), t1.Hour(), t1.Minute(), 0, 0, time.UTC)
		m2 := time.Date(t2.Year(), t2.Month(), t2.Day(), t2.Hour(), t2.Minute(), 0, 0, time.UTC)
		return storage.IntValue(int64(m2.Sub(m1) / time.Minute))
	case "second":
		s1 := time.Date(t1.Year(), t1.Month(), t1.Day(), t1.Hour(), t1.Minute(), t1.Second(), 0, time.UTC)
		s2 := time.Date(t2.Year(), t2.Month(), t2.Day(), t2.Hour(), t2.Minute(), t2.Second(), 0, time.UTC)
		return storage.IntValue(int64(s2.Sub(s1) / time.Second))
	case "millisecond":
		diff := t2.Sub(t1)
		return storage.IntValue(diff.Milliseconds())
	case "microsecond":
		diff := t2.Sub(t1)
		return storage.IntValue(diff.Microseconds())
	case "nanosecond":
		diff := t2.Sub(t1)
		return storage.IntValue(diff.Nanoseconds())
	default:
		return storage.NullValue(storage.TypeInt)
	}
}

func addMonthsClamped(t time.Time, months int) time.Time {
	curYear := t.Year()
	curMonth0 := int(t.Month()) - 1
	totalMonths := int64(curYear)*12 + int64(curMonth0) + int64(months)

	targetYear := int(totalMonths / 12)
	targetMonth0 := int(totalMonths % 12)
	if targetMonth0 < 0 {
		targetYear -= 1
		targetMonth0 += 12
	}
	m := time.Month(targetMonth0 + 1)
	lastDay := time.Date(targetYear, m+1, 0, 0, 0, 0, 0, t.Location()).Day()
	day := t.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(targetYear, m, day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

func evalDateAdd(part string, n int64, t time.Time) (time.Time, bool) {
	switch part {
	case "year":
		return addMonthsClamped(t, int(n)*12), true
	case "quarter":
		return addMonthsClamped(t, int(n)*3), true
	case "month":
		return addMonthsClamped(t, int(n)), true
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
	case "tzoffset":
		_, offsetSec := t.Zone()
		sign := "+"
		if offsetSec < 0 {
			sign = "-"
			offsetSec = -offsetSec
		}
		offsetMin := offsetSec / 60
		return storage.StringValue(fmt.Sprintf("%s%02d:%02d", sign, offsetMin/60, offsetMin%60))
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
	case "GETDATE", "NOW", "CURRENT_TIMESTAMP", "STATEMENT_TIMESTAMP", "CLOCK_TIMESTAMP", "TIMEOFDAY", "GETUTCDATE", "SYSDATETIME", "SYSUTCDATETIME", "SYSDATETIMEOFFSET",
		"CURDATE", "CURRENT_DATE", "CURTIME", "CURRENT_TIME",
		"YEAR", "MONTH", "DAY", "DATEPART", "DATE_PART", "EXTRACT", "DATENAME", "DATEDIFF", "DATEDIFF_BIG", "DATEADD", "EOMONTH", "ISDATE",
		"DATETRUNC", "DATE_TRUNC", "DATE_BUCKET", "DATEBUCKET",
		"DATEFROMPARTS", "TIMEFROMPARTS", "DATETIMEFROMPARTS", "SMALLDATETIMEFROMPARTS",
		"DATETIME2FROMPARTS", "DATETIMEOFFSETFROMPARTS",
		"TO_DATE", "TO_TIMESTAMP",
		"SWITCHOFFSET", "TODATETIMEOFFSET":
		return true
	default:
		return false
	}
}

func isValidCalendarDate(y, m, d, minYear, maxYear int) bool {
	if y < minYear || y > maxYear || m < 1 || m > 12 || d < 1 || d > 31 {
		return false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t.Year() == y && int(t.Month()) == m && t.Day() == d
}

func isValidTimeOfDay(h, min, s int) bool {
	return h >= 0 && h <= 23 && min >= 0 && min <= 59 && s >= 0 && s <= 59
}

func isValidFractionForPrecision(frac int64, prec int) bool {
	if prec < 0 || prec > 7 || frac < 0 {
		return false
	}
	if prec == 0 {
		return frac == 0
	}
	maxFrac := int64(1)
	for i := 0; i < prec; i++ {
		maxFrac *= 10
	}
	return frac < maxFrac
}

func isValidDatePartUnit(part string, allowEpoch bool) bool {
	switch part {
	case "year", "quarter", "month", "day", "weekday", "dayofyear", "week",
		"hour", "minute", "second", "millisecond", "microsecond", "nanosecond", "tzoffset":
		return true
	case "epoch":
		return allowEpoch
	default:
		return false
	}
}

func evalDateBuiltinWithErr(upper string, args []storage.Value) (storage.Value, error) {
	switch upper {
	case "CURRENT_TIMESTAMP", "NOW", "CURRENT_TIME":
		if len(args) == 1 && !args[0].IsNull {
			prec, ok := toIntNum(args[0])
			if !ok || prec < 0 || prec > 7 {
				return storage.NullValue(storage.TypeDateTime), fmt.Errorf("TIMESTAMP precision (%d) must be between 0 and 7.", prec)
			}
		}

	case "YEAR", "MONTH", "DAY", "EOMONTH":
		if len(args) >= 1 && !args[0].IsNull {
			if _, ok := parseDateValue(args[0]); !ok {
				return storage.NullValue(storage.TypeInt), fmt.Errorf("Msg 241, Level 16, State 1: Conversion failed when converting date and/or time from character string.")
			}
		}

	case "DATEPART", "DATE_PART", "EXTRACT", "DATENAME":
		if len(args) >= 1 {
			rawPart := strings.TrimSpace(valToString(args[0]))
			part := normalizeDatePart(rawPart)
			allowEpoch := upper != "DATENAME"
			if args[0].IsNull || !isValidDatePartUnit(part, allowEpoch) {
				return storage.NullValue(storage.TypeInt), fmt.Errorf("'%s' is not a recognized %s option.", rawPart, strings.ToLower(upper))
			}
		}
		if len(args) >= 2 && !args[1].IsNull {
			if _, ok := parseDateValue(args[1]); !ok {
				return storage.NullValue(storage.TypeInt), fmt.Errorf("Msg 241, Level 16, State 1: Conversion failed when converting date and/or time from character string.")
			}
		}

	case "DATETRUNC", "DATE_TRUNC":
		if len(args) >= 1 {
			rawPart := strings.TrimSpace(valToString(args[0]))
			part := normalizeDatePart(rawPart)
			if args[0].IsNull || !isValidDatePartUnit(part, false) {
				return storage.NullValue(storage.TypeVarChar), fmt.Errorf("'%s' is not a recognized %s option.", rawPart, strings.ToLower(upper))
			}
		}
		if len(args) >= 2 && !args[1].IsNull {
			if _, ok := parseDateValue(args[1]); !ok {
				return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 241, Level 16, State 1: Conversion failed when converting date and/or time from character string.")
			}
		}

	case "DATEADD":
		if len(args) >= 1 {
			rawPart := strings.TrimSpace(valToString(args[0]))
			part := normalizeDatePart(rawPart)
			if args[0].IsNull || !isValidDatePartUnit(part, false) {
				return storage.NullValue(storage.TypeVarChar), fmt.Errorf("'%s' is not a recognized dateadd option.", rawPart)
			}
		}
		if len(args) >= 3 && !args[2].IsNull {
			if _, ok := parseDateValue(args[2]); !ok {
				return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 241, Level 16, State 1: Conversion failed when converting date and/or time from character string.")
			}
		}

	case "DATEDIFF", "DATEDIFF_BIG":
		if len(args) >= 1 {
			rawPart := strings.TrimSpace(valToString(args[0]))
			part := normalizeDatePart(rawPart)
			if args[0].IsNull || !isValidDatePartUnit(part, false) {
				return storage.NullValue(storage.TypeInt), fmt.Errorf("'%s' is not a recognized %s option.", rawPart, strings.ToLower(upper))
			}
		}
		if len(args) >= 2 && !args[1].IsNull {
			if _, ok := parseDateValue(args[1]); !ok {
				return storage.NullValue(storage.TypeInt), fmt.Errorf("Msg 241, Level 16, State 1: Conversion failed when converting date and/or time from character string.")
			}
		}
		if len(args) >= 3 && !args[2].IsNull {
			if _, ok := parseDateValue(args[2]); !ok {
				return storage.NullValue(storage.TypeInt), fmt.Errorf("Msg 241, Level 16, State 1: Conversion failed when converting date and/or time from character string.")
			}
		}

	case "DATEFROMPARTS":
		if len(args) != 3 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("The datefromparts function requires 3 argument(s).")
		}
		for i := 0; i < 3; i++ {
			if args[i].IsNull {
				return storage.NullValue(storage.TypeVarChar), nil
			}
		}
		y, okY := toIntNum(args[0])
		m, okM := toIntNum(args[1])
		d, okD := toIntNum(args[2])
		if !okY || !okM || !okD || !isValidCalendarDate(int(y), int(m), int(d), 1, 9999) {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 289, Level 16, State 1: Cannot construct data type date, some of the arguments have values which are not valid.")
		}
		return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d", y, m, d)), nil

	case "TIMEFROMPARTS":
		if len(args) != 3 && len(args) != 5 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("The timefromparts function requires 5 argument(s).")
		}
		if len(args) == 5 && args[4].IsNull {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 10760, Level 16, State 1: Scale argument is not valid. Valid expressions for data type time scale argument are integer constants and integer constant expressions.")
		}
		prec := 0
		if len(args) == 5 {
			pVal, okP := toIntNum(args[4])
			if !okP || pVal < 0 || pVal > 7 {
				return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 289, Level 16, State 2: Cannot construct data type time, some of the arguments have values which are not valid.")
			}
			prec = int(pVal)
		}
		checkCount := len(args)
		if checkCount == 5 {
			checkCount = 4
		}
		for i := 0; i < checkCount; i++ {
			if args[i].IsNull {
				return storage.NullValue(storage.TypeVarChar), nil
			}
		}
		h, okH := toIntNum(args[0])
		m, okM := toIntNum(args[1])
		s, okS := toIntNum(args[2])
		frac := int64(0)
		okF := true
		if len(args) == 5 {
			frac, okF = toIntNum(args[3])
		}
		if !okH || !okM || !okS || !okF || !isValidTimeOfDay(int(h), int(m), int(s)) || !isValidFractionForPrecision(frac, prec) {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 289, Level 16, State 2: Cannot construct data type time, some of the arguments have values which are not valid.")
		}
		if prec > 0 {
			return storage.StringValue(fmt.Sprintf("%02d:%02d:%02d.%0*d", h, m, s, prec, frac)), nil
		}
		return storage.StringValue(fmt.Sprintf("%02d:%02d:%02d", h, m, s)), nil

	case "DATETIMEFROMPARTS":
		if len(args) != 7 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("The datetimefromparts function requires 7 argument(s).")
		}
		for i := 0; i < 7; i++ {
			if args[i].IsNull {
				return storage.NullValue(storage.TypeVarChar), nil
			}
		}
		y, okY := toIntNum(args[0])
		m, okM := toIntNum(args[1])
		d, okD := toIntNum(args[2])
		h, okH := toIntNum(args[3])
		min, okMin := toIntNum(args[4])
		s, okS := toIntNum(args[5])
		ms, okMS := toIntNum(args[6])
		if !okY || !okM || !okD || !okH || !okMin || !okS || !okMS ||
			!isValidCalendarDate(int(y), int(m), int(d), 1753, 9999) ||
			!isValidTimeOfDay(int(h), int(min), int(s)) ||
			ms < 0 || ms > 999 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 289, Level 16, State 3: Cannot construct data type datetime, some of the arguments have values which are not valid.")
		}
		return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d.%03d", y, m, d, h, min, s, ms)), nil

	case "SMALLDATETIMEFROMPARTS":
		if len(args) != 5 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("The smalldatetimefromparts function requires 5 argument(s).")
		}
		for i := 0; i < 5; i++ {
			if args[i].IsNull {
				return storage.NullValue(storage.TypeVarChar), nil
			}
		}
		y, okY := toIntNum(args[0])
		m, okM := toIntNum(args[1])
		d, okD := toIntNum(args[2])
		hh, okH := toIntNum(args[3])
		mm, okMin := toIntNum(args[4])
		if !okY || !okM || !okD || !okH || !okMin ||
			!isValidCalendarDate(int(y), int(m), int(d), 1900, 2079) ||
			!isValidTimeOfDay(int(hh), int(mm), 0) {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 289, Level 16, State 4: Cannot construct data type smalldatetime, some of the arguments have values which are not valid.")
		}
		return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d %02d:%02d:00", y, m, d, hh, mm)), nil

	case "DATETIME2FROMPARTS":
		if len(args) != 8 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("The datetime2fromparts function requires 8 argument(s).")
		}
		if args[7].IsNull {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 10760, Level 16, State 1: Scale argument is not valid. Valid expressions for data type datetime2 scale argument are integer constants and integer constant expressions.")
		}
		pVal, okP := toIntNum(args[7])
		if !okP || pVal < 0 || pVal > 7 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 289, Level 16, State 5: Cannot construct data type datetime2, some of the arguments have values which are not valid.")
		}
		prec := int(pVal)
		for i := 0; i < 7; i++ {
			if args[i].IsNull {
				return storage.NullValue(storage.TypeVarChar), nil
			}
		}
		y, okY := toIntNum(args[0])
		m, okM := toIntNum(args[1])
		d, okD := toIntNum(args[2])
		h, okH := toIntNum(args[3])
		min, okMin := toIntNum(args[4])
		s, okS := toIntNum(args[5])
		frac, okF := toIntNum(args[6])
		if !okY || !okM || !okD || !okH || !okMin || !okS || !okF ||
			!isValidCalendarDate(int(y), int(m), int(d), 1, 9999) ||
			!isValidTimeOfDay(int(h), int(min), int(s)) ||
			!isValidFractionForPrecision(frac, prec) {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 289, Level 16, State 5: Cannot construct data type datetime2, some of the arguments have values which are not valid.")
		}
		if prec > 0 {
			return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d.%0*d", y, m, d, h, min, s, prec, frac)), nil
		}
		return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", y, m, d, h, min, s)), nil

	case "DATETIMEOFFSETFROMPARTS":
		if len(args) != 10 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("The datetimeoffsetfromparts function requires 10 argument(s).")
		}
		if args[9].IsNull {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 10760, Level 16, State 1: Scale argument is not valid. Valid expressions for data type datetimeoffset scale argument are integer constants and integer constant expressions.")
		}
		pVal, okP := toIntNum(args[9])
		if !okP || pVal < 0 || pVal > 7 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 289, Level 16, State 6: Cannot construct data type datetimeoffset, some of the arguments have values which are not valid.")
		}
		prec := int(pVal)
		for i := 0; i < 9; i++ {
			if args[i].IsNull {
				return storage.NullValue(storage.TypeVarChar), nil
			}
		}
		y, okY := toIntNum(args[0])
		m, okM := toIntNum(args[1])
		d, okD := toIntNum(args[2])
		h, okH := toIntNum(args[3])
		min, okMin := toIntNum(args[4])
		s, okS := toIntNum(args[5])
		frac, okF := toIntNum(args[6])
		hOff, okHO := toIntNum(args[7])
		mOff, okMO := toIntNum(args[8])
		if !okY || !okM || !okD || !okH || !okMin || !okS || !okF || !okHO || !okMO ||
			!isValidCalendarDate(int(y), int(m), int(d), 1, 9999) ||
			!isValidTimeOfDay(int(h), int(min), int(s)) ||
			!isValidFractionForPrecision(frac, prec) ||
			hOff < -14 || hOff > 14 || mOff < -59 || mOff > 59 ||
			(hOff > 0 && mOff < 0) || (hOff < 0 && mOff > 0) ||
			(hOff*60+mOff) < -14*60 || (hOff*60+mOff) > 14*60 {
			return storage.NullValue(storage.TypeVarChar), fmt.Errorf("Msg 289, Level 16, State 6: Cannot construct data type datetimeoffset, some of the arguments have values which are not valid.")
		}
		sign := "+"
		absH := hOff
		absM := mOff
		if hOff < 0 || mOff < 0 {
			sign = "-"
			if absH < 0 {
				absH = -absH
			}
			if absM < 0 {
				absM = -absM
			}
		}
		if prec > 0 {
			return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d.%0*d %s%02d:%02d", y, m, d, h, min, s, prec, frac, sign, absH, absM)), nil
		}
		return storage.StringValue(fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d %s%02d:%02d", y, m, d, h, min, s, sign, absH, absM)), nil
	}
	return evalDateBuiltin(upper, args), nil
}

func evalDateBuiltin(upper string, args []storage.Value) storage.Value {
	switch upper {
	case "GETDATE", "NOW", "CURRENT_TIMESTAMP", "STATEMENT_TIMESTAMP", "CLOCK_TIMESTAMP":
		return storage.DateTimeValue(time.Now())

	case "TIMEOFDAY":
		return storage.StringValue(time.Now().Format("Mon Jan 02 15:04:05.000000 2006 MST"))

	case "CURDATE", "CURRENT_DATE":
		return storage.DateValue(time.Now())

	case "CURTIME", "CURRENT_TIME":
		return storage.TimeValue(time.Now())

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
		if args[1].Type == storage.TypeFloat || args[1].Type == storage.TypeReal {
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
		if args[1].Type == storage.TypeFloat || args[1].Type == storage.TypeReal {
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
			} else if args[1].Type == storage.TypeFloat || args[1].Type == storage.TypeReal {
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
