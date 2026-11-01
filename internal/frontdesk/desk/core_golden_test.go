package desk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/config"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
)

// golden reads testdata/golden/<name>: each top-level list as its
// cases, raw.
func golden(t *testing.T, name string) map[string][]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string][]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// py decodes raw JSON as Python's json.loads would (keys in order).
func py(t *testing.T, raw []byte) any {
	t.Helper()
	v, err := pyjson.Decode(raw)
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

func field(t *testing.T, raw json.RawMessage, k string) any {
	t.Helper()
	o, ok := py(t, raw).(pyjson.Obj)
	if !ok {
		t.Fatalf("not an object: %s", raw)
	}
	return o.Val(k)
}

func objs(v any) []pyjson.Obj {
	list, _ := v.([]any)
	out := []pyjson.Obj{}
	for _, x := range list {
		out = append(out, x.(pyjson.Obj))
	}
	return out
}

var goldenNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func TestGoldenSQL(t *testing.T) {
	g := golden(t, "core.json")
	var sql []string
	for _, r := range g["sql"] {
		var s string
		json.Unmarshal(r, &s)
		sql = append(sql, s)
	}
	if sql[0] != TodaySQL || sql[1] != HistorySQL {
		t.Errorf("SQL differs from frontdesk.py's")
	}
}

func TestGoldenSettings(t *testing.T) {
	dir := t.TempDir()
	for i, raw := range golden(t, "core.json")["settings"] {
		var c struct {
			Text, Master, DBURL string
			HTTPS               bool
			Host                string
			Port                int
			Defaults            struct {
				Models           []string
				Budget, Duration string
			}
			Hidden, Warnings []string
			Scrub            string
		}
		json.Unmarshal(raw, &c)
		var dburl struct {
			DB string `json:"db_url"`
		}
		json.Unmarshal(raw, &dburl)
		p := filepath.Join(dir, "seats.env")
		os.WriteFile(p, []byte(c.Text), 0o600)
		s, err := config.LoadFile(p, func(string) (string, bool) {
			return "", false
		})
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		if s.Master != c.Master || s.DBURL != dburl.DB ||
			s.HTTPS != c.HTTPS || s.Host != c.Host ||
			s.Port != c.Port ||
			strings.Join(s.Defaults.Models, "|") !=
				strings.Join(c.Defaults.Models, "|") ||
			s.Defaults.Budget != c.Defaults.Budget ||
			s.Defaults.Duration != c.Defaults.Duration ||
			strings.Join(s.Hidden, "|") != strings.Join(c.Hidden,
				"|") ||
			strings.Join(s.Warnings, "|") !=
				strings.Join(c.Warnings, "|") ||
			s.Scrub("x "+strings.Join(s.Hidden,
				" ")+" y") != c.Scrub {
			t.Errorf("%d: got %v hidden %d warn %q", i, s,
				len(s.Hidden), s.Warnings)
		}
	}
}

func TestGoldenSeatState(t *testing.T) {
	var c times
	for _, raw := range golden(t, "core.json")["states"] {
		k := field(t, raw, "key").(pyjson.Obj)
		st := field(t, raw, "state").(pyjson.Obj)
		want, ok := st.Val("ok").(string)
		if !ok {
			// Python's check failed here; Go counts the time as
			// absent (ledger F-9).
			want = field(t, raw, "if_absent").(string)
		}
		if got := c.SeatState(k, goldenNow); got != want {
			t.Errorf("%s: %s, want %s", raw, got, want)
		}
	}
}

func TestGoldenDollarsAndTimes(t *testing.T) {
	g := golden(t, "core.json")
	for _, raw := range g["dollars"] {
		got := string(pyjson.Compact(Dollars(field(t, raw, "in"))))
		want := string(pyjson.Compact(field(t, raw, "out")))
		if got != want {
			t.Errorf("dollars %s: %s", raw, got)
		}
	}
	var c times
	for _, raw := range g["utc"] {
		got := c.UTCText(field(t, raw, "in"))
		want := field(t, raw, "out")
		var gv any
		if got != nil {
			gv = *got
		}
		if string(pyjson.Compact(gv)) != string(pyjson.Compact(want)) {
			t.Errorf("utc_text %s: %v", raw, gv)
		}
	}
}

func TestGoldenMerge(t *testing.T) {
	for _, raw := range golden(t, "core.json")["merge"] {
		h := field(t, raw, "hist").(pyjson.Obj)
		td := field(t, raw, "today").(pyjson.Obj)
		m := MergeTally(objs(h.Val("rows")), objs(td.Val("rows")),
			td.Val("day"))
		got := string(pyjson.Compact(tallyObj(m, false)))
		want := string(pyjson.Compact(field(t, raw, "out")))
		if got != want {
			t.Errorf("merge:\n got %s\nwant %s", got, want)
		}
	}
}

// tallyObj is a Tally as Python's dict: day and rows, then source and
// why when set.
func tallyObj(m Tally, extra bool) pyjson.Obj {
	rows := []pyjson.Obj{}
	for _, r := range m.Rows {
		rows = append(rows, r.Obj(nil))
	}
	o := pyjson.O("day", m.Day, "rows", rows)
	if extra {
		o = append(o, pyjson.KV{K: "source", V: m.Source},
			pyjson.KV{K: "why", V: m.Why})
	}
	return o
}

// testDesk is a Desk on a fake clock at the goldens' time, with the
// golden's secret, and settings from text.
func testDesk(t *testing.T, text string, lan bool) *Desk {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "seats.env")
	os.WriteFile(p, []byte(text), 0o600)
	s, err := config.LoadFile(p, func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	log := config.NewLog(&strings.Builder{})
	clk := clock.NewFake(float64(goldenNow.Unix()), 1000)
	d := New(Options{Live: config.NewLive(s, log, nil), Log: log,
		Clock: clk, LAN: lan, Game: dir})
	var secret []byte
	for i := 0; i < 16; i++ {
		secret = append(secret, byte(i))
	}
	d.SetSecret(secret)
	return d
}

func TestGoldenHTTPTally(t *testing.T) {
	for _, raw := range golden(t, "core.json")["http_tally"] {
		act := field(t, raw, "activity")
		srv := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter, r *http.Request) {
			if s, ok := act.(string); ok && s == "proxy-error" {
				w.WriteHeader(500)
				return
			}
			w.Write(pyjson.Compact(act))
		}))
		d := testDesk(t, "LITELLM_MASTER_KEY=sk-fake-build-master\n"+
			"PROXY_URL="+srv.URL+"\n", false)
		m := d.HTTPTally(context.Background(), "why it fell",
			objs(field(t, raw, "keys")))
		got := string(pyjson.Compact(tallyObj(m, true)))
		want := field(t, raw, "out").(string)
		if got != want {
			t.Errorf("http_tally:\n got %s\nwant %s", got, want)
		}
		srv.Close()
	}
}

