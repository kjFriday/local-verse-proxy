//go:build e2e

// The rest of spec 16.5's security scripts, ported: typo_vb, surr_vb,
// nest_vb, rst_vb, rst2_vb, refill_vb, game_vb (tests/reference/
// security), inputs, rawhost and sec_fix (tests/reference/perf).
// lock2 and logflood come after TestLock, in security_test.go. Each
// prints its script's lines; run it against Python, then against Go,
// and compare (gate-a.md, G-4). This file sorts before
// security_test.go, so these run before TestLock and the sweep.
//
// The floods (rst_vb, nest_vb, surr_vb) last E2E_FLOOD_SECS seconds
// each (5 by default). The cards refill_vb and game_vb mint are
// cancelled at the end, so the other desk on the same proxy can run
// the same steps.

package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/tests/e2e/e2ekit"
)

func floodSecs() time.Duration {
	f, err := strconv.ParseFloat(os.Getenv("E2E_FLOOD_SECS"), 64)
	if err != nil || f <= 0 {
		f = 5
	}
	return time.Duration(f * float64(time.Second))
}

func needLog(t *testing.T, d *e2ekit.Desk) {
	t.Helper()
	if d.Log == "" {
		t.Skip("needs the desk's log file (E2E_LOG)")
	}
}

// logLines counts the log's lines, as the scripts did.
func logLines(d *e2ekit.Desk) int {
	b, _ := os.ReadFile(d.Log)
	return bytes.Count(b, []byte("\n"))
}

// lastLog is the log's last line.
func lastLog(d *e2ekit.Desk) string {
	b, _ := os.ReadFile(d.Log)
	ls := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return ls[len(ls)-1]
}

// flood runs fn on n goroutines for secs and gives the count and the
// time it took.
func flood(n int, secs time.Duration, fn func()) (int64,
	time.Duration) {
	var cnt atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	t0 := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				fn()
				cnt.Add(1)
			}
		}()
	}
	time.Sleep(secs)
	stop.Store(true)
	wg.Wait()
	return cnt.Load(), time.Since(t0)
}

// rate prints a flood's log growth as the scripts did, and fails if
// it is above max lines a second.
func rate(t *testing.T, d *e2ekit.Desk, what string, n0 int, b0 int64,
	cnt int64, dt time.Duration, max float64) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	n1, b1 := logLines(d), d.LogSize()
	s := dt.Seconds()
	per := float64(n1-n0) / s
	say(t, "  %s, %.1f s: %d requests, %d log lines (%.0f/s), "+
		"%.0f KiB/s of log", what, s, cnt, n1-n0, per,
		float64(b1-b0)/s/1024)
	if per > max {
		t.Errorf("%s: %.0f log lines a second, at most %.0f", what,
			per, max)
	}
}

// TestTypo is typo_vb: a device that mistyped once gets in while 10
// other addresses guess.
func TestTypo(t *testing.T) {
	d := e2ekit.FromEnv(t)
	devices(t, d)
	if d.PW == "" {
		t.Skip("needs a password (E2E_PWFILE)")
	}
	var stop atomic.Bool
	var checked atomic.Int64
	var wg sync.WaitGroup
	for k := 0; k < 2; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				a, err := d.Login(fmt.Sprintf("127.0.8.%d",
					(i*2+k)%10+1), "nope-nope")
				if err == nil && a.Status == 401 {
					checked.Add(1)
				}
			}
		}(k)
	}
	time.Sleep(500 * time.Millisecond)
	say(t, "legit typo: %d", login(t, d, "127.0.8.200", "typo").st)
	t0 := time.Now()
	tries, got := 0, -1.0
	for time.Since(t0) < 120*time.Second {
		time.Sleep(time.Second)
		tries++
		if login(t, d, "127.0.8.200", d.PW).st == 200 {
			got = time.Since(t0).Seconds()
			break
		}
	}
	stop.Store(true)
	wg.Wait()
	g := "None"
	if got >= 0 {
		g = fmt.Sprintf("%.1f", got)
	}
	say(t, "attacker checked %d; typo device got in after %s s, %d "+
		"tries", checked.Load(), g, tries)
	if got < 0 {
		t.Errorf("the typo device never got in")
	}
}

// TestSurrogate is surr_vb: a password with a lone surrogate.
func TestSurrogate(t *testing.T) {
	d := e2ekit.FromEnv(t)
	devices(t, d)
	needLog(t, d)
	if d.PW == "" {
		t.Skip("needs a password (E2E_PWFILE)")
	}
	src := "127.0.10.1"
	raw := []byte(`{"password": "x\ud800"}`)
	surr := func() (int, string) {
		a, err := d.Req(src, "POST", "/api/login", nil, raw,
			map[string]string{"Content-Type": "application/json"})
		if err != nil {
			return 0, err.Error()
		}
		return a.Status, a.Err()
	}
	for i := 0; i < 7; i++ {
		st, e := surr()
		say(t, "  try %d (%d, %q)", i+1, st, e)
		time.Sleep(1050 * time.Millisecond)
	}
	say(t, "  last log line: %s", lastLog(d))
	n0, b0 := logLines(d), d.LogSize()
	cnt, dt := flood(8, floodSecs(), func() { surr() })
	rate(t, d, "tries", n0, b0, cnt, dt, 2)
	say(t, "  same device, RIGHT password now: %d",
		login(t, d, src, d.PW).st)
	say(t, "  then a real wrong one: %q", login(t, d, src, "nope").msg)
}

