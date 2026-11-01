package pyjson

import (
	"encoding/json"
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