func TestGoldenBuild(t *testing.T) {
	for _, raw := range golden(t, "core.json")["builds"] {
		lan := field(t, raw, "lan").(bool)
		d := testDesk(t, "LITELLM_MASTER_KEY=sk-fake-build-master\n"+
			"DEFAULT_MODELS=local,big\n", lan)
		d.rawKeys = objs(field(t, raw, "keys"))
		d.models = []string{"big", "local"}
		d.litellm = "1.106.6"
		d.db = "connected"
		d.game = map[string]bool{}
		for _, g := range field(t, raw, "game").([]any) {
			d.game[g.(string)] = true
		}
		if tl, ok := field(t, raw, "tally").(pyjson.Obj); ok {
			d.tally = tallyFrom(tl)
		}
		var notes []string
		for _, n := range field(t, raw, "notes").([]any) {
			notes = append(notes, n.(string))
		}
		d.mu.Lock()
		b := d.build(field(t, raw, "up").(bool), notes)
		d.mu.Unlock()
		got := string(pyjson.Compact(b.obj))
		want := field(t, raw, "body").(string)
		if got != want {
			g, w := parting(got, want)
			t.Errorf("%s: build differs:\n got %s\nwant %s",
				field(t, raw, "seed"), g, w)
		}
		tokens := field(t, raw, "tokens").(pyjson.Obj)
		if len(tokens) != len(d.tokens) {
			t.Errorf("tokens: %d, want %d", len(d.tokens),
				len(tokens))
		}
		for _, kv := range tokens {
			if d.tokens[kv.K] != kv.V {
				t.Errorf("token for %s: %q", kv.K,
					d.tokens[kv.K])
			}
		}
	}
}

// cut shows a long text around where it parts from another.
func cut(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func parting(a, b string) (string, string) {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := max(0, i-300)
	return cut(a[lo:], 600), cut(b[lo:], 600)
}

func tallyFrom(o pyjson.Obj) *Tally {
	t := &Tally{Day: o.Val("day"), Source: o.Val("source"),
		Why: o.Val("why")}
	for _, r := range objs(o.Val("rows")) {
		row := Row{K: text(r.Val("k")), Name: text(r.Val("name"))}
		for i, f := range Counts {
			n, _ := Number(r.Val(f))
			row.All[i] = &n
			row.Today[i], _ = Number(r.Val("t_" + f))
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}