// TestNested is nest_vb: deep-nested JSON bodies, not logged in.
func TestNested(t *testing.T) {
	d := e2ekit.FromEnv(t)
	needLog(t, d)
	body := strings.Repeat("[", 4000) + strings.Repeat("]", 4000)
	data := []byte(fmt.Sprintf("POST /api/login HTTP/1.1\r\nHost: %s"+
		"\r\nContent-Type: application/json\r\nContent-Length: %d"+
		"\r\n\r\n%s", d.Host, len(body), body))
	one := func() string {
		c, err := d.Dial("127.0.9.1", 5*time.Second)
		if err != nil {
			return "refused"
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		c.Write(data)
		var got []byte
		b := make([]byte, 4096)
		for !bytes.Contains(got, []byte("\r\n\r\n")) {
			n, err := c.Read(b)
			got = append(got, b[:n]...)
			if err != nil {
				break
			}
		}
		e2ekit.Record(got)
		first, _, _ := strings.Cut(string(got), "\r\n")
		return first
	}
	first := one()
	say(t, "one try: %s", first)
	if !strings.Contains(first, " 400 ") {
		t.Errorf("nested body: %s", first)
	}
	time.Sleep(200 * time.Millisecond)
	say(t, "last log line: %s", cut(lastLog(d), 120))
	n0, b0 := logLines(d), d.LogSize()
	cnt, dt := flood(8, floodSecs(), func() { one() })
	rate(t, d, "one unauthenticated address, 8 conns", n0, b0, cnt, dt,
		0)
}

// rst sends data and resets the connection (RST, not FIN).
func rst(d *e2ekit.Desk, src string, data []byte, wait time.Duration,
	readFirst bool) error {
	c, err := d.Dial(src, 5*time.Second)
	if err != nil {
		return err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetLinger(0)
	}
	if len(data) > 0 {
		c.Write(data)
	}
	if readFirst {
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		b := make([]byte, 65536)
		n, _ := c.Read(b)
		e2ekit.Record(b[:n])
	}
	time.Sleep(wait)
	return c.Close()
}

// TestResets is rst_vb: a request, then a reset at once. Does the
// log grow?
func TestResets(t *testing.T) {
	d := e2ekit.FromEnv(t)
	needLog(t, d)
	src, path := "127.0.12.1", "/"
	data := []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\n"+
		"Accept-Encoding: gzip\r\n\r\n", path, d.Host))
	n0 := logLines(d)
	for i := 0; i < 20; i++ {
		rst(d, src, data, 0, false)
	}
	time.Sleep(500 * time.Millisecond)
	n1 := logLines(d)
	say(t, "20 reset requests for %s: %d new log lines", path, n1-n0)
	if n1 != n0 {
		b, _ := os.ReadFile(d.Log)
		ls := strings.Split(string(b), "\n")
		say(t, "  first new lines:")
		for _, l := range ls[n0:min(n0+14, len(ls))] {
			say(t, "   | %s", cut(l, 110))
		}
		t.Errorf("resets wrote %d log lines", n1-n0)
	}
	n2, b2 := logLines(d), d.LogSize()
	cnt, dt := flood(4, floodSecs(), func() {
		rst(d, src, data, 0, false)
	})
	time.Sleep(200 * time.Millisecond)
	rate(t, d, "one address, 4 threads", n2, b2, cnt, dt, 0)
}

