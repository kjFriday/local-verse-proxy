package pyfmt

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Round is round(x, n) for a float: the decimal nearest to x's exact
// value with n digits after the point (ties to even), read back as a
// float.
func Round(x float64, ndigits int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return x
	}
	if ndigits > 323 {
		return x
	}
	if ndigits < -308 {
		return math.Copysign(0, x)
	}
	if ndigits >= 0 {
		s := strconv.FormatFloat(x, 'f', ndigits, 64)
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return x
		}
		return math.Copysign(f, x)
	}
	// round(1234.5, -2): to a multiple of 10**-ndigits, exactly.
	r := new(big.Rat).SetFloat64(x)
	unit := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10),
		big.NewInt(int64(-ndigits)), nil))
	q := new(big.Rat).Quo(r, unit)
	n := roundHalfEven(q)
	f, _ := new(big.Rat).Mul(new(big.Rat).SetInt(n), unit).Float64()
	return math.Copysign(f, x)
}

func roundHalfEven(q *big.Rat) *big.Int {
	num, den := q.Num(), q.Denom()
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Mul(new(big.Int).Abs(rem), big.NewInt(2))
	switch twice.Cmp(den) {
	case 1:
		quo.Add(quo, big.NewInt(int64(num.Sign())))
	case 0:
		if quo.Bit(0) == 1 {
			quo.Add(quo, big.NewInt(int64(num.Sign())))
		}
	}
	return quo
}

// RoundInt is round(x) with no digits: the nearest whole number,
// ties to even. NaN and infinities give 0 (Python would raise); past
// int64 it stops at the largest or smallest.
func RoundInt(x float64) int64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	r := math.RoundToEven(x)
	switch {
	case r >= 1<<63:
		return math.MaxInt64
	case r < -(1 << 63):
		return math.MinInt64
	}
	return int64(r)
}

// Repr is repr(float): the shortest text that reads back as x, with
// Python's layout (1.0, 0.0001, 1e-05, 1e+16, inf, nan).
func Repr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	sign := ""
	if math.Signbit(f) {
		sign, f = "-", -f
	}
	if f == 0 {
		return sign + "0.0"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	k := strings.IndexByte(e, 'e')
	mant, exp := e[:k], 0
	exp, _ = strconv.Atoi(e[k+1:])
	digits := strings.Replace(mant, ".", "", 1)
	decpt := exp + 1 // digits before the point
	if decpt > 16 || decpt < -3 {
		m := digits[:1]
		if len(digits) > 1 {
			m += "." + digits[1:]
		}
		es := "+"
		if exp < 0 {
			es, exp = "-", -exp
		}
		x := strconv.Itoa(exp)
		if len(x) < 2 {
			x = "0" + x
		}
		return sign + m + "e" + es + x
	}
	switch {
	case decpt <= 0:
		return sign + "0." + strings.Repeat("0", -decpt) + digits
	case decpt >= len(digits):
		return sign + digits + strings.Repeat("0", decpt-len(digits)) +
			".0"
	}
	return sign + digits[:decpt] + "." + digits[decpt:]
}

// Str is str() of a decoded JSON or TOML value: int64, float64,
// json.Number (int forms keep Python's int str, "-0" is "0"),
// bool (True, False), nil (None), string, []any, map[string]any, and
// TOML's dates and times (time.Time, as tomllib's datetime, date and
// time print). A map prints in sorted key order (Python keeps the
// file's order).
func Str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case time.Time:
		return timeStr(x)
	}
	return repr(v)
}

// TOML's local kinds, by the zone names BurntSushi gives them; any
// other zone is an offset date-time.
const (
	localDatetime = "datetime-local"
	localDate     = "date-local"
	localTime     = "time-local"
)

