// Package pyfmt does what Python does with numbers and text, exactly,
// where the Go port must print or read the same bytes: format()
// specs, round(), repr() and str(), str.strip(), splitlines(),
// split() and lower(), datetime.fromisoformat() and isoformat(),
// urlsplit()'s host and port, ipaddress, and the C library's words
// for OS errors.
//
// Each function names the Python it stands for. The goldens in
// testdata/golden come from Python 3.13 (tests/golden/pyfmt.py).
package pyfmt

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// spec is a parsed format spec:
// [[fill]align][sign][z][#][0][width][grouping][.precision][type].
type spec struct {
	fill     rune
	align    byte // 0 for the default
	sign     byte // 0, '+', '-' or ' '
	alt      bool
	zero     bool
	width    int
	group    byte // 0, ',' or '_'
	prec     int  // -1: none
	typ      byte // 0 for none
	hasFill  bool
	negZeroZ bool
}

func parseSpec(s string) (spec, error) {
	sp := spec{fill: ' ', prec: -1}
	r := []rune(s)
	i := 0
	isAlign := func(c rune) bool {
		return c == '<' || c == '>' || c == '=' || c == '^'
	}
	if len(r) >= 2 && isAlign(r[1]) {
		sp.fill, sp.align, sp.hasFill = r[0], byte(r[1]), true
		i = 2
	} else if len(r) >= 1 && isAlign(r[0]) {
		sp.align = byte(r[0])
		i = 1
	}
	if i < len(r) && (r[i] == '+' || r[i] == '-' || r[i] == ' ') {
		sp.sign = byte(r[i])
		i++
	}
	if i < len(r) && r[i] == 'z' {
		sp.negZeroZ = true
		i++
	}
	if i < len(r) && r[i] == '#' {
		sp.alt = true
		i++
	}
	if i < len(r) && r[i] == '0' {
		sp.zero = true
		i++
	}
	start := i
	for i < len(r) && r[i] >= '0' && r[i] <= '9' {
		i++
	}
	if i > start {
		sp.width, _ = strconv.Atoi(string(r[start:i]))
	}
	if i < len(r) && (r[i] == ',' || r[i] == '_') {
		sp.group = byte(r[i])
		i++
		if i < len(r) && (r[i] == ',' || r[i] == '_') {
			return sp, fmt.Errorf("cannot specify both ',' and '_'")
		}
	}
	if i < len(r) && r[i] == '.' {
		i++
		start = i
		for i < len(r) && r[i] >= '0' && r[i] <= '9' {
			i++
		}
		if i == start {
			return sp, fmt.Errorf("format specifier missing " +
				"precision")
		}
		sp.prec, _ = strconv.Atoi(string(r[start:i]))
	}
	if i < len(r) {
		if r[i] > 0x7f {
			return sp, fmt.Errorf("invalid format specifier %q", s)
		}
		sp.typ = byte(r[i])
		i++
	}
	if i != len(r) {
		return sp, fmt.Errorf("invalid format specifier %q", s)
	}
	return sp, nil
}

// numeric is the "0" flag for a number: zero fill, and '=' (padding
// after the sign) unless an alignment was given.
func (sp spec) numeric() spec {
	if sp.zero && !sp.hasFill {
		sp.fill = '0'
		if sp.align == 0 {
			sp.align = '='
		}
	}
	return sp
}

// Format is Python's format(v, spec): fill, align, sign, z, #, 0,
// width, "," or "_", precision and every type, for int, int64,
// float64, bool, json.Number and string. A spec Python would refuse
// for that value gives "!(...)" with the reason, never a guess.
// Other values take only "" (Python's str()).
func Format(v any, spec string) string {
	sp, err := parseSpec(spec)
	if err != nil {
		return "!(" + err.Error() + ")"
	}
	var out string
	switch x := v.(type) {
	case int:
		out, err = sp.int(int64(x))
	case int64:
		out, err = sp.int(x)
	case float64:
		out, err = sp.float(x)
	case string:
		out, err = sp.str(x)
	case json.Number:
		if n, e := x.Int64(); e == nil {
			out, err = sp.int(n)
		} else {
			f, _ := x.Float64()
			out, err = sp.float(f)
		}
	case bool:
		if spec == "" {
			return Str(x) // True or False; any other spec: an int
		}
		n := int64(0)
		if x {
			n = 1
		}
		out, err = sp.int(n)
	default:
		if spec == "" {
			return Str(v)
		}
		err = fmt.Errorf("unsupported format string for %T", v)
	}
	if err != nil {
		return "!(" + err.Error() + ")"
	}
	return out
}

func unknownType(typ byte, kind string) error {
	return fmt.Errorf("unknown format code %q for object of type %s",
		rune(typ), kind)
}

