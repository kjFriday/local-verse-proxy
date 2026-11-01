// Package pyjson writes JSON the way Python's json.dumps does, so
// Front Desk's answers keep frontdesk.py's bytes: keys in the order
// given, ASCII only (\uXXXX escapes, as ensure_ascii), Python's short
// escapes, floats as repr() prints them, and ints as ints.
//
// Values it takes: nil, bool, string, int, int64, float64, Num,
// json.Number (re-spelt as Python's json.loads then json.dumps would),
// Obj, []any, []string, []Obj and map[string]any (sorted keys: Python
// would keep the proxy's order; ledger F-5).
package pyjson

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// KV is one key and its value.
type KV struct {
	K string
	V any
}

// Obj is a JSON object with its keys in order, as a Python dict.
type Obj []KV

// Get finds a key's value.
func (o Obj) Get(k string) (any, bool) {
	for _, kv := range o {
		if kv.K == k {
			return kv.V, true
		}
	}
	return nil, false
}

// Num is a Python number: an int (Int) or a float.
type Num struct {
	F   float64
	Int bool
}

// Float is a Python float.
func Float(f float64) Num { return Num{F: f} }

// IntNum is a Python int.
func IntNum(n int64) Num { return Num{F: float64(n), Int: true} }

// Add is a + b: an int only if both are.
func (a Num) Add(b Num) Num {
	return Num{F: a.F + b.F, Int: a.Int && b.Int}
}

// FromJSON reads a json.Number as Python's json.loads would: an int
// for an integer literal, else a float.
func FromJSON(n json.Number) Num {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		f, _ := strconv.ParseFloat(s, 64)
		return Num{F: f, Int: true}
	}
	f, _ := strconv.ParseFloat(s, 64)
	return Num{F: f}
}

// Options are the two ways frontdesk.py called json.dumps.
type Options struct {
	Compact bool // separators=(",", ":"); else ", " and ": "
}

// Marshal writes v with Python's default separators.
func Marshal(v any) []byte { return Options{}.Marshal(v) }

// Compact writes v as json.dumps(v, separators=(",", ":")).
func Compact(v any) []byte { return Options{Compact: true}.Marshal(v) }

// Marshal writes v.
func (o Options) Marshal(v any) []byte {
	var b strings.Builder
	o.write(&b, v)
	return []byte(b.String())
}

func (o Options) seps() (string, string) {
	if o.Compact {
		return ",", ":"
	}
	return ", ", ": "
}

func (o Options) write(b *strings.Builder, v any) {
	item, colon := o.seps()
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
		writeString(b, x)
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case float64:
		writeFloat(b, x)
	case Num:
		writeNum(b, x)
	case *Num:
		if x == nil {
			b.WriteString("null")
		} else {
			writeNum(b, *x)
		}
	case json.Number:
		writeNumber(b, x)
	case Obj:
		b.WriteByte('{')
		for i, kv := range x {
			if i > 0 {
				b.WriteString(item)
			}
			writeString(b, kv.K)
			b.WriteString(colon)
			o.write(b, kv.V)
		}
		b.WriteByte('}')
	case []Obj:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(item)
			}
			o.write(b, e)
		}
		b.WriteByte(']')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(item)
			}
			o.write(b, e)
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(item)
			}
			writeString(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteString(item)
			}
			writeString(b, k)
			b.WriteString(colon)
			o.write(b, x[k])
		}
		b.WriteByte('}')
	default:
		// Never expected: say what it was, as text.
		writeString(b, fmt.Sprint(x))
	}
}

func writeNum(b *strings.Builder, n Num) {
	if n.Int && !math.IsInf(n.F, 0) && !math.IsNaN(n.F) {
		b.WriteString(strconv.FormatFloat(n.F, 'f', -1, 64))
		return
	}
	writeFloat(b, n.F)
}

// writeFloat is float.__repr__, with json's words for the others.
func writeFloat(b *strings.Builder, f float64) {
	switch {
	case math.IsNaN(f):
		b.WriteString("NaN")
	case math.IsInf(f, 1):
		b.WriteString("Infinity")
	case math.IsInf(f, -1):
		b.WriteString("-Infinity")
	default:
		b.WriteString(pyfmt.Repr(f))
	}
}

// writeNumber keeps an integer literal's digits (Python's int), and
// re-spells any other number as a float's repr().
func writeNumber(b *strings.Builder, n json.Number) {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		s = strings.TrimPrefix(s, "-")
		if strings.TrimLeft(s, "0") == "" {
			b.WriteString("0")
			return
		}
		b.WriteString(string(n))
		return
	}
	f, _ := strconv.ParseFloat(s, 64)
	writeFloat(b, f)
}

const hexDigits = "0123456789abcdef"

// writeString is json.dumps's ensure_ascii string: everything outside
// space to ~ escaped, \" \\ \n \r \t \b \f short.
func writeString(b *strings.Builder, s string) {
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
			if r >= ' ' && r <= '~' {
				b.WriteRune(r)
				continue
			}
			if r == utf8.RuneError {
				r = 0xfffd
			}
			if r > 0xffff {
				r -= 0x10000
				esc(b, 0xd800+(r>>10))
				esc(b, 0xdc00+(r&0x3ff))
				continue
			}
			esc(b, r)
		}
	}
	b.WriteByte('"')
}

func esc(b *strings.Builder, r rune) {
	b.WriteString(`\u`)
	for shift := 12; shift >= 0; shift -= 4 {
		b.WriteByte(hexDigits[(r>>shift)&0xf])
	}
}

// Val is the key's value, or nil (Python's dict.get).
func (o Obj) Val(k string) any {
	v, _ := o.Get(k)
	return v
}

// MaxDepth is how deep Decode goes into arrays and objects.
const MaxDepth = 10000

// Decode reads one JSON value as Python's json.loads does, but for
// NaN and Infinity, which it refuses (ledger F-10): objects as Obj
// with their keys in order (a repeated key keeps its first place and
// its last value), numbers as json.Number. A UTF-8 byte order mark is
// dropped. Anything after the value is an error.
func Decode(body []byte) (any, error) {
	body = []byte(strings.TrimPrefix(string(body), "\xef\xbb\xbf"))
	d := json.NewDecoder(strings.NewReader(string(body)))
	d.UseNumber()
	v, err := decodeValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err == nil {
		return nil, fmt.Errorf("Extra data")
	} else if err.Error() != "EOF" {
		return nil, err
	}
	return v, nil
}

func decodeValue(d *json.Decoder, depth int) (any, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("nested too deep")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '[':
			out := []any{}
			for d.More() {
				v, err := decodeValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return out, nil
		case '{':
			out := Obj{}
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				v, err := decodeValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				replaced := false
				for i := range out {
					if out[i].K == k {
						out[i].V = v
						replaced = true
						break
					}
				}
				if !replaced {
					out = append(out, KV{k, v})
				}
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return out, nil
		}
		return nil, fmt.Errorf("unexpected %v", x)
	}
	return t, nil
}

// O makes an Obj from keys and values in turn: O("a", 1, "b", 2).
func O(kv ...any) Obj {
	o := make(Obj, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		o = append(o, KV{K: kv[i].(string), V: kv[i+1]})
	}
	return o
}
