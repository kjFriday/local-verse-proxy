package pyfmt

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

var errISO = errors.New("Invalid isoformat string")

// ParseISO is datetime.fromisoformat (3.11 rules, as CPython 3.13's C
// code reads them); naive: no zone. A zone offset with a fraction of
// a second keeps whole seconds only (Go zones cannot hold less).
func ParseISO(s string) (t time.Time, naive bool, err error) {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) < 7 {
		return time.Time{}, false, fmt.Errorf("%w: %s", errISO,
			ReprString(s))
	}
	fail := func() (time.Time, bool, error) {
		return time.Time{}, false, fmt.Errorf("%w: %s", errISO,
			ReprString(s))
	}
	sep := isoSeparator(s)
	if sep < 0 {
		return fail()
	}
	year, month, day, ok := isoDate(s, sep)
	if !ok {
		return fail()
	}
	var hour, minute, second, usec, tzoff int
	rv := 0
	if len(s) > sep {
		p := sep
		_, n := utf8.DecodeRuneInString(s[p:])
		p += n
		rv, hour, minute, second, usec, tzoff = isoTime(s, p)
	}
	if rv < 0 {
		return fail()
	}
	if year < 1 || year > 9999 || month < 1 || month > 12 ||
		day < 1 || day > daysIn(year, month) || hour > 23 ||
		minute > 59 || second > 59 {
		return fail()
	}
	loc := time.UTC
	if rv == 1 && tzoff != 0 {
		loc = time.FixedZone("", tzoff)
	}
	t = time.Date(year, time.Month(month), day, hour, minute, second,
		usec*1000, loc)
	return t, rv != 1, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// at is s[i] or 0 past the end, as C's NUL-ended strings read.
func at(s string, i int) byte {
	if i >= 0 && i < len(s) {
		return s[i]
	}
	return 0
}

// isoSeparator is _find_isoformat_datetime_separator.
func isoSeparator(s string) int {
	n := len(s)
	if n == 7 {
		return 7
	}
	if at(s, 4) == '-' {
		if at(s, 5) == 'W' {
			if n < 8 {
				return -1
			}
			if n > 8 && at(s, 8) == '-' {
				if n == 9 {
					return -1
				}
				if n > 10 && isDigit(at(s, 10)) {
					return 8
				}
				return 10
			}
			return 8
		}
		return 10
	}
	if at(s, 4) == 'W' {
		idx := 7
		for ; idx < n; idx++ {
			if !isDigit(s[idx]) {
				break
			}
		}
		if idx < 9 {
			return idx
		}
		if idx%2 == 0 {
			return 7
		}
		return 8
	}
	return 8
}

func digits(s string, p, n int) (int, int, bool) {
	v := 0
	for i := 0; i < n; i++ {
		c := at(s, p+i)
		if !isDigit(c) {
			return 0, p, false
		}
		v = v*10 + int(c-'0')
	}
	return v, p + n, true
}

// isoDate is parse_isoformat_date over s[:end].
func isoDate(s string, end int) (y, m, d int, ok bool) {
	y, p, ok := digits(s, 0, 4)
	if !ok {
		return
	}
	dash := at(s, p) == '-'
	if dash {
		p++
	}
	if at(s, p) == 'W' {
		p++
		var week, wday int
		week, p, ok = digits(s, p, 2)
		if !ok {
			return
		}
		if p < end {
			if dash {
				if at(s, p) != '-' {
					return 0, 0, 0, false
				}
				p++
			}
			wday, p, ok = digits(s, p, 1)
			if !ok {
				return
			}
		} else {
			wday = 1
		}
		return isoWeek(y, week, wday)
	}
	m, p, ok = digits(s, p, 2)
	if !ok {
		return
	}
	if dash {
		if at(s, p) != '-' {
			return 0, 0, 0, false
		}
		p++
	}
	d, _, ok = digits(s, p, 2)
	return
}

// isoWeek is iso_to_ymd.
func isoWeek(year, week, wday int) (y, m, d int, ok bool) {
	if year < 1 || year > 9999 {
		return 0, 0, 0, false
	}
	if week <= 0 || week >= 53 {
		jan1 := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Weekday()
		leap := year%4 == 0 && (year%100 != 0 || year%400 == 0)
		if !(week == 53 && (jan1 == time.Thursday ||
			leap && jan1 == time.Wednesday)) {
			return 0, 0, 0, false
		}
	}
	if wday <= 0 || wday >= 8 {
		return 0, 0, 0, false
	}
	// Monday of week 1: the week with the year's first Thursday.
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	off := (int(jan4.Weekday()) + 6) % 7
	mon := jan4.AddDate(0, 0, -off)
	t := mon.AddDate(0, 0, (week-1)*7+wday-1)
	return t.Year(), int(t.Month()), t.Day(), true
}