func (sp spec) str(s string) (string, error) {
	switch {
	case sp.typ != 0 && sp.typ != 's':
		return "", unknownType(sp.typ, "str")
	case sp.sign != 0:
		return "", fmt.Errorf("sign not allowed in string format " +
			"specifier")
	case sp.negZeroZ:
		return "", fmt.Errorf("negative zero coercion (z) not " +
			"allowed in string format specifier")
	case sp.alt:
		return "", fmt.Errorf("alternate form (#) not allowed in " +
			"string format specifier")
	case sp.align == '=':
		return "", fmt.Errorf("'=' alignment not allowed in string " +
			"format specifier")
	case sp.group != 0:
		return "", fmt.Errorf("cannot specify %q with 's'",
			rune(sp.group))
	}
	if sp.zero && !sp.hasFill {
		sp.fill = '0' // text keeps its alignment (Python 3.10 on)
	}
	if sp.prec >= 0 {
		s = Cut(s, sp.prec)
	}
	return sp.pad("", s, orDefault(sp.align, '<')), nil
}

func (sp spec) int(n int64) (string, error) {
	switch sp.typ {
	case 'e', 'E', 'f', 'F', 'g', 'G', '%':
		return sp.float(float64(n))
	case 0, 'b', 'c', 'd', 'o', 'x', 'X', 'n':
	default:
		return "", unknownType(sp.typ, "int")
	}
	switch {
	case sp.prec >= 0:
		return "", fmt.Errorf("precision not allowed in integer " +
			"format specifier")
	case sp.negZeroZ:
		return "", fmt.Errorf("negative zero coercion (z) not " +
			"allowed in integer format specifier")
	case sp.group == ',' && sp.typ != 0 && sp.typ != 'd',
		sp.group == '_' && (sp.typ == 'c' || sp.typ == 'n'):
		return "", fmt.Errorf("cannot specify %q with %q",
			rune(sp.group), rune(sp.typ))
	}
	sp = sp.numeric()
	neg := n < 0
	var digits string
	u := uint64(n)
	if neg {
		u = uint64(-n)
	}
	switch sp.typ {
	case 'x':
		digits = strconv.FormatUint(u, 16)
	case 'X':
		digits = strings.ToUpper(strconv.FormatUint(u, 16))
	case 'o':
		digits = strconv.FormatUint(u, 8)
	case 'b':
		digits = strconv.FormatUint(u, 2)
	case 'c':
		switch {
		case sp.sign != 0:
			return "", fmt.Errorf("sign not allowed with integer " +
				"format specifier 'c'")
		case sp.alt:
			return "", fmt.Errorf("alternate form (#) not " +
				"allowed with integer format specifier 'c'")
		case n < 0 || n > 0x10ffff:
			return "", fmt.Errorf("%%c arg not in range(0x110000)")
		}
		return sp.pad("", string(rune(n)), orDefault(sp.align, '>')),
			nil
	default:
		digits = strconv.FormatUint(u, 10)
	}
	prefix := ""
	if sp.alt {
		switch sp.typ {
		case 'x':
			prefix = "0x"
		case 'X':
			prefix = "0X"
		case 'o':
			prefix = "0o"
		case 'b':
			prefix = "0b"
		}
	}
	every := 3
	if sp.typ == 'x' || sp.typ == 'X' || sp.typ == 'o' ||
		sp.typ == 'b' {
		every = 4
	}
	return sp.number(neg, prefix, digits, "", every), nil
}

func (sp spec) float(x float64) (string, error) {
	switch sp.typ {
	case 0, 'e', 'E', 'f', 'F', 'g', 'G', 'n', '%':
	default:
		return "", unknownType(sp.typ, "float")
	}
	if sp.group != 0 && sp.typ == 'n' {
		return "", fmt.Errorf("cannot specify %q with 'n'",
			rune(sp.group))
	}
	sp = sp.numeric()
	neg := math.Signbit(x)
	a := math.Abs(x)
	typ, prec := sp.typ, sp.prec
	pct := false
	if typ == '%' {
		a *= 100
		typ, pct = 'f', true
	}
	if prec < 0 && typ != 0 {
		prec = 6
	}
	var body string
	switch {
	case math.IsInf(a, 0):
		body = "inf"
		if typ == 'F' || typ == 'E' || typ == 'G' {
			body = "INF"
		}
	case math.IsNaN(a):
		// Python never prints a NaN's sign: "nan", or "+nan" with +.
		body = "nan"
		if typ == 'F' || typ == 'E' || typ == 'G' {
			body = "NAN"
		}
		neg = false
	default:
		switch typ {
		case 'f', 'F':
			body = strconv.FormatFloat(a, 'f', prec, 64)
		case 'e', 'E':
			body = strconv.FormatFloat(a, 'e', prec, 64)
			if typ == 'E' {
				body = strings.ToUpper(body)
			}
		case 'g', 'G', 'n':
			body = formatG(a, prec, sp.alt, false)
			if typ == 'G' {
				body = strings.ToUpper(body)
			}
		default: // no type: repr, or like g with at least one decimal
			if prec < 0 {
				body = Repr(a)
			} else {
				body = formatG(a, prec, sp.alt, true)
			}
		}
		if sp.alt {
			body = withPoint(body)
		}
		// z: a negative number that rounds to zero prints as zero.
		if sp.negZeroZ && isZero(body) {
			neg = false
		}
	}
	if pct {
		body += "%"
	}
	intPart, rest := body, ""
	if k := strings.IndexAny(body, ".eE%"); k >= 0 {
		intPart, rest = body[:k], body[k:]
	}
	if math.IsInf(a, 0) || math.IsNaN(a) {
		intPart, rest = "", body
	}
	return sp.number(neg, "", intPart, rest, 3), nil
}

