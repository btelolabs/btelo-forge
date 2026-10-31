package formula

import "math"

// serial coerces a date argument to a whole day, refusing what is before the first day or after the last.
func (c *ctx) serial(v Value) (int, Value, bool) {
	f, bad, ok := c.num(v)
	if !ok {
		return 0, bad, false
	}
	if f < 0 || f > maxSerial {
		return 0, Err(ErrNum), false
	}
	return int(math.Floor(f)), Value{}, true
}

// datePart makes YEAR, MONTH and DAY.
func datePart(pick func(y, m, d int) int) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		f, bad, ok := c.num(a[0])
		if !ok {
			return bad
		}
		if f < 0 || f >= maxSerial+1 {
			return Err(ErrNum)
		}
		y, m, d, _, _, _, _ := DateFromSerial(f, c.date1904())
		return Num(float64(pick(y, m, d)))
	}
}

// timePart makes HOUR, MINUTE and SECOND.
func timePart(pick func(h, m, s int) int) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		f, bad, ok := c.num(a[0])
		if !ok {
			return bad
		}
		if f < 0 || f >= maxSerial+1 {
			return Err(ErrNum)
		}
		_, _, _, h, m, s, ms := DateFromSerial(f, c.date1904())
		if ms >= 500 {
			s++
			if s == 60 {
				s = 0
				m++
				if m == 60 {
					m = 0
					h = (h + 1) % 24
				}
			}
		}
		return Num(float64(pick(h, m, s)))
	}
}

func fnDate(c *ctx, a []Value) Value {
	var y, m, d float64
	if bad, ok := c.nums(a, &y, &m, &d); !ok {
		return bad
	}
	y, m, d = math.Trunc(y), math.Trunc(m), math.Trunc(d)
	if y >= 0 && y < 1900 {
		y += 1900
	}
	if y < 0 || y >= 10000 || math.Abs(m) > 1e6 || math.Abs(d) > 1e8 {
		return Err(ErrNum)
	}
	s := SerialFromDate(int(y), int(m), int(d), c.date1904())
	if s < 0 || s > maxSerial {
		return Err(ErrNum)
	}
	return Num(s)
}

func fnTime(c *ctx, a []Value) Value {
	var h, m, s float64
	if bad, ok := c.nums(a, &h, &m, &s); !ok {
		return bad
	}
	if h < 0 || m < 0 || s < 0 {
		// Only the total has to be positive.
		if math.Trunc(h)*3600+math.Trunc(m)*60+math.Trunc(s) < 0 {
			return Err(ErrNum)
		}
	}
	t := math.Trunc(h)*3600 + math.Trunc(m)*60 + math.Trunc(s)
	return numResult(math.Mod(t, 86400) / 86400)
}

func fnWeekday(c *ctx, a []Value) Value {
	d, bad, ok := c.serial(a[0])
	if !ok {
		return bad
	}
	t := 1.0
	if bad, ok := c.nums(a[1:], &t); !ok {
		return bad
	}
	w := weekdayOf(d, c.date1904()) // 0 is Sunday
	switch n := int(t); {
	case n == 1 || n == 17:
		return Num(float64(w + 1))
	case n == 2 || n == 11:
		return Num(float64((w+6)%7 + 1))
	case n == 3:
		return Num(float64((w + 6) % 7))
	case n >= 12 && n <= 16:
		// 12 starts the week on Tuesday, 16 on Saturday.
		return Num(float64((w+7-(n-10))%7 + 1))
	}
	return Err(ErrNum)
}

func fnEdate(c *ctx, a []Value) Value {
	d, bad, ok := c.serial(a[0])
	if !ok {
		return bad
	}
	var n float64
	if bad, ok := c.nums(a[1:], &n); !ok {
		return bad
	}
	if math.Abs(n) > 1e6 {
		return Err(ErrNum)
	}
	sys := c.date1904()
	y, m, day := dateOfDay(d, sys)
	m += int(math.Trunc(n))
	last := int(SerialFromDate(y, m+1, 1, sys) - SerialFromDate(y, m, 1, sys))
	s := SerialFromDate(y, m, min(day, last), sys)
	if s < 0 || s > maxSerial {
		return Err(ErrNum)
	}
	return Num(s)
}

