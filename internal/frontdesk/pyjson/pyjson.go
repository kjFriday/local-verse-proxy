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
	"bytes"
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
func (o Options) Marshal(v any) []byte { return o.MarshalSized(v, 0) }

// MarshalSized is Marshal with room for about size bytes made at
// first (the size of the last answer, say).
func (o Options) MarshalSized(v any, size int) []byte {
	var b strings.Builder
	b.Grow(size)
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
	// The plain part as one piece: most text needs no escape.
	n := 0
	for n < len(s) && s[n] >= ' ' && s[n] <= '~' && s[n] != '"' &&
		s[n] != '\\' {
		n++
	}
	b.WriteString(s[:n])
	for _, r := range s[n:] {
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
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	// Most answers are plain, well-formed JSON: read those in one
	// pass. Anything else (an error, a deep or odd text) goes the
	// slow way, which gives the same value and the same error text.
	p := parser{b: body}
	if v, ok := p.value(0); ok {
		p.space()
		if p.i == len(p.b) {
			return v, nil
		}
	}
	return decodeSlow(body)
}

// decodeSlow is Decode token by token with encoding/json.
func decodeSlow(body []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(body))
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

// fastDepth is how deep the one-pass reader goes before it leaves
// the text to decodeSlow.
const fastDepth = 512

// parser reads strict JSON (RFC 8259, as encoding/json's scanner) in
// one pass, giving what decodeValue gives. ok false: not sure, use
// decodeSlow.
type parser struct {
	b    []byte
	i    int
	keys map[string]string // object keys, made once each
	vals []any             // items of the arrays being read
	kvs  []KV              // pairs of the objects being read
	maps []map[string]int  // big objects' keys, by depth
}

func (p *parser) space() {
	for p.i < len(p.b) {
		switch p.b[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) value(depth int) (any, bool) {
	if depth > fastDepth {
		return nil, false
	}
	p.space()
	if p.i >= len(p.b) {
		return nil, false
	}
	switch c := p.b[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		return p.str()
	case c == '-' || c >= '0' && c <= '9':
		return p.number()
	case c == 't':
		return true, p.word("true")
	case c == 'f':
		return false, p.word("false")
	case c == 'n':
		return nil, p.word("null")
	}
	return nil, false
}

func (p *parser) word(w string) bool {
	if !bytes.HasPrefix(p.b[p.i:], []byte(w)) {
		return false
	}
	p.i += len(w)
	return true
}

func (p *parser) array(depth int) (any, bool) {
	p.i++ // [
	p.space()
	if p.i < len(p.b) && p.b[p.i] == ']' {
		p.i++
		return []any{}, true
	}
	// The items gather on a stack shared with nested arrays, then
	// move to a slice of just their size.
	base := len(p.vals)
	defer func() { p.vals = p.vals[:base] }()
	for {
		v, ok := p.value(depth + 1)
		if !ok {
			return nil, false
		}
		p.vals = append(p.vals, v)
		p.space()
		if p.i >= len(p.b) {
			return nil, false
		}
		switch p.b[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return append([]any(nil), p.vals[base:]...), true
		default:
			return nil, false
		}
	}
}

func (p *parser) object(depth int) (any, bool) {
	p.i++ // {
	p.space()
	if p.i < len(p.b) && p.b[p.i] == '}' {
		p.i++
		return Obj{}, true
	}
	// As array: the pairs gather on a shared stack. Big objects (a
	// key card has about 60 fields) find repeated keys with a map,
	// one per depth, kept for the next object there.
	base := len(p.kvs)
	defer func() { p.kvs = p.kvs[:base] }()
	var at map[string]int
	for {
		p.space()
		if p.i >= len(p.b) || p.b[p.i] != '"' {
			return nil, false
		}
		k, ok := p.key()
		if !ok {
			return nil, false
		}
		p.space()
		if p.i >= len(p.b) || p.b[p.i] != ':' {
			return nil, false
		}
		p.i++
		v, ok := p.value(depth + 1)
		if !ok {
			return nil, false
		}
		// A repeated key keeps its first place and its last value.
		out := p.kvs[base:]
		i := -1
		if at != nil {
			if j, ok := at[k]; ok {
				i = j
			}
		} else {
			for j := range out {
				if out[j].K == k {
					i = j
					break
				}
			}
		}
		if i >= 0 {
			out[i].V = v
		} else {
			p.kvs = append(p.kvs, KV{k, v})
			n := len(p.kvs) - base
			if at != nil {
				at[k] = n - 1
			} else if n == 16 {
				at = p.keyMap(depth)
				for j, kv := range p.kvs[base:] {
					at[kv.K] = j
				}
			}
		}
		p.space()
		if p.i >= len(p.b) {
			return nil, false
		}
		switch p.b[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return append(Obj(nil), p.kvs[base:]...), true
		default:
			return nil, false
		}
	}
}

// keyMap is the empty map for an object at depth.
func (p *parser) keyMap(depth int) map[string]int {
	for len(p.maps) <= depth {
		p.maps = append(p.maps, nil)
	}
	if p.maps[depth] == nil {
		p.maps[depth] = make(map[string]int, 64)
	}
	clear(p.maps[depth])
	return p.maps[depth]
}

// key reads an object's key: a plain one is made once per Decode,
// as the same few names come back in every card.
func (p *parser) key() (string, bool) {
	start := p.i
	for j := p.i + 1; j < len(p.b); j++ {
		c := p.b[j]
		if c == '"' {
			inner := p.b[start+1 : j]
			if k, ok := p.keys[string(inner)]; ok {
				p.i = j + 1
				return k, true
			}
			break
		}
		if c < 0x20 || c == '\\' || c >= 0x80 {
			break
		}
	}
	v, ok := p.str()
	if !ok {
		return "", false
	}
	k := v.(string)
	if p.i-start-2 == len(k) { // plain: the text is the key
		if p.keys == nil {
			p.keys = map[string]string{}
		}
		if len(p.keys) < 4096 {
			p.keys[k] = k
		}
	}
	return k, true
}

// str reads a string. Plain ones (no escape, valid UTF-8) are taken
// as they are; the rest are unquoted by encoding/json itself, so
// escapes, bad UTF-8 and lone surrogates read exactly as before.
func (p *parser) str() (any, bool) {
	start := p.i
	p.i++ // "
	plain := true
	for p.i < len(p.b) {
		c := p.b[p.i]
		switch {
		case c == '"':
			p.i++
			raw := p.b[start:p.i]
			inner := raw[1 : len(raw)-1]
			if plain && utf8.Valid(inner) {
				return string(inner), true
			}
			var s string
			if json.Unmarshal(raw, &s) != nil {
				return nil, false
			}
			return s, true
		case c < 0x20:
			return nil, false
		case c == '\\':
			plain = false
			p.i++
			if p.i >= len(p.b) {
				return nil, false
			}
			switch p.b[p.i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				p.i++
			case 'u':
				if p.i+4 >= len(p.b) {
					return nil, false
				}
				for _, h := range p.b[p.i+1 : p.i+5] {
					if !isHex(h) {
						return nil, false
					}
				}
				p.i += 5
			default:
				return nil, false
			}
		default:
			p.i++
		}
	}
	return nil, false
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' ||
		c >= 'A' && c <= 'F'
}

func (p *parser) digits() int {
	n := 0
	for p.i < len(p.b) && p.b[p.i] >= '0' && p.b[p.i] <= '9' {
		p.i++
		n++
	}
	return n
}

// number reads -?(0|[1-9][0-9]*)(.[0-9]+)?([eE][+-]?[0-9]+)? and keeps
// its text, as UseNumber does.
func (p *parser) number() (any, bool) {
	start := p.i
	if p.b[p.i] == '-' {
		p.i++
	}
	if p.i >= len(p.b) {
		return nil, false
	}
	if p.b[p.i] == '0' {
		p.i++
	} else if p.digits() == 0 {
		return nil, false
	}
	if p.i < len(p.b) && p.b[p.i] == '.' {
		p.i++
		if p.digits() == 0 {
			return nil, false
		}
	}
	if p.i < len(p.b) && (p.b[p.i] == 'e' || p.b[p.i] == 'E') {
		p.i++
		if p.i < len(p.b) && (p.b[p.i] == '+' || p.b[p.i] == '-') {
			p.i++
		}
		if p.digits() == 0 {
			return nil, false
		}
	}
	return json.Number(p.b[start:p.i]), true
}

// O makes an Obj from keys and values in turn: O("a", 1, "b", 2).
func O(kv ...any) Obj {
	o := make(Obj, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		o = append(o, KV{K: kv[i].(string), V: kv[i+1]})
	}
	return o
}
