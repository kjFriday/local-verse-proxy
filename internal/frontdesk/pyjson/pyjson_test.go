package pyjson

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The answers are json.dumps's, from Python 3.13.
func TestMarshal(t *testing.T) {
	tenth := 0.1
	for _, c := range []struct {
		v    any
		want string
	}{
		{O("a", "\u00e9<>&/\x7f\u2028", "b", []any{IntNum(1),
			Float(2)}), "{\"a\": \"\\u00e9<>&/\\u007f\\u2028\", " +
			"\"b\": [1, 2.0]}"},
		{O("e", "\U0001f600\"\\\n\r\t\b\f\x01"),
			`{"e": "\ud83d\ude00\"\\\n\r\t\b\f\u0001"}`},
		{[]any{nil, true, false, json.Number("5"),
			json.Number("5.0"), json.Number("-0"),
			json.Number("1e3"), json.Number("1E400"),
			json.Number("0.00001")},
			"[null, true, false, 5, 5.0, 0, 1000.0, Infinity, " +
				"1e-05]"},
		{map[string]any{"z": 1.5, "a": []string{"x"}},
			`{"a": ["x"], "z": 1.5}`},
		{Float(tenth + float64(2*tenth)), `0.30000000000000004`},
		{IntNum(3).Add(Float(0.5)), `3.5`},
		{IntNum(3).Add(IntNum(4)), `7`},
	} {
		if got := string(Marshal(c.v)); got != c.want {
			t.Errorf("got  %s\nwant %s", got, c.want)
		}
	}
	if got := string(Compact(O("a", []Obj{O("b", 1)}))); got !=
		`{"a":[{"b":1}]}` {
		t.Errorf("compact: %s", got)
	}
}

func TestDecode(t *testing.T) {
	v, err := Decode([]byte("\xef\xbb\xbf{\"b\": 1, \"a\": 2, \"b\": 3}"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(Compact(v)); got != `{"b":3,"a":2}` {
		t.Errorf("order and repeats: %s", got)
	}
	for _, bad := range []string{"NaN", `{"a": Infinity}`, "{} {}", "{",
		"[" + string(make([]byte, 0)) + "1,]"} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	deep := make([]byte, 0, 2*MaxDepth+4)
	for i := 0; i < MaxDepth+2; i++ {
		deep = append(deep, '[')
	}
	for i := 0; i < MaxDepth+2; i++ {
		deep = append(deep, ']')
	}
	if _, err := Decode(deep); err == nil {
		t.Error("no limit on depth")
	}
}

// sameDecode checks that Decode gives what the token-by-token reader
// gives: the same value, or an error with the same text.
func sameDecode(t *testing.T, in []byte) {
	t.Helper()
	got, gerr := Decode(in)
	want, werr := decodeSlow(bytes.TrimPrefix(in, []byte("\xef\xbb\xbf")))
	if (gerr == nil) != (werr == nil) ||
		gerr != nil && gerr.Error() != werr.Error() {
		t.Fatalf("%q: error %v, want %v", in, gerr, werr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q: got %#v, want %#v", in, got, want)
	}
}

var decodeSeeds = []string{
	``, ` `, `null`, `true`, `false`, `tru`, `nul`, `0`, `-0`, `01`,
	`-`, `1.`, `.5`, `1e`, `1e+`, `1E-07`, `-12.5e3`, `1.5e400`,
	`""`, `"a"`, `"é"`, `"\ud800"`, `"😀"`, `"\x"`,
	`"\u12"`, `"\u12g4"`, "\"\xff\"", "\"\xc3\xa9\"", "\"a\x01\"",
	`"\/\b\f\n\r\t\"\\"`, `[]`, `[ ]`, `[1,]`, `[,1]`, `[1 2]`,
	`{}`, `{ }`, `{"a":1,}`, `{"a" 1}`, `{1:2}`, `{"a":1,"a":2}`,
	`{"b": 1, "a": [true, null, {"c": "d"}], "b": 3}`, `{} {}`,
	`[1] `, " \n\t\r[1]\r\n", `NaN`, `[Infinity]`, `[-Infinity]`,
	"\xef\xbb\xbf[1]", `{"kA": "v"}`, `{"a":"\u0000"}`,
}

func TestDecodeSame(t *testing.T) {
	for _, s := range decodeSeeds {
		sameDecode(t, []byte(s))
	}
	deep := strings.Repeat("[", fastDepth+3) + strings.Repeat("]",
		fastDepth+3)
	sameDecode(t, []byte(deep))
	sameDecode(t, []byte(`{"x": `+deep+`}`))
}

func FuzzDecode(f *testing.F) {
	for _, s := range decodeSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) { sameDecode(t, in) })
}
