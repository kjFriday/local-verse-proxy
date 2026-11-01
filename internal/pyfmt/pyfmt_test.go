package pyfmt

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// golden is testdata/golden/pyfmt.json, from tests/golden/pyfmt.py.
type golden struct {
	Python   []string          `json:"python"`
	Format   [][]any           `json:"format"`
	Fuzz     [][]any           `json:"format_fuzz"`
	Round    [][]any           `json:"round"`
	Repr     [][]string        `json:"repr"`
	Str      [][]string        `json:"str"`
	Text     []textCase        `json:"text"`
	ReprStr  [][]string        `json:"reprstr"`
	ISO      [][]any           `json:"iso"`
	Hosts    []map[string]any  `json:"hosts"`
	IPs      [][]any           `json:"ips"`
	FloorDiv [][]string        `json:"floordiv"`
	Errno    [][]any           `json:"errno_linux"`
	OSError  [][]string        `json:"oserror_linux"`
	Extra    map[string]string `json:"-"`
}

type textCase struct {
	S     string   `json:"s"`
	Strip string   `json:"strip"`
	Split []string `json:"split"`
	Lines []string `json:"lines"`
	Lower string   `json:"lower"`
	Cut3  string   `json:"cut3"`
	Repr  string   `json:"repr"`
}

func load(t *testing.T) golden {
	t.Helper()
	b, err := os.ReadFile("testdata/golden/pyfmt.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&g); err != nil {
		t.Fatal(err)
	}
	return g
}

func bits(t *testing.T, s any) float64 {
	t.Helper()
	u, err := strconv.ParseUint(s.(string), 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	return math.Float64frombits(u)
}

// formatValue is a golden format case's value, or false for an int
// beyond int64, which is not a Go case.
func formatValue(t *testing.T, kind string, enc any) (any, bool) {
	switch kind {
	case "int":
		n, err := enc.(json.Number).Int64()
		return n, err == nil
	case "float":
		return bits(t, enc), true
	case "bool":
		return enc.(bool), true
	}
	return enc.(string), true
}

func TestFormat(t *testing.T) {
	g := load(t)
	for _, c := range g.Format {
		kind, spec, want := c[0].(string), c[2].(string), c[3].(string)
		v, ok := formatValue(t, kind, c[1])
		if !ok {
			continue
		}
		if got := Format(v, spec); got != want {
			t.Errorf("format(%v %s, %q) = %q, Python %q", v, kind,
				spec, got, want)
		}
	}
}

// TestFormatFuzz is random specs: where Python printed, Go prints the
// same; where Python refused, Go says "!(...)".
func TestFormatFuzz(t *testing.T) {
	g := load(t)
	if len(g.Fuzz) < 1000 {
		t.Fatalf("%d fuzz cases; want 1000 or more", len(g.Fuzz))
	}
	bad := 0
	for _, c := range g.Fuzz {
		kind, spec := c[0].(string), c[2].(string)
		v, ok := formatValue(t, kind, c[1])
		if !ok {
			continue
		}
		got := Format(v, spec)
		want, printed := c[3].(string)
		switch {
		case !printed && !strings.HasPrefix(got, "!("):
			t.Errorf("format(%v %s, %q) = %q; Python refused it",
				v, kind, spec, got)
		case printed && got != want:
			t.Errorf("format(%v %s, %q) = %q, Python %q", v, kind,
				spec, got, want)
		default:
			continue
		}
		if bad++; bad > 40 {
			t.Fatal("too many")
		}
	}
}

func TestRound(t *testing.T) {
	g := load(t)
	for _, c := range g.Round {
		x := bits(t, c[0])
		if c[1] == nil {
			want, err := c[2].(json.Number).Int64()
			if err != nil {
				continue // past int64
			}
			if got := RoundInt(x); got != want {
				t.Errorf("round(%v) = %d, Python %d", x, got,
					want)
			}
			continue
		}
		n, _ := c[1].(json.Number).Int64()
		want := bits(t, c[2])
		got := Round(x, int(n))
		if math.Float64bits(got) != math.Float64bits(want) &&
			!(got == 0 && want == 0) {
			t.Errorf("round(%v, %d) = %v, Python %v", x, n, got,
				want)
		}
	}
}

func TestRepr(t *testing.T) {
	g := load(t)
	for _, c := range g.Repr {
		x := bits(t, c[0])
		if got := Repr(x); got != c[1] {
			t.Errorf("repr(%s) = %q, Python %q", c[0], got, c[1])
		}
	}
}

func TestStr(t *testing.T) {
	g := load(t)
	for _, c := range g.Str {
		dec := json.NewDecoder(bytes.NewBufferString(c[0]))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			// Go's decoder refuses 1E400 only as a float;
			// it as float; UseNumber keeps the text.
			t.Fatalf("%s: %v", c[0], err)
		}
		if got := Str(v); got != c[1] {
			t.Errorf("str(json.loads(%s)) = %q, Python %q", c[0],
				got,
				c[1])
		}
	}
}