// timeStr is str() of tomllib's value: "1979-05-27 07:32:00-08:00",
// "1979-05-27 07:32:00.250000", "1979-05-27", "07:32:00".
func timeStr(t time.Time) string {
	clock := t.Format("15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		clock += fmt.Sprintf(".%06d", us)
	}
	date := fmt.Sprintf("%04d-%02d-%02d", t.Year(), t.Month(), t.Day())
	switch t.Location().String() {
	case localDate:
		return date
	case localTime:
		return clock
	case localDatetime:
		return date + " " + clock
	}
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	zone := fmt.Sprintf("%s%02d:%02d", sign, off/3600, off/60%60)
	if off%60 != 0 {
		zone += fmt.Sprintf(":%02d", off%60)
	}
	return date + " " + clock + zone
}

// timeRepr is repr() of tomllib's value: datetime.date(1979, 5, 27),
// datetime.time(7, 32), datetime.datetime(1979, 5, 27, 7, 32,
// tzinfo=datetime.timezone.utc).
func timeRepr(t time.Time) string {
	us := t.Nanosecond() / 1000
	hms := fmt.Sprintf("%d, %d", t.Hour(), t.Minute())
	if t.Second() != 0 || us != 0 {
		hms += fmt.Sprintf(", %d", t.Second())
	}
	if us != 0 {
		hms += fmt.Sprintf(", %d", us)
	}
	ymd := fmt.Sprintf("%d, %d, %d", t.Year(), t.Month(), t.Day())
	switch t.Location().String() {
	case localDate:
		return "datetime.date(" + ymd + ")"
	case localTime:
		return "datetime.time(" + hms + ")"
	case localDatetime:
		return "datetime.datetime(" + ymd + ", " + hms + ")"
	}
	_, off := t.Zone()
	tz := "datetime.timezone.utc"
	if off != 0 {
		// timedelta(seconds=off), as Python normalises it.
		days := off / 86400
		if off < 0 && off%86400 != 0 {
			days--
		}
		var parts []string
		if days != 0 {
			parts = append(parts, fmt.Sprintf("days=%d", days))
		}
		if secs := off - days*86400; secs != 0 {
			parts = append(parts, fmt.Sprintf("seconds=%d", secs))
		}
		tz = "datetime.timezone(datetime.timedelta(" +
			strings.Join(parts, ", ") + "))"
	}
	return "datetime.datetime(" + ymd + ", " + hms + ", tzinfo=" + tz +
		")"
}

func repr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return ReprString(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return Repr(x)
	case json.Number:
		return numberStr(string(x))
	case time.Time:
		return timeRepr(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = repr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []map[string]any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = repr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = ReprString(k) + ": " + repr(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return "?"
}

// numberStr is str() of the number Python's json reads from text: an
// int when there is no point or exponent, else a float.
func numberStr(s string) string {
	if !strings.ContainsAny(s, ".eE") {
		n, ok := new(big.Int).SetString(s, 10)
		if ok {
			return n.String()
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// Out of range reads as inf in Python too.
		if strings.HasPrefix(s, "-") {
			return "-inf"
		}
		return "inf"
	}
	return Repr(f)
}

// FloorDiv is a // b for floats.
func FloorDiv(a, b float64) float64 {
	_, div := divmod(a, b)
	return div
}

// Mod is a % b for floats: the sign of b, as Python.
func Mod(a, b float64) float64 {
	mod, _ := divmod(a, b)
	return mod
}

// divmod is CPython's float_divmod.
func divmod(vx, wx float64) (mod, floordiv float64) {
	if wx == 0 {
		return math.NaN(), math.NaN()
	}
	mod = math.Mod(vx, wx)
	div := (vx - mod) / wx
	if mod != 0 {
		if (wx < 0) != (mod < 0) {
			mod += wx
			div -= 1.0
		}
	} else {
		mod = math.Copysign(0, wx)
	}
	if div != 0 {
		floordiv = math.Floor(div)
		if div-floordiv > 0.5 {
			floordiv += 1.0
		}
	} else {
		floordiv = math.Copysign(0, vx/wx)
	}
	return mod, floordiv
}