// TestResets2 is rst2_vb: reset variants beyond GET /, then the
// slots: all given back?
func TestResets2(t *testing.T) {
	d := e2ekit.FromEnv(t)
	needLog(t, d)
	h := d.Host
	src := "127.0.14.1"
	post := func(path, body, extra string) []byte {
		return []byte(fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\n"+
			"Content-Type: application/json\r\nContent-Length: %d"+
			"\r\n%s\r\n%s", path, h, len(body), extra, body))
	}
	get := func(line, extra string) []byte {
		return []byte(fmt.Sprintf("%s\r\nHost: %s\r\n%s\r\n", line, h,
			extra))
	}
	var many strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&many, "X%d: y\r\n", i)
	}
	short := post("/api/login", "{}", "")
	cases := []struct {
		name string
		data []byte
	}{
		{"POST /api/login {} (400)", post("/api/login", "{}", "")},
		{"POST /api/mint unauth (401)", post("/api/mint", "{}", "")},
		{"POST bad JSON (400)", post("/api/login", "{nope", "")},
		{"POST nested 4000", post("/api/login",
			strings.Repeat("[", 4000)+strings.Repeat("]", 4000),
			"")},
		{"bad request line (400)", []byte("GARBAGE\r\n\r\n")},
		{"bad version (400)", []byte("GET / HTTP/9.x\r\n\r\n")},
		{"HTTP/2.0 (505)", get("GET / HTTP/2.0", "")},
		{"URI 70k (414)", get("GET /"+strings.Repeat("a", 70000)+
			" HTTP/1.1", "")},
		{"200 headers (431)", get("GET / HTTP/1.1", many.String())},
		{"unknown method (501)", get("BREW / HTTP/1.1", "")},
		{"wrong Host (421)", []byte("GET / HTTP/1.1\r\nHost: " +
			"evil.example\r\n\r\n")},
		{"HEAD /api/state (405)", get("HEAD /api/state HTTP/1.1", "")},
		{"GET /nope (404)", get("GET /nope HTTP/1.1", "")},
		{"GET /app.js gzip", get("GET /app.js HTTP/1.1",
			"Accept-Encoding: gzip\r\n")},
		{"GET /app.js plain", get("GET /app.js HTTP/1.1", "")},
		{"Expect 100-continue, no body", []byte(fmt.Sprintf(
			"POST /api/login HTTP/1.1\r\nHost: %s\r\n"+
				"Content-Type: application/json\r\n"+
				"Content-Length: 20\r\n"+
				"Expect: 100-continue\r\n\r\n", h))},
		{"POST body cut short", short[:len(short)-1]},
		{"pipelined x5", bytes.Repeat(get("GET / HTTP/1.1", ""), 5)},
	}
	total := 0
	for _, c := range cases {
		n0 := logLines(d)
		for i := 0; i < 10; i++ {
			rst(d, src, c.data, 0, false)
		}
		time.Sleep(300 * time.Millisecond)
		n := logLines(d) - n0
		total += n
		say(t, "  %-32s x10 reset at once: %d new log lines", c.name,
			n)
	}
	waits := []struct {
		name      string
		data      []byte
		wait      time.Duration
		readFirst bool
	}{
		{"keep-alive idle, then reset", get("GET / HTTP/1.1", ""),
			200 * time.Millisecond, true},
		{"half request line, then reset", []byte("GET / HT"),
			100 * time.Millisecond, false},
		{"half headers, then reset", []byte("GET / HTTP/1.1\r\n" +
			"Host: " + h + "\r\nX-A: b"), 100 * time.Millisecond,
			false},
		{"POST body half, then reset", []byte(fmt.Sprintf(
			"POST /api/login HTTP/1.1\r\nHost: %s\r\n"+
				"Content-Type: application/json\r\n"+
				"Content-Length: 200\r\n\r\n"+
				`{"pa`, h)), 100 * time.Millisecond, false},
	}
	for _, w := range waits {
		n0 := logLines(d)
		for i := 0; i < 10; i++ {
			rst(d, src, w.data, w.wait, w.readFirst)
		}
		time.Sleep(300 * time.Millisecond)
		n := logLines(d) - n0
		total += n
		say(t, "  %-32s x10: %d lines", w.name, n)
	}
	if total != 0 {
		t.Errorf("resets wrote %d log lines", total)
	}
	time.Sleep(500 * time.Millisecond)
	say(t, "  threads now: %d", d.Threads())
	if !d.Devices() {
		return
	}
	held := stall(d, src, 40)
	say(t, "  same address after resets: %d of 40 held (cap is 32)",
		len(held))
	for _, c := range held {
		c.Close()
	}
	if len(held) != 32 {
		t.Errorf("after resets one address holds %d, want 32",
			len(held))
	}
}

// mintSession is a logged-in device that mints and cancels.
type mintSession struct {
	d      *e2ekit.Desk
	src    string
	h      map[string]string
	cookie string
	made   []string
}

func newMintSession(t *testing.T, d *e2ekit.Desk,
	src string) *mintSession {
	ck, csrf := d.Session(t, src)
	h := map[string]string{"X-CSRF-Token": csrf,
		"Origin": "http://" + d.Host}
	if ck != "" {
		h["Cookie"] = ck
	}
	m := &mintSession{d: d, src: src, h: h, cookie: ck}
	t.Cleanup(func() { m.cancelAll(t) })
	return m
}

func (m *mintSession) post(t *testing.T, path string,
	raw []byte) *e2ekit.Answer {
	h := map[string]string{"Content-Type": "application/json"}
	for k, v := range m.h {
		h[k] = v
	}
	return req(t, m.d, m.src, "POST", path, nil, raw, h)
}

func (m *mintSession) mint(t *testing.T, raw []byte) *e2ekit.Answer {
	a := m.post(t, "/api/mint", raw)
	if s, _ := a.JSON["seat"].(string); a.Status == 200 && s != "" {
		m.made = append(m.made, s)
	}
	return a
}

func (m *mintSession) state(t *testing.T) map[string]any {
	h := map[string]string{}
	if m.cookie != "" {
		h["Cookie"] = m.cookie
	}
	return req(t, m.d, m.src, "GET", "/api/state", nil, nil, h).JSON
}

// cancelAll cancels the cards this session made, so the other desk
// on the same proxy finds the names free.
func (m *mintSession) cancelAll(t *testing.T) {
	for _, s := range m.made {
		b, _ := json.Marshal(map[string]string{"seat": s})
		a := m.post(t, "/api/revoke", b)
		t.Logf("cleanup: cancel %s: %d", s, a.Status)
	}
	m.made = nil
}

func get(j map[string]any, k string) any {
	if j == nil {
		return nil
	}
	return j[k]
}

// pyrepr shows a value the way the scripts' f-strings showed it, for
// the values that come up: strings, None, numbers, lists.
func pyrepr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return "'" + strings.ReplaceAll(x, "'", `\'`) + "'"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		var p []string
		for _, e := range x {
			p = append(p, pyrepr(e))
		}
		return "[" + strings.Join(p, ", ") + "]"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// TestRefill is refill_vb: the refill field, checked on the server.