func fnEomonth(c *ctx, a []Value) Value {
	d, bad, ok := c.serial(a[0])
	if !ok {
		return bad
	}
	var n float64
	if bad, ok := c.nums(a[1:], &n); !ok {
		return bad
	}
	if math.Abs(n) > 1e6 {
		return Err(ErrNum)
	}
	sys := c.date1904()
	y, m, _ := dateOfDay(d, sys)
	s := SerialFromDate(y, m+int(math.Trunc(n))+1, 1, sys) - 1
	if s < 0 || s > maxSerial {
		return Err(ErrNum)
	}
	return Num(s)
}

func fnDatedif(c *ctx, a []Value) Value {
	s, bad, ok := c.serial(a[0])
	if !ok {
		return bad
	}
	e, bad, ok := c.serial(a[1])
	if !ok {
		return bad
	}
	unit, bad, ok := c.text(a[2])
	if !ok {
		return bad
	}
	if s > e {
		return Err(ErrNum)
	}
	sys := c.date1904()
	y1, m1, d1 := dateOfDay(s, sys)
	y2, m2, d2 := dateOfDay(e, sys)
	months := (y2-y1)*12 + m2 - m1
	if d2 < d1 {
		months--
	}
	inMonth := func(y, m int) int {
		return int(SerialFromDate(y, m+1, 1, sys) - SerialFromDate(y, m, 1, sys))
	}
	switch upperASCII(unit) {
	case "D":
		return Num(float64(e - s))
	case "M":
		return Num(float64(months))
	case "Y":
		return Num(float64(months / 12))
	case "YM":
		return Num(float64(months % 12))
	case "MD":
		if d2 >= d1 {
			return Num(float64(d2 - d1))
		}
		py, pm := y2, m2-1
		if pm == 0 {
			py, pm = py-1, 12
		}
		return Num(float64(d2 + inMonth(py, pm) - d1))
	case "YD":
		y := y2
		if m2 < m1 || (m2 == m1 && d2 < d1) {
			y--
		}
		return Num(float64(e) - SerialFromDate(y, m1, d1, sys))
	}
	return Err(ErrNum)
}

func upperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// holidaySet gathers the days of a holidays argument.
func (c *ctx) holidaySet(v Value) (map[int]bool, Value, bool) {
	set := map[int]bool{}
	if v.Kind == Blank {
		return set, Value{}, true
	}
	var bad Value
	failed := false
	c.eachValue(v, func(x Value, _ bool) bool {
		d, e, ok := c.serial(x)
		if !ok {
			bad, failed = e, true
			return false
		}
		set[d] = true
		return true
	})
	if failed {
		return nil, bad, false
	}
	return set, Value{}, true
}

// weekendMask gives a mask of weekend days, bit 0 for Sunday, from the number or text of NETWORKDAYS.INTL.
func (c *ctx) weekendMask(v Value) (uint8, Value, bool) {
	const sun, mon, sat = 1 << 0, 1 << 1, 1 << 6
	if v.Kind == Blank {
		return sun | sat, Value{}, true
	}
	if v.Kind == Text {
		if len(v.Str) != 7 {
			return 0, Err(ErrValue), false
		}
		var m uint8
		for i := range 7 {
			switch v.Str[i] {
			case '1':
				m |= 1 << ((i + 1) % 7) // the text starts on Monday
			case '0':
			default:
				return 0, Err(ErrValue), false
			}
		}
		if m == 0x7F {
			return 0, Err(ErrValue), false
		}
		return m, Value{}, true
	}
	f, bad, ok := c.num(v)
	if !ok {
		return 0, bad, false
	}
	switch n := int(f); {
	case n >= 1 && n <= 7:
		// 1 is Saturday-Sunday, 2 Sunday-Monday, on to 7, Friday-Saturday.
		first := (n + 5) % 7
		return 1<<first | 1<<((first+1)%7), Value{}, true
	case n >= 11 && n <= 17:
		return 1 << (n - 11), Value{}, true
	}
	return 0, Err(ErrNum), false
}