func TestText(t *testing.T) {
	g := load(t)
	for _, c := range g.Text {
		if got := Strip(c.S); got != c.Strip {
			t.Errorf("%q.strip() = %q, Python %q", c.S, got,
				c.Strip)
		}
		if got := Fields(c.S); !equalStrs(got, c.Split) {
			t.Errorf("%q.split() = %q, Python %q", c.S, got,
				c.Split)
		}
		if got := SplitLines(c.S); !equalStrs(got, c.Lines) {
			t.Errorf("%q.splitlines() = %q, Python %q", c.S, got,
				c.Lines)
		}
		if got := Lower(c.S); got != c.Lower {
			t.Errorf("%q.lower() = %q, Python %q", c.S, got,
				c.Lower)
		}
		if got := Cut(c.S, 3); got != c.Cut3 {
			t.Errorf("%q[:3] = %q, Python %q", c.S, got, c.Cut3)
		}
		if got := ReprString(c.S); got != c.Repr {
			t.Errorf("repr(%q) = %s, Python %s", c.S, got, c.Repr)
		}
	}
	for _, c := range g.ReprStr {
		if got := ReprString(c[0]); got != c[1] {
			t.Errorf("repr(%q) = %s, Python %s", c[0], got, c[1])
		}
	}
}

func equalStrs(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func TestISO(t *testing.T) {
	g := load(t)
	for _, c := range g.ISO {
		s := c[0].(string)
		tm, naive, err := ParseISO(s)
		if c[1] == nil {
			if err == nil {
				t.Errorf("fromisoformat(%q): Go read %s; "+
					"Python refused it", s, ISOFormat(tm,
					naive))
			}
			continue
		}
		if err != nil {
			t.Errorf("fromisoformat(%q): %v; Python read %s", s,
				err,
				c[1])
			continue
		}
		if naive != c[2].(bool) {
			t.Errorf("fromisoformat(%q): naive %v, Python %v", s,
				naive, c[2])
		}
		if c[3].(bool) {
			continue // a zone with microseconds: Go keeps seconds
		}
		if got := ISOFormat(tm, naive); got != c[1] {
			t.Errorf("fromisoformat(%q) as text: %s, Python %s",
				s, got, c[1])
		}
	}
}

func TestURLSplitHost(t *testing.T) {
	g := load(t)
	for _, c := range g.Hosts {
		h := c["h"].(string)
		name, port, has, err := URLSplitHost(h)
		if msg, ok := c["split_err"]; ok {
			var pe *PortError
			if err == nil || errors.As(err, &pe) {
				t.Errorf("urlsplit(//%q): Go %q; Python: %s", h,
					name, msg)
			} else if err.Error() != msg {
				t.Errorf("urlsplit(//%q): %q, Python %q", h,
					err, msg)
			}
			continue
		}
		wantName, _ := c["name"].(string)
		if name != wantName {
			t.Errorf("urlsplit(//%q).hostname = %q, Python %q", h,
				name, wantName)
		}
		if msg, ok := c["port_err"]; ok {
			var pe *PortError
			if !errors.As(err, &pe) || pe.Msg != msg {
				t.Errorf("urlsplit(//%q).port: %v, Python %q",
					h,
					err, msg)
			}
			continue
		}
		if err != nil {
			t.Errorf("urlsplit(//%q): %v", h, err)
			continue
		}
		switch p := c["port"].(type) {
		case nil:
			if has {
				t.Errorf("urlsplit(//%q).port %d, Python None",
					h, port)
			}
		case json.Number:
			n, _ := p.Int64()
			if !has || int64(port) != n {
				t.Errorf("urlsplit(//%q).port %d %v, Python %d",
					h, port, has, n)
			}
		}
	}
}

func TestIP(t *testing.T) {
	g := load(t)
	for _, c := range g.IPs {
		s := c[0].(string)
		if got := IsIP(s); got != c[1].(bool) {
			t.Errorf("ip_address(%q) accepted: %v, Python %v", s,
				got,
				c[1])
		}
		if got := IsLoopback(s); got != c[2].(bool) {
			t.Errorf("ip_address(%q).is_loopback: %v, Python %v", s,
				got, c[2])
		}
	}
}

func TestFloorDiv(t *testing.T) {
	g := load(t)
	for _, c := range g.FloorDiv {
		a, b := bits(t, c[0]), bits(t, c[1])
		d, m := FloorDiv(a, b), Mod(a, b)
		if math.Float64bits(d) != math.Float64bits(bits(t, c[2])) ||
			math.Float64bits(m) != math.Float64bits(bits(t, c[3])) {
			t.Errorf("%v // %v, %% = %v, %v; Python %v, %v", a, b,
				d, m,
				bits(t, c[2]), bits(t, c[3]))
		}
	}
	// The board's m:ss, where 02.0f can print 60.
	at := 119.6
	if got := Format(FloorDiv(at, 60), ".0f") + ":" +
		Format(Mod(at, 60), "02.0f"); got != "1:60" {
		t.Errorf("m:ss of 119.6 = %s, Python 1:60", got)
	}
}

func TestErrors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the golden is the C library's words on Linux")
	}
	g := load(t)
	for _, c := range g.Errno {
		n, _ := c[0].(json.Number).Int64()
		if got := StrError(syscall.Errno(n)); got != c[1] {
			t.Errorf("strerror(%d) = %q, Python %q", n, got, c[1])
		}
	}
	for _, c := range g.OSError {
		_, err := os.ReadFile(c[0])
		if got := OSErrorText(err, c[0]); got != c[1] {
			t.Errorf("str(OSError) = %q, Python %q", got, c[1])
		}
	}
}

func TestSpace(t *testing.T) {
	// Space is exactly what isspace() says (the golden's text cases
	// test strip and split with it); SpaceClass works in a regexp.
	re := regexp.MustCompile("^[" + SpaceClass() + "]+$")
	if !re.MatchString(Space) || re.MatchString("a") {
		t.Fatal("SpaceClass does not match Space")
	}
}

func TestPythonVersion(t *testing.T) {
	g := load(t)
	if len(g.Python) != 1 || g.Python[0] == "" {
		t.Fatal("the golden does not say which Python made it")
	}
}