// withPoint is the "#" form: a decimal point always, before any
// exponent.
func withPoint(body string) string {
	k := strings.IndexAny(body, "eE")
	mant, exp := body, ""
	if k >= 0 {
		mant, exp = body[:k], body[k:]
	}
	if !strings.Contains(mant, ".") {
		mant += "."
	}
	return mant + exp
}

// isZero: the digits before any exponent are all zeros.
func isZero(body string) bool {
	if k := strings.IndexAny(body, "eE"); k >= 0 {
		body = body[:k]
	}
	return strings.Trim(body, "0.") == ""
}

// formatG is Python's 'g' (and, with noType, the no-type form with a
// precision): p significant digits, fixed or exponent.
func formatG(a float64, prec int, alt, noType bool) string {
	p := prec
	if p < 0 {
		p = 6
	}
	if p == 0 {
		p = 1
	}
	e := strconv.FormatFloat(a, 'e', p-1, 64)
	k := strings.IndexByte(e, 'e')
	exp, _ := strconv.Atoi(e[k+1:])
	// Python switches to an exponent at p digits, or at p-1 for the
	// no-type form, which keeps a digit after the point.
	limit := p
	if noType {
		limit = p - 1
	}
	var out string
	if exp < -4 || exp >= limit {
		mant := e[:k]
		if !alt {
			mant = trimZeros(mant)
		}
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		out = fmt.Sprintf("%se%s%02d", mant, sign, exp)
	} else {
		out = strconv.FormatFloat(a, 'f', p-1-exp, 64)
		if !alt {
			out = trimZeros(out)
		}
		if noType && !strings.Contains(out, ".") {
			out += ".0"
		}
	}
	return out
}

func trimZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func orDefault(b, def byte) byte {
	if b == 0 {
		return def
	}
	return b
}

// number lays out a sign, prefix, integer digits (grouped) and the
// rest, padded as the spec says.
func (sp spec) number(neg bool, prefix, digits, rest string,
	every int) string {
	sign := ""
	switch {
	case neg:
		sign = "-"
	case sp.sign == '+':
		sign = "+"
	case sp.sign == ' ':
		sign = " "
	}
	align := orDefault(sp.align, '>')
	if align == '=' && sp.fill == '0' && sp.group != 0 && digits != "" {
		// Python puts the separators into the zero padding too.
		want := sp.width - len(sign) - len(prefix) -
			utf8.RuneCountInString(rest)
		for {
			g := group(digits, sp.group, every)
			if len(g) >= want {
				return sign + prefix + g + rest
			}
			digits = "0" + digits
		}
	}
	if sp.group != 0 {
		digits = group(digits, sp.group, every)
	}
	return sp.pad(sign+prefix, digits+rest, align)
}

func group(digits string, sep byte, every int) string {
	if len(digits) <= every {
		return digits
	}
	var b strings.Builder
	first := len(digits) % every
	if first == 0 {
		first = every
	}
	b.WriteString(digits[:first])
	for i := first; i < len(digits); i += every {
		b.WriteByte(sep)
		b.WriteString(digits[i : i+every])
	}
	return b.String()
}

func (sp spec) pad(sign, body string, align byte) string {
	n := utf8.RuneCountInString(sign) + utf8.RuneCountInString(body)
	if n >= sp.width {
		return sign + body
	}
	fill := strings.Repeat(string(sp.fill), sp.width-n)
	switch align {
	case '<':
		return sign + body + fill
	case '^':
		left := (sp.width - n) / 2
		return strings.Repeat(string(sp.fill), left) + sign + body +
			strings.Repeat(string(sp.fill), sp.width-n-left)
	case '=':
		return sign + fill + body
	}
	return fill + sign + body
}