// workdaysBetween counts the working days from lo to hi inclusive.
func workdaysBetween(lo, hi int, mask uint8, hols map[int]bool, sys bool) int {
	perWeek := 0
	for i := range 7 {
		if mask&(1<<i) == 0 {
			perWeek++
		}
	}
	days := hi - lo + 1
	n := days / 7 * perWeek
	start := lo + days/7*7
	for d := start; d <= hi; d++ {
		if mask&(1<<weekdayOf(d, sys)) == 0 {
			n++
		}
	}
	for d := range hols {
		if d >= lo && d <= hi && mask&(1<<weekdayOf(d, sys)) == 0 {
			n--
		}
	}
	return n
}

func (c *ctx) networkDays(start, end, weekend, hols Value) Value {
	s, bad, ok := c.serial(c.operand(start))
	if !ok {
		return bad
	}
	e, bad, ok := c.serial(c.operand(end))
	if !ok {
		return bad
	}
	mask, bad, ok := c.weekendMask(c.operand(weekend))
	if !ok {
		return bad
	}
	set, bad, ok := c.holidaySet(hols)
	if !ok {
		return bad
	}
	if s > e {
		return Num(-float64(workdaysBetween(e, s, mask, set, c.date1904())))
	}
	return Num(float64(workdaysBetween(s, e, mask, set, c.date1904())))
}

func (c *ctx) workday(start, count, weekend, hols Value) Value {
	s, bad, ok := c.serial(c.operand(start))
	if !ok {
		return bad
	}
	f, bad, ok := c.num(c.operand(count))
	if !ok {
		return bad
	}
	mask, bad, ok := c.weekendMask(c.operand(weekend))
	if !ok {
		return bad
	}
	if mask == 0x7F {
		return Err(ErrValue)
	}
	set, bad, ok := c.holidaySet(hols)
	if !ok {
		return bad
	}
	n := int(math.Trunc(f))
	if n == 0 {
		return Num(float64(s))
	}
	if n > maxSerial || n < -maxSerial {
		return Err(ErrNum)
	}
	step := 1
	if n < 0 {
		step, n = -1, -n
	}
	sys := c.date1904()
	d := s
	for n > 0 {
		d += step
		if d < 0 || d > maxSerial {
			return Err(ErrNum)
		}
		if mask&(1<<weekdayOf(d, sys)) == 0 && !set[d] {
			n--
		}
	}
	return Num(float64(d))
}

func fnWeeknum(c *ctx, a []Value) Value {
	d, bad, ok := c.serial(a[0])
	if !ok {
		return bad
	}
	t := 1.0
	if bad, ok := c.nums(a[1:], &t); !ok {
		return bad
	}
	sys := c.date1904()
	n := int(t)
	if n == 21 {
		return Num(float64(isoWeek(d, sys)))
	}
	var first int // the weekday the week starts on, 0 for Sunday
	switch {
	case n == 1 || n == 17:
		first = 0
	case n == 2 || n == 11:
		first = 1
	case n >= 12 && n <= 16:
		first = n - 10
	default:
		return Err(ErrNum)
	}
	y, _, _ := dateOfDay(d, sys)
	jan1 := int(SerialFromDate(y, 1, 1, sys))
	off := (weekdayOf(jan1, sys) - first + 7) % 7
	return Num(float64((d-jan1+off)/7 + 1))
}

// isoWeek gives the ISO 8601 week: the one holding the Thursday of the date's week.
func isoWeek(d int, sys bool) int {
	w := (weekdayOf(d, sys) + 6) % 7 // 0 is Monday
	thursday := d - w + 3
	y, _, _ := dateOfDay(thursday, sys)
	return (thursday-int(SerialFromDate(y, 1, 1, sys)))/7 + 1
}