func TestRefill(t *testing.T) {
	d := e2ekit.FromEnv(t)
	if d.PW == "" {
		t.Skip("needs a password (E2E_PWFILE)")
	}
	m := newMintSession(t, d, "127.0.0.20")
	s := m.state(t)
	say(t, "models: %s defaults: %s", pyrepr(s["models"]),
		cut(fmt.Sprint(s["defaults"]), 200))
	models, _ := s["models"].([]any)
	if len(models) == 0 {
		t.Fatal("no models")
	}
	model, _ := json.Marshal(models[0])
	body := func(seat, extra string) []byte {
		return []byte(fmt.Sprintf(`{"models": [%s], "budget": "1", `+
			`"duration": "30d", "seat": %q%s}`, model, seat, extra))
	}
	bad := []string{`"2d"`, `"1m"`, `"1h"`, `"30m"`, `"1D"`, `" 1d"`,
		`"1d "`, `"1mo "`, `"1d\n"`, `"1d;id"`, `"--models"`,
		`"1d --models all"`, `"١d"`, `"1 d"`, `5`, `1`, `0`,
		`null`, `true`, `false`, `["1d"]`, `{"v": "1d"}`, `1.0`}
	for n, v := range bad {
		a := m.mint(t, body(fmt.Sprintf("vb-bad-%d", n+1),
			`, "refill": `+v))
		if a.Status == 200 {
			say(t, "  refill=%s -> 200 (MINTED!)", v)
			t.Errorf("refill %s minted", v)
			continue
		}
		say(t, "  refill=%-22s -> %d field=%v %s", v, a.Status,
			get(a.JSON, "field"), pyrepr(cut(a.Err(), 40)))
	}
	any := false
	for _, c := range cards(m.state(t)) {
		if strings.HasPrefix(str(c["seat"]), "vb-bad-") {
			any = true
		}
	}
	say(t, "  any vb-bad-* card made? %v", pyBool(any))
	for _, g := range []struct{ seat, extra string }{
		{"vb-r-none", ""}, {"vb-r-empty", `, "refill": ""`},
		{"vb-r-1d", `, "refill": "1d"`},
		{"vb-r-1w", `, "refill": "1w"`},
		{"vb-r-1mo", `, "refill": "1mo"`}} {
		a := m.mint(t, body(g.seat, g.extra))
		say(t, "  %s: %d refill echo=%s key=%s", g.seat, a.Status,
			pyrepr(get(a.JSON, "refill")),
			e2ekit.Mask(str(get(a.JSON, "key"))))
		if a.Status != 200 {
			t.Errorf("%s: %d %s", g.seat, a.Status, a.Err())
		}
	}
	for _, x := range [][2]string{{"vb-d-1s", "1s"}, {"vb-d-5m", "5m"},
		{"vb-d-1mo", "1mo"}, {"vb-d-9999mo", "9999mo"}} {
		raw := []byte(fmt.Sprintf(`{"models": [%s], "budget": "1", `+
			`"duration": %q, "seat": %q}`, model, x[1], x[0]))
		a := m.mint(t, raw)
		say(t, "  duration %s: %d %s", x[1], a.Status,
			pyrepr(cut(a.Err(), 70)))
	}
	// refill_vb waited 1.5 s. Both desks rebuild the state every
	// 2 s, so whether vb-d-1s showed as expired was chance: 3.5 s.
	time.Sleep(3500 * time.Millisecond)
	for _, c := range cards(m.state(t)) {
		if strings.HasPrefix(str(c["seat"]), "vb-") {
			say(t, "  card %-12s state=%-8s refills=%-6s "+
				"resets=%v expires=%v", str(c["seat"]),
				str(c["state"]), pyrepr(c["refills"]),
				pyNone(c["resets"]),
				pyNone(c["expires"]))
		}
	}
}

