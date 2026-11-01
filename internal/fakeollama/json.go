package fakeollama

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// object is a JSON object that keeps its keys' order, as a Python
// dict does, so what comes in goes back out the same way.
type object struct {
	keys []string
	vals map[string]any
}

// obj makes an object of key, value pairs.
func obj(kv ...any) *object {
	o := &object{vals: map[string]any{}}
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func (o *object) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) lookup(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// get is dict.get(k): nil if absent.
func (o *object) get(k string) any { return o.vals[k] }

// loads is json.loads for UTF-8 text (a lone surrogate escape reads
// as U+FFFD: ledger P-3): objects as *object, numbers as
// json.Number.
func loads(data []byte) (any, error) {
	// json.loads of bytes reads a UTF-8 byte order mark as
	// utf-8-sig, and skips it.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		return nil, errors.New("not UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := value(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("extra data")
	}
	return v, nil
}

func value(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch d := t.(type) {
	case json.Delim:
		switch d {
		case '{':
			o := obj()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("bad key")
				}
				v, err := value(dec)
				if err != nil {
					return nil, err
				}
				o.set(k, v)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			list := []any{}
			for dec.More() {
				v, err := value(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
			_, err := dec.Token()
			return list, err
		}
		return nil, errors.New("bad delimiter")
	}
	return t, nil
}

// pyNum is a JSON number as Python reads it: an int (as digits) or a
// float.
func pyNum(n json.Number) (digits string, f float64, isInt bool) {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		neg := strings.HasPrefix(s, "-")
		s = strings.TrimLeft(strings.TrimPrefix(s, "-"), "0")
		if s == "" {
			return "0", 0, true
		}
		if neg {
			s = "-" + s
		}
		return s, 0, true
	}
	f, _ = strconv.ParseFloat(s, 64) // out of range: ±Inf, as float()
	return "", f, false
}

// truthy is Python's bool() of a JSON value.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		d, f, isInt := pyNum(x)
		if isInt {
			return d != "0"
		}
		return f != 0
	case int64:
		return x != 0
	case []any:
		return len(x) > 0
	case *object:
		return len(x.keys) > 0
	}
	return true
}

// intOf is int() of a JSON number; ok false where Python raised.
func intOf(text string) (int64, bool) {
	d, f, isInt := pyNum(json.Number(text))
	if isInt {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil {
			if strings.HasPrefix(d, "-") {
				return math.MinInt64, true
			}
			return math.MaxInt64, true
		}
		return n, true
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	t := math.Trunc(f)
	if t >= math.MaxInt64 {
		return math.MaxInt64, true
	}
	if t <= math.MinInt64 {
		return math.MinInt64, true
	}
	return int64(t), true
}

// intText is Python's int(text): spaces around it, a sign, decimal
// digits of any script, single _ between digits.
func intText(text string) (int64, bool) {
	s := []rune(pyfmt.Strip(text))
	neg := false
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if len(s) == 0 || s[0] == '_' || s[len(s)-1] == '_' {
		return 0, false
	}
	var n int64
	big := false
	for i, r := range s {
		if r == '_' {
			if s[i-1] == '_' {
				return 0, false
			}
			continue
		}
		d, ok := digit(r)
		if !ok {
			return 0, false
		}
		if n > (math.MaxInt64-d)/10 {
			big = true
			continue
		}
		n = n*10 + d
	}
	if big {
		n = math.MaxInt64
	}
	if neg {
		n = -n
	}
	return n, true
}

func digit(r rune) (int64, bool) {
	if r >= '0' && r <= '9' {
		return int64(r - '0'), true
	}
	if !unicode.Is(unicode.Nd, r) {
		return 0, false
	}
	start := r
	for unicode.Is(unicode.Nd, start-1) {
		start--
	}
	return int64(r-start) % 10, true
}

// dumps is Python's json.dumps(v) with its defaults: ", " and ": ",
// ASCII only.
func dumps(v any) string {
	var b strings.Builder
	write(&b, v)
	return b.String()
}

func write(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		b.WriteString(quote(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case int:
		b.WriteString(strconv.Itoa(x))
	case float64:
		b.WriteString(floatJSON(x))
	case json.Number:
		d, f, isInt := pyNum(x)
		if isInt {
			b.WriteString(d)
		} else {
			b.WriteString(floatJSON(f))
		}
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			write(b, e)
		}
		b.WriteByte(']')
	case *object:
		b.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quote(k) + ": ")
			write(b, x.vals[k])
		}
		b.WriteByte('}')
	default:
		b.WriteString(quote(fmt.Sprint(x)))
	}
}

func floatJSON(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return pyfmt.Repr(f)
}

// quote is json.dumps of a str: ASCII only, \uXXXX outside space to
// tilde, surrogate pairs above U+FFFF.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r >= ' ' && r <= '~':
				b.WriteRune(r)
			case r > 0xFFFF:
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xD800+(r>>10),
					0xDC00+(r&0x3FF))
			default:
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// str is Python's str() of a JSON value.
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return repr(v)
}

// repr is Python's repr() of a JSON value.
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
		return pyfmt.ReprString(x)
	case json.Number:
		d, f, isInt := pyNum(x)
		if isInt {
			return d
		}
		return pyfmt.Repr(f)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = repr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *object:
		parts := make([]string, len(x.keys))
		for i, k := range x.keys {
			parts[i] = pyfmt.ReprString(k) + ": " + repr(x.vals[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}