func registerDates() {
	scalar("DATE", 3, 3, fnDate)
	scalar("TIME", 3, 3, fnTime)
	scalar("YEAR", 1, 1, datePart(func(y, _, _ int) int { return y }))
	scalar("MONTH", 1, 1, datePart(func(_, m, _ int) int { return m }))
	scalar("DAY", 1, 1, datePart(func(_, _, d int) int { return d }))
	scalar("HOUR", 1, 1, timePart(func(h, _, _ int) int { return h }))
	scalar("MINUTE", 1, 1, timePart(func(_, m, _ int) int { return m }))
	scalar("SECOND", 1, 1, timePart(func(_, _, s int) int { return s }))
	ranged("TODAY", 0, 0, func(c *ctx, _ []Value) Value {
		return Num(math.Floor(nowSerial(c.date1904())))
	}).volatile = true
	ranged("NOW", 0, 0, func(c *ctx, _ []Value) Value { return Num(nowSerial(c.date1904())) }).volatile = true
	scalar("WEEKDAY", 1, 2, fnWeekday)
	scalar("EDATE", 2, 2, fnEdate)
	scalar("EOMONTH", 2, 2, fnEomonth)
	scalar("DATEDIF", 3, 3, fnDatedif)
	scalar("DAYS", 2, 2, func(c *ctx, a []Value) Value {
		e, bad, ok := c.serial(a[0])
		if !ok {
			return bad
		}
		s, bad, ok := c.serial(a[1])
		if !ok {
			return bad
		}
		return Num(float64(e - s))
	})
	scalar("DATEVALUE", 1, 1, func(c *ctx, a []Value) Value {
		if a[0].Kind == Error {
			return a[0]
		}
		if a[0].Kind != Text {
			return Err(ErrValue)
		}
		d, _, hasDate, _, ok := parseDateTime(a[0].Str, c.date1904())
		if !ok || !hasDate {
			return Err(ErrValue)
		}
		return Num(d)
	})
	scalar("TIMEVALUE", 1, 1, func(c *ctx, a []Value) Value {
		if a[0].Kind == Error {
			return a[0]
		}
		if a[0].Kind != Text {
			return Err(ErrValue)
		}
		_, t, _, _, ok := parseDateTime(a[0].Str, c.date1904())
		if !ok {
			return Err(ErrValue)
		}
		return Num(t)
	})
	scalar("WEEKNUM", 1, 2, fnWeeknum)
	scalar("ISOWEEKNUM", 1, 1, func(c *ctx, a []Value) Value {
		d, bad, ok := c.serial(a[0])
		if !ok {
			return bad
		}
		return Num(float64(isoWeek(d, c.date1904())))
	})
	ranged("NETWORKDAYS", 2, 3, func(c *ctx, a []Value) Value {
		h := Value{}
		if len(a) > 2 {
			h = a[2]
		}
		return c.networkDays(a[0], a[1], Value{}, h)
	})
	ranged("NETWORKDAYS.INTL", 2, 4, func(c *ctx, a []Value) Value {
		w, h := Value{}, Value{}
		if len(a) > 2 {
			w = a[2]
		}
		if len(a) > 3 {
			h = a[3]
		}
		return c.networkDays(a[0], a[1], w, h)
	})
	ranged("WORKDAY", 2, 3, func(c *ctx, a []Value) Value {
		h := Value{}
		if len(a) > 2 {
			h = a[2]
		}
		return c.workday(a[0], a[1], Value{}, h)
	})
	ranged("WORKDAY.INTL", 2, 4, func(c *ctx, a []Value) Value {
		w, h := Value{}, Value{}
		if len(a) > 2 {
			w = a[2]
		}
		if len(a) > 3 {
			h = a[3]
		}
		return c.workday(a[0], a[1], w, h)
	})
}