// isoTime is parse_isoformat_time from s[p:]: rv 0 no zone, 1 zone,
// < 0 an error.
func isoTime(s string, p int) (rv, h, m, sec, us, tz int) {
	end := len(s)
	tzi := p
	for {
		c := at(s, tzi)
		if c == 'Z' || c == '+' || c == '-' {
			break
		}
		tzi++
		if tzi >= end {
			break
		}
	}
	r, h, m, sec, us := hmsf(s, p, tzi)
	if r < 0 {
		return r, 0, 0, 0, 0, 0
	}
	if tzi == end {
		if r == 1 {
			return -5, 0, 0, 0, 0, 0
		}
		return 0, h, m, sec, us, 0
	}
	if tzi > end {
		return -3, 0, 0, 0, 0, 0
	}
	if s[tzi] == 'Z' {
		if tzi+1 != end {
			return -5, 0, 0, 0, 0, 0
		}
		return 1, h, m, sec, us, 0
	}
	sign := 1
	if s[tzi] == '-' {
		sign = -1
	}
	r2, th, tm, ts, tus := hmsf(s, tzi+1, end)
	if r2 < 0 {
		return r2, 0, 0, 0, 0, 0
	}
	// Only the whole offset is checked, as timezone() checks it:
	// +00:99 reads as +01:39.
	off := th*3600 + tm*60 + ts
	if off >= 86400 || off == 86399 && tus > 999999 {
		return -6, 0, 0, 0, 0, 0
	}
	if r2 != 0 {
		return -5, 0, 0, 0, 0, 0
	}
	return 1, h, m, sec, us, sign * off
}

// hmsf is parse_hh_mm_ss_ff over s[p:end]; the result is 0 at the
// end of s, 1 with more after, or < 0 for an error.
func hmsf(s string, p, end int) (r, h, m, sec, us int) {
	vals := [3]int{}
	sep := true
	var ok bool
	i := 0
	for ; i < 3; i++ {
		vals[i], p, ok = digits(s, p, 2)
		if !ok {
			return -3, 0, 0, 0, 0
		}
		c := at(s, p)
		p++
		if i == 0 {
			sep = c == ':'
		}
		// A decimal mark after any part starts the fraction (12.5 is
		// half past twelve), and must have a digit after it.
		if c == '.' || c == ',' {
			break
		}
		if p >= end {
			r = 0
			if c != 0 {
				r = 1
			}
			return r, vals[0], vals[1], vals[2], 0
		} else if sep && c == ':' {
			if i == 2 {
				return -4, 0, 0, 0, 0
			}
			continue
		} else if !sep {
			p--
		} else {
			return -4, 0, 0, 0, 0
		}
	}
	// Here after a decimal mark, or after HHMMSS with more digits,
	// which C reads as the fraction too (21264325 is 21:26:43.25).
	remains := end - p
	if remains <= 0 {
		return -3, 0, 0, 0, 0
	}
	n := remains
	if n >= 6 {
		n = 6
	}
	frac, q, ok := digits(s, p, n)
	if !ok {
		return -3, 0, 0, 0, 0
	}
	p = q
	for k := n; k < 6; k++ {
		frac *= 10
	}
	for isDigit(at(s, p)) {
		p++
	}
	r = 0
	if at(s, p) != 0 {
		r = 1
	}
	return r, vals[0], vals[1], vals[2], frac
}

func daysIn(y, m int) int {
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// ISOFormat is datetime.isoformat(): YYYY-MM-DDTHH:MM:SS, then
// .ffffff if there are microseconds, then the zone as +HH:MM (and
// :SS when it has seconds) unless naive.
func ISOFormat(t time.Time, naive bool) string {
	s := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d", t.Year(),
		int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second())
	if us := t.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	if naive {
		return s
	}
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	s += fmt.Sprintf("%s%02d:%02d", sign, off/3600, off%3600/60)
	if off%60 != 0 {
		s += fmt.Sprintf(":%02d", off%60)
	}
	return s
}
