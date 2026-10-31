package formula

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// maxSerial is 31 December 9999 in the 1900 system.
const maxSerial = 2958465

// daysFromCivil counts days from 1970-01-01 to a proleptic Gregorian date; m is 1 to 12.
func daysFromCivil(y, m, d int) int {
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// civilFromDays is the inverse of daysFromCivil.
func civilFromDays(z int) (y, m, d int) {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

var (
	epoch1900 = daysFromCivil(1899, 12, 30)
	epoch1904 = daysFromCivil(1904, 1, 1)
)

// SerialFromDate gives the serial of a date. The month and day may overflow, as in DATE: month 13 is January of the
// next year, day 0 the last of the month before. 1900 has its fictional 29 February, serial 60.
func SerialFromDate(y, m, d int, date1904 bool) float64 {
	y += floorDiv(m-1, 12)
	m -= floorDiv(m-1, 12) * 12
	base := daysFromCivil(y, m, 1)
	if date1904 {
		return float64(base - epoch1904 + d - 1)
	}
	s := base - epoch1900
	if s < 61 {
		s--
	}
	return float64(s + d - 1)
}

// DateFromSerial splits a serial into its date and time, to the nearest millisecond. Serial 0 of the 1900 system is
// the day Excel shows as 1900-01-00, and serial 60 is 1900-02-29.
func DateFromSerial(s float64, date1904 bool) (y, m, d, hh, mm, ss int, ms float64) {
	if math.IsNaN(s) || math.IsInf(s, 0) {
		return 0, 0, 0, 0, 0, 0, 0
	}
	s = math.Max(-1e7, math.Min(s, 1e8))
	whole := math.Floor(s)
	millis := math.Round((s - whole) * 86400000)
	if millis >= 86400000 {
		whole++
		millis = 0
	}
	y, m, d = dateOfDay(int(whole), date1904)
	t := int(millis)
	hh = t / 3600000
	mm = t / 60000 % 60
	ss = t / 1000 % 60
	return y, m, d, hh, mm, ss, float64(t % 1000)
}

// dateOfDay gives the date of a whole serial.
func dateOfDay(day int, date1904 bool) (y, m, d int) {
	switch {
	case date1904:
		return civilFromDays(epoch1904 + day)
	case day == 0:
		return 1900, 1, 0
	case day == 60:
		return 1900, 2, 29
	case day < 60:
		return civilFromDays(epoch1900 + day + 1)
	}
	return civilFromDays(epoch1900 + day)
}

// weekdayOf gives 0 for Sunday to 6 for Saturday, with Excel's 1900 calendar, where serial 1 is a Sunday.
func weekdayOf(day int, date1904 bool) int {
	if date1904 {
		day += 1462
	}
	w := (day + 6) % 7
	if w < 0 {
		w += 7
	}
	return w
}

// nowSerial is the local date and time as a serial.
func nowSerial(date1904 bool) float64 {
	t := time.Now()
	day := SerialFromDate(t.Year(), int(t.Month()), t.Day(), date1904)
	secs := float64(t.Hour()*3600+t.Minute()*60+t.Second()) + float64(t.Nanosecond())/1e9
	return day + secs/86400
}

var monthNames = []string{"january", "february", "march", "april", "may", "june", "july", "august", "september",
	"october", "november", "december"}

func monthOf(s string) int {
	s = strings.ToLower(s)
	if len(s) < 3 {
		return 0
	}
	for i, n := range monthNames {
		if s == n || s == n[:3] || (i == 8 && s == "sept") {
			return i + 1
		}
	}
	return 0
}

// fullYear expands a two-digit year the way Excel does: 00 to 29 are 20xx, 30 to 99 are 19xx.
func fullYear(y int, digits int) int {
	if digits <= 2 {
		if y < 30 {
			return 2000 + y
		}
		return 1900 + y
	}
	return y
}

func validDate(y, m, d int) bool {
	if y < 1 || y > 9999 || m < 1 || m > 12 || d < 1 {
		return false
	}
	if y == 1900 && m == 2 && d == 29 {
		return true
	}
	return d <= daysIn(y, m)
}

func daysIn(y, m int) int {
	switch m {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	}
	return 31
}

// allDigits says s is a run of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseDatePart reads the date forms Excel accepts when typed in en-US.
func parseDatePart(s string, date1904 bool) (float64, bool) {
	s = strings.TrimSpace(s)
	done := func(y, m, d int) (float64, bool) {
		if !validDate(y, m, d) {
			return 0, false
		}
		v := SerialFromDate(y, m, d, date1904)
		if v < 0 {
			return 0, false
		}
		return v, true
	}
	if strings.Contains(s, "年") {
		rest := strings.ReplaceAll(strings.ReplaceAll(s, "年", "-"), "月", "-")
		rest = strings.TrimSuffix(rest, "日")
		rest = strings.TrimRight(rest, "-")
		s = rest
	}
	norm := strings.NewReplacer("/", " ", "-", " ", ",", " ", ".", " ").Replace(s)
	f := strings.Fields(norm)
	if len(f) < 2 || len(f) > 3 {
		return 0, false
	}
	if !strings.ContainsAny(s, "/-") && monthOf(f[0]) == 0 && (len(f) < 2 || monthOf(f[1]) == 0) {
		return 0, false
	}
	atoi := func(t string) (int, bool) {
		if !allDigits(t) || len(t) > 4 {
			return 0, false
		}
		n, err := strconv.Atoi(t)
		return n, err == nil
	}
	year := time.Now().Year()
	if m := monthOf(f[0]); m != 0 { // Jan 5, 2020 / Jan 2020 / Jan 5
		a, ok := atoi(f[1])
		if !ok {
			return 0, false
		}
		if len(f) == 3 {
			y, ok := atoi(f[2])
			if !ok {
				return 0, false
			}
			return done(fullYear(y, len(f[2])), m, a)
		}
		if a > 31 || len(f[1]) == 4 {
			return done(fullYear(a, len(f[1])), m, 1)
		}
		return done(year, m, a)
	}
	if m := monthOf(f[1]); m != 0 { // 5 Jan 2020 / 5-Jan
		a, ok := atoi(f[0])
		if !ok {
			return 0, false
		}
		if len(f) == 3 {
			y, ok := atoi(f[2])
			if !ok {
				return 0, false
			}
			return done(fullYear(y, len(f[2])), m, a)
		}
		return done(year, m, a)
	}
	a, ok := atoi(f[0])
	if !ok {
		return 0, false
	}
	b, ok := atoi(f[1])
	if !ok {
		return 0, false
	}
	if len(f) == 2 {
		if a >= 1 && a <= 12 && b >= 1 && b <= 31 {
			return done(year, a, b)
		}
		if a >= 1 && a <= 12 { // 1/2020
			return done(fullYear(b, len(f[1])), a, 1)
		}
		return 0, false
	}
	c, ok := atoi(f[2])
	if !ok {
		return 0, false
	}
	if len(f[0]) >= 3 || a > 31 { // 2020-01-05
		return done(a, b, c)
	}
	return done(fullYear(c, len(f[2])), a, b)
}

// parseTimePart reads 13:05, 1:05:30.5 PM, 1 PM and the like as a fraction of a day.
func parseTimePart(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	up := strings.ToUpper(s)
	pm, ampm := false, false
	for _, suffix := range []string{" AM", " PM", "AM", "PM", " A", " P"} {
		if strings.HasSuffix(up, suffix) {
			ampm = true
			pm = strings.Contains(suffix, "P")
			s = strings.TrimSpace(s[:len(s)-len(suffix)])
			break
		}
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 || (len(parts) == 1 && !ampm) {
		return 0, false
	}
	h, m := 0, 0
	secs := 0.0
	var err error
	if !allDigits(parts[0]) || len(parts[0]) > 5 {
		return 0, false
	}
	h, _ = strconv.Atoi(parts[0])
	if len(parts) > 1 {
		if !allDigits(parts[1]) || len(parts[1]) > 5 {
			return 0, false
		}
		m, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		t := parts[2]
		if w, f, cut := strings.Cut(t, "."); cut {
			if !allDigits(w) || (f != "" && !allDigits(f)) {
				return 0, false
			}
		} else if !allDigits(t) {
			return 0, false
		}
		if secs, err = strconv.ParseFloat(t, 64); err != nil || secs >= 60 {
			return 0, false
		}
	}
	if ampm {
		if h < 1 || h > 12 {
			return 0, false
		}
		h %= 12
		if pm {
			h += 12
		}
	}
	if m >= 60 && len(parts) > 1 {
		return 0, false
	}
	return (float64(h)*3600 + float64(m)*60 + secs) / 86400, true
}

// parseDateTime reads a text as a date, a time, or both. It reports which parts were there.
func parseDateTime(s string, date1904 bool) (day, tod float64, hasDate, hasTime, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false, false, false
	}
	if v, good := parseTimePart(s); good {
		return 0, v, false, true, true
	}
	if v, good := parseDatePart(s, date1904); good {
		return v, 0, true, false, true
	}
	// A date, then a time: the time is the tail that starts at the last word with a colon, or at an AM/PM hour.
	f := strings.Fields(s)
	for cut := len(f) - 1; cut >= 1; cut-- {
		t, good := parseTimePart(strings.Join(f[cut:], " "))
		if !good {
			continue
		}
		d, good := parseDatePart(strings.Join(f[:cut], " "), date1904)
		if good {
			return d, t, true, true, true
		}
	}
	return 0, 0, false, false, false
}

// textToNumber reads a text as Excel coerces it: plain numbers, thousands commas, currency, percent, parenthesised
// negatives, dates and times.
func textToNumber(s string, date1904 bool) (float64, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, false
	}
	if v, ok := plainNumber(t); ok {
		return v, true
	}
	if d, tod, _, _, ok := parseDateTime(t, date1904); ok {
		return d + tod, true
	}
	return 0, false
}

func plainNumber(t string) (float64, bool) {
	neg := false
	if strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")") {
		neg = true
		t = strings.TrimSpace(t[1 : len(t)-1])
	}
	if t != "" && (t[0] == '-' || t[0] == '+') {
		if neg {
			return 0, false
		}
		neg = t[0] == '-'
		t = strings.TrimSpace(t[1:])
	}
	if strings.HasPrefix(t, "$") {
		t = strings.TrimSpace(t[1:])
		if t != "" && t[0] == '-' && !neg {
			neg = true
			t = t[1:]
		}
	}
	pct := 0
	for strings.HasSuffix(t, "%") {
		pct++
		t = strings.TrimSpace(t[:len(t)-1])
	}
	if !validNumberText(t) {
		return 0, false
	}
	t = strings.ReplaceAll(t, ",", "")
	v, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, false
	}
	for ; pct > 0; pct-- {
		v /= 100
	}
	if neg {
		v = -v
	}
	return v, true
}

// validNumberText checks digits with optional groups of three, a fraction and an exponent.
func validNumberText(t string) bool {
	i, digits := 0, 0
	group := -1
	for i < len(t) {
		c := t[i]
		if c >= '0' && c <= '9' {
			digits++
			if group >= 0 {
				group++
			}
			i++
			continue
		}
		if c == ',' && digits > 0 {
			if group >= 0 && group != 3 {
				return false
			}
			group = 0
			i++
			continue
		}
		break
	}
	if group >= 0 && group != 3 {
		return false
	}
	if i < len(t) && t[i] == '.' {
		i++
		for i < len(t) && t[i] >= '0' && t[i] <= '9' {
			digits++
			i++
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(t) && (t[i] == 'e' || t[i] == 'E') {
		i++
		if i < len(t) && (t[i] == '+' || t[i] == '-') {
			i++
		}
		start := i
		for i < len(t) && t[i] >= '0' && t[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(t)
}