func cards(s map[string]any) []map[string]any {
	var out []map[string]any
	l, _ := s["cards"].([]any)
	for _, c := range l {
		if m, ok := c.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func str(v any) string { s, _ := v.(string); return s }

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

func pyNone(v any) any {
	if v == nil {
		return "None"
	}
	return v
}

// TestGameNames is game_vb: seats that look like the game's.
func TestGameNames(t *testing.T) {
	d := e2ekit.FromEnv(t)
	if d.PW == "" {
		t.Skip("needs a password (E2E_PWFILE)")
	}
	m := newMintSession(t, d, "127.0.0.21")
	for _, seat := range []string{"game-x", "GAME-x", "Game-Fern",
		"game-", "gamex", "my-game"} {
		raw, _ := json.Marshal(map[string]any{"seat": seat,
			"models": []string{"local"}, "budget": "1",
			"duration": "1d"})
		a := m.mint(t, raw)
		e := "ok"
		if _, has := a.JSON["error"]; has {
			e = a.Err()
		}
		say(t, "  %-10s -> %d %s %s", seat, a.Status,
			pyNone(get(a.JSON, "field")), pyrepr(cut(e, 70)))
		game := strings.HasPrefix(strings.ToLower(seat), "game-")
		if game && a.Status == 200 {
			t.Errorf("%s: a game seat was minted", seat)
		}
	}
}

// TestInputs is inputs.py: the server's checks on /api/mint and
// /api/revoke. Keys are never printed. inputs.py ran against a local
// desk; with a password the session's cookie goes along.
func TestInputs(t *testing.T) {
	d := e2ekit.FromEnv(t)
	m := newMintSession(t, d, "127.0.0.22")
	H := map[string]string{"Content-Type": "application/json",
		"X-CSRF-Token": m.h["X-CSRF-Token"]}
	if m.cookie != "" {
		H["Cookie"] = m.cookie
		H["Origin"] = "http://" + d.Host
	}
	with := func(kv ...string) map[string]string {
		h := map[string]string{}
		for k, v := range H {
			h[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			h[kv[i]] = kv[i+1]
		}
		return h
	}
	ok := map[string]any{"seat": "probe", "models": []string{"local"},
		"budget": "1", "duration": "1d"}
	patch := func(k string, v any) []byte {
		b := map[string]any{}
		for x, y := range ok {
			b[x] = y
		}
		b[k] = v
		raw, _ := json.Marshal(b)
		return raw
	}
	cases := []struct {
		name string
		k    string
		v    any
	}{
		{"seat 41 chars", "seat", strings.Repeat("a", 41)},
		{"seat leading dash", "seat", "-x"},
		{"seat space", "seat", "a b"}, {"seat unicode", "seat", "sé"},
		{"seat newline", "seat", "a\nb"},
		{"seat list", "seat", []string{"a"}},
		{"seat null", "seat", nil},
		{"models empty", "models", []string{}},
		{"models unknown", "models", []string{"gpt-x"}},
		{"models str", "models", "local"},
		{"models comma", "models", []string{"local,claude"}},
		{"budget 0", "budget", "0"}, {"budget -1", "budget", "-1"},
		{"budget 1e3", "budget", "1e3"},
		{"budget 1.234", "budget", "1.234"},
		{"budget 1234567", "budget", "1234567"},
		{"budget num 2.5", "budget", 2.5},
		{"budget nan", "budget", "nan"},
		{"budget ' 5 '", "budget", " 5 "},
		{"duration 0d", "duration", "0d"},
		{"duration 30m", "duration", "30m"},
		{"duration 1y", "duration", "1y"},
		{"duration 99999d", "duration", "99999d"},
		{"duration --budget", "duration", "--budget"},
	}
	for _, c := range cases {
		a := req(t, d, m.src, "POST", "/api/mint", nil,
			patch(c.k, c.v), H)
		_, leaked := a.JSON["key"]
		kr := ""
		if leaked {
			// As inputs.py: cancel it at once. Some of its
			// cases are taken since (a budget as a number, " 5 ",
			// 30m): the two desks are compared, not judged.
			kr = "KEY RETURNED"
			m.made = append(m.made, str(a.JSON["seat"]))
			m.cancelAll(t)
		}
		say(t, "mint %-20s -> %d field=%v %s %s", c.name, a.Status,
			pyNone(get(a.JSON, "field")), kr, cut(a.Err(), 60))
	}
	okRaw, _ := json.Marshal(ok)
	pad := patch("pad", strings.Repeat("x", 9000))
	transport := []struct {
		name string
		h    map[string]string
		raw  []byte
		cl   string
	}{
		{"no csrf", map[string]string{"Content-Type": "application/" +
			"json", "Cookie": H["Cookie"]}, okRaw, ""},
		{"wrong csrf", with("X-CSRF-Token", strings.Repeat("x", 32)),
			okRaw, ""},
		{"text/plain", with("Content-Type", "text/plain"), okRaw, ""},
		{"origin evil", with("Origin", "http://evil.test"), okRaw, ""},
		{"origin null", with("Origin", "null"), okRaw, ""},
		{"big body", H, pad, ""},
		{"bad json", H, []byte("{nope"), ""},
		{"json list", H, []byte("[1,2]"), ""},
		{"bad length", H, nil, "abc"},
		{"neg length", H, nil, "-5"},
	}
	for _, c := range transport {
		if c.h["Cookie"] == "" {
			delete(c.h, "Cookie")
		}
		cl := c.cl
		if cl == "" {
			cl = fmt.Sprint(len(c.raw))
		}
		var b bytes.Buffer
		fmt.Fprintf(&b, "POST /api/mint HTTP/1.1\r\nHost: %s\r\n",
			d.Host)
		for k, v := range c.h {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
		fmt.Fprintf(&b, "Content-Length: %s\r\nConnection: close"+
			"\r\n\r\n", cl)
		b.Write(c.raw)
		line, all := d.Raw(m.src, b.Bytes(), 12*time.Second)
		_, body, _ := bytes.Cut(all, []byte("\r\n\r\n"))
		say(t, "post %-12s -> %s %q", c.name, status(line),
			cut(string(body), 70))
		if !regexp.MustCompile(` [45]\d\d `).MatchString(line + " ") {
			t.Errorf("%s: %s", c.name, line)
		}
	}
	for _, body := range []string{`{"seat": "nobody"}`,
		`{"id": "0000000000000000"}`, `{"seat": ["seat000"]}`,
		`{"seat": "--revoke"}`, `{}`} {
		a := req(t, d, m.src, "POST", "/api/revoke", nil,
			[]byte(body), H)
		say(t, "revoke %-24s -> %d %q", body, a.Status,
			cut(string(a.Body), 70))
	}
}

// status is the code of a status line ("HTTP/1.1 400 Bad Request").
func status(line string) string {
	f := strings.Fields(line)
	if len(f) > 1 {
		return f[1]
	}
	return line
}

// TestRawHost is rawhost.py: odd Host headers, sent raw from
// 127.0.0.1.
func TestRawHost(t *testing.T) {
	d := e2ekit.FromEnv(t)
	p := d.Port
	ip := "192.0.2.2"
	type c struct{ name, data string }
	g := "GET /api/session HTTP/1.1\r\n"
	cases := []c{
		{"empty Host 1.1", g + "Host: \r\n\r\n"},
		{"no Host 1.0", "GET /api/session HTTP/1.0\r\n\r\n"},
		{"no Host 1.1", g + "\r\n"},
		{"two Hosts (ip, evil)", fmt.Sprintf(g+"Host: %s:%d\r\n"+
			"Host: evil.test:%d\r\n\r\n", ip, p, p)},
		{"two Hosts (evil, ip)", fmt.Sprintf(g+"Host: evil.test:%d"+
			"\r\nHost: %s:%d\r\n\r\n", p, ip, p)},
		{"trailing dot vm.", fmt.Sprintf(g+"Host: vm.:%d\r\n\r\n", p)},
		{"upper VM", fmt.Sprintf(g+"Host: VM:%d\r\n\r\n", p)},
		{"ip with userinfo", fmt.Sprintf(g+"Host: evil.test@%s:%d"+
			"\r\n\r\n", ip, p)},
		{"absolute URI evil", fmt.Sprintf("GET http://evil.test:%d/"+
			"api/session HTTP/1.1\r\nHost: %s:%d\r\n\r\n", p, ip,
			p)},
		{"ipv4 decimal 3221225986", fmt.Sprintf(g+"Host: 3221225986:"+
			"%d\r\n\r\n", p)},
		{"0.0.0.0", fmt.Sprintf(g+"Host: 0.0.0.0:%d\r\n\r\n", p)},
		{fmt.Sprintf("port 0%d", p), fmt.Sprintf(g+"Host: %s:0%d"+
			"\r\n\r\n", ip, p)},
		{"space in host", fmt.Sprintf(g+"Host: %s :%d\r\n\r\n", ip,
			p)},
	}
	for _, k := range cases {
		c, err := d.Dial("127.0.0.1", 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte(k.data))
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		b := make([]byte, 4096)
		n, err := c.Read(b)
		data := b[:n]
		if ne, ok := err.(net.Error); ok && ne.Timeout() && n == 0 {
			data = []byte("(timeout)")
		}
		c.Close()
		e2ekit.Record(data)
		line, _, _ := bytes.Cut(data, []byte("\r\n"))
		parts := bytes.SplitN(data, []byte("\r\n\r\n"), 2)
		body := parts[len(parts)-1]
		say(t, "%-26s -> %s %q", k.name, line, cut(string(body), 70))
		// Two Hosts: Python read the first, so (ip, evil) was
		// served there; a browser sends one.
		if strings.Contains(k.name, "evil") &&
			!strings.HasPrefix(k.name, "two Hosts") {
			if bytes.Contains(line, []byte(" 200 ")) {
				t.Errorf("%s served: %s", k.name, line)
			}
		}
	}
}

// TestSecFix is sec_fix.py: Host checks, tries left, a session bound
// to its device, the server's own errors, the password kept from
// child processes, the thread cap and the read timeout. Its last part
// (mint-key's error text, scrubbed) is TestMintErrorScrubbed.
func TestSecFix(t *testing.T) {
	d := e2ekit.FromEnv(t)
	devices(t, d)
	if d.PW == "" {
		t.Skip("needs a password (E2E_PWFILE)")
	}
	p, ip := d.Port, "192.0.2.2"
	r := func(method, path, host string, body any, h map[string]string,
		src string) *e2ekit.Answer {
		if host == "" {
			host = fmt.Sprintf("127.0.0.1:%d", p)
		}
		dd := *d
		dd.Host = host
		return req(t, &dd, src, method, path, body, nil, h)
	}
	say(t, "== --lan Host check (DNS rebinding)")
	me, _ := os.Hostname()
	for _, host := range []string{fmt.Sprintf("%s:%d", ip, p),
		fmt.Sprintf("127.0.0.1:%d", p), fmt.Sprintf("localhost:%d", p),
		fmt.Sprintf("%s:%d", me, p), fmt.Sprintf("%s.local:%d", me, p),
		fmt.Sprintf("rebind.test:%d", p),
		fmt.Sprintf("evil.example:%d", p), ip + ":9999", ip,
		fmt.Sprintf("%s.nip.io:%d", ip, p), fmt.Sprintf("[::1]:%d", p),
		""} {
		var a *e2ekit.Answer
		if host == "" {
			line, _ := d.Raw("127.0.0.1", []byte(
				"GET /api/session HTTP/1.1\r\nHost: \r\n"+
					"Connection: close\r\n\r\n"),
				5*time.Second)
			say(t, "  GET /api/session Host %-28s -> %s", "''",
				status(line))
			continue
		}
		a = r("GET", "/api/session", host, nil, nil, "127.0.0.1")
		name := host
		if host == me+":"+fmt.Sprint(p) ||
			host == me+".local:"+fmt.Sprint(p) {
			name = strings.Replace(host, me, "<hostname>", 1)
		}
		say(t, "  GET /api/session Host %-28s -> %d", pyrepr(name),
			a.Status)
	}
	a := r("POST", "/api/login", fmt.Sprintf("rebind.test:%d", p),
		map[string]string{"password": "wrong-wrong-wrong"},
		map[string]string{"Origin": fmt.Sprintf("http://rebind.test:%d",
			p)}, "127.0.0.1")
	say(t, "  POST /api/login via rebind.test -> %d (never reaches "+
		"the password check, so it cannot lock anyone out)", a.Status)
	if a.Status != 421 {
		t.Errorf("login via rebind.test: %d", a.Status)
	}

	say(t, "== login: tries left, then the lock")
	for i := 0; i < 2; i++ {
		a := r("POST", "/api/login", "", map[string]string{
			"password": strings.Repeat("nope", 4)}, nil,
			"127.0.0.5")
		say(t, "  wrong try %d: %d %s", i+1, a.Status, a.Err())
		time.Sleep(1100 * time.Millisecond)
	}

	say(t, "== session bound to the address that logged in")
	a = r("POST", "/api/login", "", map[string]string{"password": d.PW},
		nil, "127.0.0.1")
	sc := a.Header.Get("Set-Cookie")
	name, _, _ := strings.Cut(sc, "=")
	cookie, flags, _ := strings.Cut(sc, ";")
	var fl []string
	for _, f := range strings.Split(flags, ";") {
		fl = append(fl, strings.TrimSpace(f))
	}
	say(t, "  login %d, cookie name %s, flags: %s", a.Status, name,
		strings.Join(fl, "; "))
	for _, src := range []string{"127.0.0.1", "127.0.0.3"} {
		h := map[string]string{"Cookie": cookie}
		a := r("GET", "/api/state", "", nil, h, src)
		b := r("GET", "/api/session", "", nil, h, src)
		given := "null"
		if str(b.JSON["csrf"]) != "" {
			given = "given"
		}
		say(t, "  from %s: /api/state %d, /api/session csrf %s", src,
			a.Status, given)
		if src != "127.0.0.1" && (a.Status != 401 || given != "null") {
			t.Errorf("cookie from another device: %d %s", a.Status,
				given)
		}
	}
	_, v, _ := strings.Cut(cookie, "=")
	a = r("GET", "/api/state", "", nil, map[string]string{
		"Cookie": "fd=" + v}, "127.0.0.1")
	say(t, "  old cookie name 'fd' -> %d", a.Status)

	say(t, "== http.server's own errors, HEAD")
	host := fmt.Sprintf("%s:%d", ip, p)
	a = r("HEAD", "/", host, nil, nil, "127.0.0.1")
	say(t, "  HEAD / -> %d, body %d B, CSP %s, length header %s",
		a.Status, len(a.Body), yes(a.Header.Get(
			"Content-Security-Policy")), pyNone(nz(a.Header.Get(
			"Content-Length"))))
	a = r("HEAD", "/api/state", host, nil, nil, "127.0.0.1")
	say(t, "  HEAD /api/state -> %d", a.Status)
	a = r("PUT", "/", host, nil, nil, "127.0.0.1")
	say(t, "  PUT / -> %d %s %q XFO %s nosniff %s CSP %s", a.Status,
		a.Header.Get("Content-Type"), a.Body,
		a.Header.Get("X-Frame-Options"),
		a.Header.Get("X-Content-Type-Options"),
		yes(a.Header.Get("Content-Security-Policy")))
	line, all := d.Raw("127.0.0.1", []byte("GET / HTTP/<script>"+
		"alert(1)</script>\r\n\r\n"), 5*time.Second)
	head, body, _ := bytes.Cut(all, []byte("\r\n\r\n"))
	csp := "no CSP"
	if bytes.Contains(head, []byte("Content-Security-Policy")) {
		csp = "CSP"
	}
	say(t, "  bad request line -> %q %q %s", line, cut(string(body),
		60), csp)
	if bytes.Contains(all, []byte("<script>")) {
		t.Errorf("the bad request line came back as it was")
	}

	say(t, "== FRONTDESK_PASSWORD not in any child's environment")
	// A viewer wakes the checks, and with them the desk's psql.
	r("GET", "/api/state", "", nil, map[string]string{"Cookie": cookie},
		"127.0.0.1")
	time.Sleep(2 * time.Second)
	if d.PID == 0 {
		say(t, "  (no E2E_PID: skipped)")
	} else {
		kids, _ := exec.Command("pgrep", "-P",
			fmt.Sprint(d.PID)).Output()
		for _, k := range strings.Fields(string(kids)) {
			env, _ := os.ReadFile("/proc/" + k + "/environ")
			hits := 0
			for _, e := range bytes.Split(env, []byte{0}) {
				if bytes.HasPrefix(e, []byte(
					"FRONTDESK_PASSWORD=")) {
					hits++
				}
			}
			cmd, _ := os.ReadFile("/proc/" + k + "/cmdline")
			c := bytes.Split(cmd, []byte{0})
			c = c[:min(3, len(c))]
			cs := string(bytes.Join(c, []byte(" ")))
			say(t, "  child %s: %d entries", cut(cs, 40), hits)
			if hits > 0 {
				t.Errorf("child %s has the password", k)
			}
		}
		env, _ := os.ReadFile(fmt.Sprintf("/proc/%d/environ", d.PID))
		say(t, "  Front Desk's own /proc environ (as started): %s",
			pyBool(bytes.Contains(env, []byte(
				"FRONTDESK_PASSWORD="))))
	}

	say(t, "== thread cap and read timeout")
	say(t, "  baseline: %d threads, RSS %s", d.Threads(), rss(d))
	var socks []net.Conn
	for i := 0; i < 400; i++ {
		c, err := d.Dial("127.0.0.1", 5*time.Second)
		if err != nil {
			say(t, "  connect failed: %v", err)
			break
		}
		if i%2 == 1 {
			fmt.Fprintf(c, "POST /api/login HTTP/1.1\r\n"+
				"Host: %s\r\nContent-Type: application/json"+
				"\r\nContent-Length: 8000\r\n\r\n{", host)
		} else {
			c.Write([]byte("GET / HTTP/1.1\r\n"))
		}
		socks = append(socks, c)
	}
	time.Sleep(time.Second)
	closed := 0
	for _, c := range socks {
		c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		n, err := c.Read(make([]byte, 1))
		if n == 0 && err != nil {
			if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				closed++
			}
		}
	}
	say(t, "  400 stalled connections: %d threads, RSS %s, %d closed "+
		"at once by the cap", d.Threads(), rss(d), closed)
	dd := *d
	dd.Host = host
	pg := dd.Page("127.0.0.1")
	say(t, "  a page load now: %s", pg)
	if strings.HasPrefix(pg, "200") {
		t.Errorf("a 33rd connection from one device was served")
	}
	time.Sleep(11 * time.Second)
	say(t, "  11 s later (read timeout 10 s): %d threads", d.Threads())
	pg = dd.Page("127.0.0.1")
	say(t, "  a page load: %s", pg)
	if !strings.HasPrefix(pg, "200") {
		t.Errorf("after the read timeout: %s", pg)
	}
	for _, c := range socks {
		c.Close()
	}
}

func yes(s string) string {
	if s != "" {
		return "yes"
	}
	return "no"
}

func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func rss(d *e2ekit.Desk) string {
	if d.PID == 0 {
		return "None"
	}
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", d.PID))
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS") {
			f := strings.Fields(l)
			if len(f) > 1 {
				return f[1] + " kB"
			}
		}
	}
	return "None"
}

var secretRE = regexp.MustCompile(`sk-|[0-9a-fA-F]{32,}`)

// leakyAnswer is LiteLLM's answer for a key it does not know, as
// fakelitellm's tokenNotFound has it: the key's end, then its hash.
const leakyAnswer = `{"error": {"message": "Authentication Error, ` +
	`Invalid proxy server token passed. Received API Key = sk-...%s, ` +
	`Key Hash (Token) =%s. Unable to find token in cache or ` +
	"`LiteLLM_VerificationTokenTable`" + `", "type": ` +
	`"token_not_found_in_db", "param": "key", "code": "401"}}`

// TestMintErrorScrubbed is sec_fix.py's last part, from outside. It
// ran mint-key.sh with a wrong master key and gave its stderr to
// Python's plain_error. Here a leaky stand-in for the proxy listens
// on E2E_LEAKY_ADDR and forwards every request to E2E_PROXY_URL but
// /key/generate, which it answers as LiteLLM answers a key it does
// not know: a key end (sk-...) and a 64-hex hash. The desk is started
// with PROXY_URL pointing at it (a copy of seats.env); the mint's
// error text must reach neither the page nor the desk's log.
func TestMintErrorScrubbed(t *testing.T) {
	d := e2ekit.FromEnv(t)
	addr, real := os.Getenv("E2E_LEAKY_ADDR"),
		os.Getenv("E2E_PROXY_URL")
	if addr == "" || real == "" {
		t.Skip("needs E2E_LEAKY_ADDR and E2E_PROXY_URL, and a desk " +
			"whose PROXY_URL is http://E2E_LEAKY_ADDR")
	}
	up, err := url.Parse(real)
	if err != nil {
		t.Fatal(err)
	}
	end := fmt.Sprintf("%04x", time.Now().UnixNano()&0xffff)
	hash := strings.Repeat(end, 16)
	fwd := httputil.NewSingleHostReverseProxy(up)
	fwd.Transport = &http.Transport{Proxy: nil}
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/key/generate" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			fmt.Fprintf(w, leakyAnswer, end, hash)
			return
		}
		fwd.ServeHTTP(w, r)
	})}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()
	m := newMintSession(t, d, "127.0.0.23")
	// The desk had no proxy until now: wait for its model list.
	var models []any
	for i := 0; i < 30 && len(models) == 0; i++ {
		models, _ = m.state(t)["models"].([]any)
		if len(models) == 0 {
			time.Sleep(time.Second)
		}
	}
	say(t, "  models through the leaky stand-in: %s", pyrepr(models))
	before := d.LogSize()
	a := m.mint(t, []byte(`{"seat": "errprobe", "models": ["local"], `+
		`"budget": "1", "duration": "1d"}`))
	time.Sleep(200 * time.Millisecond)
	leak := func(b string) bool {
		return strings.Contains(b, end) || strings.Contains(b, hash) ||
			secretRE.MatchString(b)
	}
	say(t, "  mint, the proxy's answer naming a key -> %d", a.Status)
	say(t, "  page gets: %s", pyrepr(a.Err()))
	say(t, "  page text has the key end, the hash, sk- or hex: %s",
		pyBool(leak(string(a.Body))))
	if leak(string(a.Body)) || a.Status == 200 {
		t.Errorf("the page got %d %q", a.Status, a.Body)
	}
	if before >= 0 {
		lg := d.LogSince(before)
		for _, l := range strings.Split(strings.TrimSpace(lg), "\n") {
			l = strings.ReplaceAll(l, end, "<END>")
			say(t, "  terminal log line: %s", cut(secretRE.
				ReplaceAllString(l, "<SECRET>"), 120))
		}
		if leak(lg) {
			t.Errorf("the log has the key end, the hash or key-" +
				"like text")
		}
	}
}
