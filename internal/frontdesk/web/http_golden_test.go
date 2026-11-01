package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/config"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/desk"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/gate"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
	"github.com/kjFriday/local-verse-proxy/internal/pgread"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

const goldenPW = "right-password-123"

type httpGolden struct {
	Fixture []struct {
		Readiness json.RawMessage
		Models    json.RawMessage
		Keys      []json.RawMessage
		Activity  json.RawMessage
		Rules     string
	}
	Mints    []map[string][3]any
	Hostname []string
	Hosts    []struct {
		Mode, Host string
		OK         bool
	}
	Names []struct {
		Mode    string
		SeatURL *string `json:"seat_url"`
		LANIP   *string `json:"lan_ip"`
		Host    string
		Name    string
		Network bool
		URL     string
	}
	Plain []struct {
		Docker    bool
		Text, Msg string
		Log       []string
	}
	Local []stepCase
	Lan   []stepCase
}

type step struct {
	Name    string
	Method  string
	Path    string
	Raw     *string
	Host    *string
	Headers [][2]string
	Cookie  *string
	Body    *string
	Ctype   *string `json:"ctype"`
	CSRF    *string `json:"csrf"`
	Length  *string
	Src     string
	Save    string
	Sleep   float64
	Proxy   string
	// has tells which of the nullable fields the step set.
	has map[string]bool
}

type stepCase struct {
	Step step
	Want record
}

type record struct {
	Status   int
	Headers  map[string]string
	Body     string
	JSON     bool
	Log      []string
	MintArgs [][]string `json:"mint_args"`
}

func loadHTTPGolden(t *testing.T) *httpGolden {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden",
		"http.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g httpGolden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	// Which keys each step sets: "csrf": null differs from no csrf.
	var raw struct {
		Local, Lan []struct{ Step map[string]any }
	}
	json.Unmarshal(b, &raw)
	for i := range g.Local {
		g.Local[i].Step.has = keys(raw.Local[i].Step)
	}
	for i := range g.Lan {
		g.Lan[i].Step.has = keys(raw.Lan[i].Step)
	}
	return &g
}

func keys(m map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func testSettings(t *testing.T, text string) *config.Live {
	t.Helper()
	p := filepath.Join(t.TempDir(), "seats.env")
	os.WriteFile(p, []byte(text), 0o600)
	s, err := config.LoadFile(p, func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	return config.NewLive(s, config.NewLog(io.Discard), nil)
}

func TestGoldenHostOK(t *testing.T) {
	g := loadHTTPGolden(t)
	apps := map[string]*App{}
	for _, mode := range []string{"local", "lan", "docker"} {
		a, err := New(Options{LAN: mode == "lan",
			Docker: mode == "docker", Port: 8040, PublicPort: 8040,
			Hosts: []string{"Named"}, Hostname: g.Hostname[0]})
		if err != nil {
			t.Fatal(err)
		}
		apps[mode] = a
	}
	if len(g.Hosts) < 100 {
		t.Fatalf("%d host cases", len(g.Hosts))
	}
	for _, c := range g.Hosts {
		if got := apps[c.Mode].HostOK(c.Host); got != c.OK {
			t.Errorf("%s %q: %v", c.Mode, c.Host, got)
		}
	}
}

func TestGoldenNames(t *testing.T) {
	g := loadHTTPGolden(t)
	live := testSettings(t, "LITELLM_MASTER_KEY=sk-fake-settings-"+
		"master\nPROXY_URL=http://localhost:4100\n")
	for _, c := range g.Names {
		ip := ""
		if c.LANIP != nil {
			ip = *c.LANIP
		}
		url := ""
		if c.SeatURL != nil {
			url = *c.SeatURL
		}
		a, _ := New(Options{LAN: c.Mode == "lan",
			Docker: c.Mode == "docker", Port: 8040,
			PublicPort: 8040,
			SeatURL:    url, Live: live,
			LANIP: func() string { return ip }})
		if n := HostName(c.Host); n != c.Name {
			t.Errorf("host_name(%q) = %q, want %q", c.Host, n,
				c.Name)
		}
		if n := OverNetwork(c.Host); n != c.Network {
			t.Errorf("over_network(%q) = %v", c.Host, n)
		}
		if u := a.SeatURL(c.Host); u != c.URL {
			t.Errorf("%s ip=%q url=%q: seat_url(%q) = %q, want %q",
				c.Mode, ip, url, c.Host, u, c.URL)
		}
	}
}

// logLines is a Log's lines without their times.
type logLines struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logLines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logLines) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []string{}
	for _, line := range strings.Split(l.b.String(), "\n") {
		if len(line) > 9 {
			out = append(out, line[9:])
		}
	}
	l.b.Reset()
	return out
}

func TestGoldenPlain(t *testing.T) {
	g := loadHTTPGolden(t)
	var logs logLines
	for _, docker := range []bool{false, true} {
		p := filepath.Join(t.TempDir(), "seats.env")
		os.WriteFile(p, []byte("LITELLM_MASTER_KEY=sk-fake-settings-"+
			"master\nPROXY_URL=http://localhost:4100\n"), 0o600)
		s, _ := config.LoadFile(p, func(string) (string, bool) {
			return "", false
		})
		log := config.NewLog(&logs)
		a, _ := New(Options{Docker: docker,
			Live: config.NewLive(s, log, nil), Log: log})
		for _, c := range g.Plain {
			if c.Docker != docker {
				continue
			}
			logs.take()
			if got := a.Plain(c.Text); got != c.Msg {
				t.Errorf("plain(%q): %q, want %q", c.Text, got,
					c.Msg)
			}
			if got := logs.take(); strings.Join(got, "\n") !=
				strings.Join(c.Log, "\n") {
				t.Errorf("plain(%q) logged %q, want %q", c.Text,
					got,
					c.Log)
			}
		}
	}
}

// fixture is the golden's small proxy.
type fixture struct {
	srv  *httptest.Server
	mu   sync.Mutex
	down bool
}

func startFixture(t *testing.T, g *httpGolden) *fixture {
	f := &fixture{}
	fx := g.Fixture[0]
	f.srv = httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request) {
		answer := func(code int, body []byte) {
			w.Header().Set("Content-Length",
				strconv.Itoa(len(body)))
			w.WriteHeader(code)
			w.Write(body)
		}
		switch r.URL.Path {
		case "/health/liveliness":
			f.mu.Lock()
			down := f.down
			f.mu.Unlock()
			if down {
				answer(503, []byte("I'm alive!"))
			} else {
				answer(200, []byte("I'm alive!"))
			}
		case "/health/readiness/details":
			answer(200, fx.Readiness)
		case "/v1/models":
			answer(200, fx.Models)
		case "/key/list":
			var ks []json.RawMessage
			alias, filter := r.URL.Query()["key_alias"]
			for _, k := range fx.Keys {
				var x struct {
					Alias *string `json:"key_alias"`
				}
				json.Unmarshal(k, &x)
				if !filter || x.Alias != nil &&
					*x.Alias == alias[0] {
					ks = append(ks, k)
				}
			}
			if ks == nil {
				ks = []json.RawMessage{}
			}
			b, _ := json.Marshal(map[string]any{"keys": ks,
				"total_pages": 1})
			answer(200, b)
		case "/user/daily/activity":
			answer(200, fx.Activity)
		default:
			answer(404, []byte(`{"detail": "no"}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type noDB struct{}

func (noDB) Query(context.Context, string) (json.RawMessage, error) {
	return nil, &pgread.Error{Msg: "no database in this test"}
}
func (noDB) Stop() {}

type goServer struct {
	port, fixport int
	fix           *fixture
	logs          *logLines
	mu            sync.Mutex
	calls         [][]string
	desk          *desk.Desk
	app           *App
	listener      net.Listener
}

// serverOpts are what the unit tests change about startGo's server.
type serverOpts struct {
	lan   bool
	login bool // a password without --lan
	clock clock.Clock
	mint  MintFunc
}

func startGo(t *testing.T, g *httpGolden, lan bool) *goServer {
	return startGoWith(t, g, serverOpts{lan: lan})
}

func startGoWith(t *testing.T, g *httpGolden, o serverOpts) *goServer {
	t.Helper()
	lan := o.lan
	fix := startFixture(t, g)
	fixport := fix.srv.Listener.Addr().(*net.TCPAddr).Port
	live := testSettings(t, fmt.Sprintf("LITELLM_MASTER_KEY=sk-fake-"+
		"settings-master\nPROXY_URL=http://127.0.0.1:%d\n"+
		"DEFAULT_MODELS=local\n", fixport))
	logs := &logLines{}
	log := config.NewLog(logs)
	game := t.TempDir()
	os.WriteFile(filepath.Join(game, "rules.toml"),
		[]byte(g.Fixture[0].Rules), 0o644)
	gs := &goServer{fixport: fixport, fix: fix, logs: logs}
	live = config.NewLive(live.Get(), log, nil)
	d := desk.New(desk.Options{Live: live, Log: log, Tally: noDB{},
		LAN: lan, Game: game})
	var secret []byte
	for i := 0; i < 16; i++ {
		secret = append(secret, byte(i))
	}
	d.SetSecret(secret)
	gs.desk = d
	bind, per := "127.0.0.1", 0
	if lan {
		bind, per = "0.0.0.0", MaxPerDevice
	}
	l, err := Listen(bind, 0, per)
	if err != nil {
		t.Fatal(err)
	}
	gs.port = l.Addr().(*net.TCPAddr).Port
	mints := g.Mints[0]
	gs.app = nil
	a, err := New(Options{LAN: lan, Port: gs.port, PublicPort: gs.port,
		Clock: o.clock,
		Hosts: []string{"named"}, Hostname: g.Hostname[0],
		LANIP:    func() string { return "192.168.1.20" },
		ResetCmd: "lvp game reset", Live: live, Log: log,
		Desk: d, Gate: gate.New(goldenPW, lan || o.login, nil, nil),
		MintFile: func() (*seats.File, error) {
			return seats.FromEnviron(nil), nil
		},
		Mint: func(ctx context.Context, f *seats.File, args []string,
			_ time.Duration) mintkey.Result {
			gs.mu.Lock()
			gs.calls = append(gs.calls, args)
			gs.mu.Unlock()
			seat := args[0]
			if strings.HasPrefix(seat, "--") {
				seat = "revoke"
			}
			m, ok := mints[seat]
			if !ok {
				m = mints["made-ok"]
			}
			return mintkey.Result{Code: int(m[0].(float64)),
				Stdout: m[1].(string), Stderr: m[2].(string)}
		}})
	if err != nil {
		t.Fatal(err)
	}
	if o.mint != nil {
		a.o.Mint = o.mint
	}
	gs.app = a
	gs.listener = l
	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx)
	d.Kick()
	srv := a.Server()
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close(); cancel() })
	time.Sleep(300 * time.Millisecond)
	return gs
}

func apply(text string, ctx map[string]string) string {
	for k, v := range ctx {
		text = strings.ReplaceAll(text, "{"+k+"}", v)
	}
	return text
}

// latin1 is the golden's text, one character per byte.
func latin1(s string) []byte {
	var b []byte
	for _, r := range s {
		b = append(b, byte(r))
	}
	return b
}

func buildRaw(s step, ctx map[string]string) []byte {
	if s.Raw != nil {
		return latin1(apply(*s.Raw, ctx))
	}
	host := "127.0.0.1:{port}"
	if s.Host != nil {
		host = *s.Host
	}
	host = apply(host, ctx)
	method := s.Method
	if method == "" {
		method = "GET"
	}
	lines := []string{method + " " + apply(s.Path, ctx) + " HTTP/1.1"}
	if host != "" {
		lines = append(lines, "Host: "+host)
	}
	var hdrs [][2]string
	for _, h := range s.Headers {
		hdrs = append(hdrs, [2]string{h[0], apply(h[1], ctx)})
	}
	cookie := "{cookie}"
	if s.Cookie != nil {
		cookie = *s.Cookie
	}
	cookie = apply(cookie, ctx)
	if cookie != "" && !strings.Contains(cookie, "{") {
		hdrs = append(hdrs, [2]string{"Cookie", "fd_" + ctx["port"] +
			"=" + cookie})
	}
	var body []byte
	if method == "POST" {
		ct := "application/json"
		if s.has["ctype"] {
			if s.Ctype == nil {
				ct = ""
			} else {
				ct = *s.Ctype
			}
		}
		if !s.has["ctype"] || s.Ctype != nil {
			hdrs = append(hdrs, [2]string{"Content-Type", ct})
		}
		csrf := "{csrf}"
		if s.has["csrf"] {
			if s.CSRF == nil {
				csrf = ""
			} else {
				csrf = *s.CSRF
			}
		}
		csrf = apply(csrf, ctx)
		if csrf != "" && !strings.Contains(csrf, "{") {
			hdrs = append(hdrs, [2]string{"X-CSRF-Token", csrf})
		}
		if s.Body != nil {
			body = latin1(apply(*s.Body, ctx))
		}
		length := strconv.Itoa(len(body))
		if s.has["length"] {
			if s.Length == nil {
				length = ""
			} else {
				length = *s.Length
			}
		}
		if !s.has["length"] || s.Length != nil {
			hdrs = append(hdrs, [2]string{"Content-Length", length})
		}
	}
	for _, h := range hdrs {
		lines = append(lines, h[0]+": "+h[1])
	}
	raw := []byte(strings.Join(lines, "\r\n") + "\r\n\r\n")
	return append(raw, body...)
}

type response struct {
	status  int
	headers map[string]string
	body    []byte
}

func exchange(t *testing.T, src string, port int, raw []byte,
	method string) response {
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(src)},
		Timeout: 15 * time.Second}
	c, err := d.Dial("tcp4", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	c.Write(raw)
	var data []byte
	buf := make([]byte, 65536)
	for !bytes.Contains(data, []byte("\r\n\r\n")) {
		n, err := c.Read(buf)
		data = append(data, buf[:n]...)
		if err != nil {
			break
		}
	}
	head, rest, _ := bytes.Cut(data, []byte("\r\n\r\n"))
	lines := strings.Split(string(head), "\r\n")
	if lines[0] == "" {
		return response{}
	}
	status, _ := strconv.Atoi(strings.Fields(lines[0])[1])
	h := map[string]string{}
	for _, l := range lines[1:] {
		k, v, _ := strings.Cut(l, ":")
		k = strings.ToLower(strings.TrimSpace(k))
		if _, ok := h[k]; !ok {
			h[k] = strings.TrimSpace(v)
		}
	}
	n, _ := strconv.Atoi(h["content-length"])
	if method == "HEAD" || status == 304 {
		n = 0
	}
	for len(rest) < n {
		k, err := c.Read(buf)
		rest = append(rest, buf[:k]...)
		if err != nil {
			break
		}
	}
	if len(rest) > n {
		rest = rest[:n]
	}
	return response{status, h, rest}
}

var shown = []string{"content-type", "content-length", "cache-control",
	"vary", "content-encoding", "connection", "retry-after", "server",
	"content-security-policy", "x-content-type-options",
	"x-frame-options", "referrer-policy", "transfer-encoding"}

func recordOf(r response, ctx map[string]string, s step) record {
	if r.status == 0 {
		return record{}
	}
	out := record{Status: r.status, Headers: map[string]string{}}
	body := r.body
	gz := r.headers["content-encoding"] == "gzip"
	if gz {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err == nil {
			body, _ = io.ReadAll(zr)
		}
	}
	for _, k := range shown {
		if v, ok := r.headers[k]; ok {
			out.Headers[k] = v
		}
	}
	if gz {
		delete(out.Headers, "content-length")
	}
	if _, ok := r.headers["etag"]; ok {
		out.Headers["etag"] = "<etag>"
	}
	if sc, ok := r.headers["set-cookie"]; ok {
		name, rest, _ := strings.Cut(sc, "=")
		val, attrs, _ := strings.Cut(rest, ";")
		v := ""
		if val != "" {
			v = "<cookie>"
			if s.Save == "cookie2" {
				ctx["cookie2"] = val
			} else {
				ctx["cookie"] = val
			}
		}
		out.Headers["set-cookie"] = strings.ReplaceAll(name,
			ctx["port"],
			"<port>") + "=" + v + ";" + attrs
	}
	text := string(body)
	if v, err := pyjson.Decode(body); err == nil {
		if o, ok := v.(pyjson.Obj); ok {
			for i := range o {
				if o[i].K != "csrf" {
					continue
				}
				if c, ok := o[i].V.(string); ok && c != "" {
					if s.Save == "csrf" ||
						s.Save == "csrf2" {
						ctx[s.Save] = c
					}
					if s.Src == "" || s.Src == "127.0.0.1" {
						ctx["csrf"] = c
					}
					o[i].V = "<csrf>"
				}
			}
			// Go's /api/session adds reset_cmd (ledger F-6), so
			// its length differs too.
			var keep pyjson.Obj
			for _, kv := range o {
				if kv.K != "reset_cmd" {
					keep = append(keep, kv)
				} else {
					delete(out.Headers, "content-length")
				}
			}
			v = keep
		}
		text = string(pyjson.Compact(v))
		out.JSON = true
	}
	if s.Save == "etag" {
		if e, ok := r.headers["etag"]; ok {
			ctx["etag"] = e
		}
	}
	text = strings.ReplaceAll(text, time.Now().UTC().Format(
		"2006-01-02"), "<day>")
	text = strings.ReplaceAll(text, ":"+ctx["fixport"], ":<proxy-port>")
	text = strings.ReplaceAll(text, ":"+ctx["port"], ":<port>")
	out.Body = text
	return out
}

// differs are the steps whose answers Go gives differently, on
// purpose, with the ledger entry that says why.
var differs = map[string]string{
	"body utf16": "H-6", "body nan": "H-6", "body inf": "H-6",
	"host 127.0.0.1:{port} \t":    "H-8",
	"host 127.0.0.1:{port}\u0131": "H-1",
}

// differsStatus is Go's own status for some of differs' steps.
var differsStatus = map[string]int{
	"host 127.0.0.1:{port} \t":    200, // the name, trimmed
	"host 127.0.0.1:{port}\u0131": 400, // net/http's own
}

func runSteps(t *testing.T, g *httpGolden, lan bool, cases []stepCase) {
	gs := startGo(t, g, lan)
	noname := desk.CardID(func() []byte {
		var s []byte
		for i := 0; i < 16; i++ {
			s = append(s, byte(i))
		}
		return s
	}(), "tok-noname")
	ctx := map[string]string{"port": strconv.Itoa(gs.port),
		"port1":   strconv.Itoa(gs.port + 1),
		"fixport": strconv.Itoa(gs.fixport), "noname": noname}
	for i, c := range cases {
		s := c.Step
		if s.Proxy != "" {
			gs.fix.mu.Lock()
			gs.fix.down = s.Proxy == "down"
			gs.fix.mu.Unlock()
		}
		if s.Sleep > 0 {
			time.Sleep(time.Duration(s.Sleep * float64(
				time.Second)))
		}
		gs.logs.take()
		gs.mu.Lock()
		n := len(gs.calls)
		gs.mu.Unlock()
		src := s.Src
		if src == "" {
			src = "127.0.0.1"
		}
		method := s.Method
		r := exchange(t, src, gs.port, buildRaw(s, ctx), method)
		time.Sleep(50 * time.Millisecond)
		got := recordOf(r, ctx, s)
		got.Log = gs.logs.take()
		gs.mu.Lock()
		got.MintArgs = append([][]string{}, gs.calls[n:]...)
		gs.mu.Unlock()
		if why, ok := differs[s.Name]; ok {
			t.Logf("step %d %s: differs on purpose (%s)", i, s.Name,
				why)
			if w, has := differsStatus[s.Name]; has &&
				got.Status != w {
				t.Errorf("step %d %q: status %d, want Go's %d",
					i, s.Name, got.Status, w)
			}
			continue
		}
		if msg := compare(got, c.Want); msg != "" {
			t.Errorf("step %d %q: %s", i, s.Name, msg)
		}
	}
}

func compare(got, want record) string {
	var out []string
	if got.Status != want.Status {
		out = append(out, fmt.Sprintf("status %d, want %d", got.Status,
			want.Status))
	}
	var ks []string
	for k := range want.Headers {
		ks = append(ks, k)
	}
	for k := range got.Headers {
		if _, ok := want.Headers[k]; !ok {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	for _, k := range ks {
		if want.Status == 304 && (k == "content-type" ||
			k == "content-length") {
			continue // net/http sends neither on a 304 (ledger H-7)
		}
		if k == "content-length" && got.Headers[k] == "" &&
			got.JSON {
			continue // a /api/session answer: reset_cmd (F-6)
		}
		if got.Headers[k] != want.Headers[k] {
			out = append(out, fmt.Sprintf("header %s %q, want %q",
				k,
				got.Headers[k], want.Headers[k]))
		}
	}
	if got.Body != want.Body {
		g, w := got.Body, want.Body
		if len(g) > 400 {
			g = g[:400]
		}
		if len(w) > 400 {
			w = w[:400]
		}
		out = append(out, fmt.Sprintf("body\n  %s\nwant\n  %s", g, w))
	}
	if strings.Join(got.Log, "\n") != strings.Join(want.Log, "\n") {
		out = append(out, fmt.Sprintf("log %q, want %q", got.Log,
			want.Log))
	}
	ga, _ := json.Marshal(got.MintArgs)
	wa, _ := json.Marshal(want.MintArgs)
	if want.MintArgs == nil {
		wa = []byte("[]")
	}
	if string(ga) != string(wa) {
		out = append(out, fmt.Sprintf("mint args %s, want %s", ga, wa))
	}
	return strings.Join(out, "; ")
}

func TestGoldenServerLocal(t *testing.T) {
	g := loadHTTPGolden(t)
	runSteps(t, g, false, g.Local)
}

func TestGoldenServerLAN(t *testing.T) {
	if runtime.GOOS == "darwin" {
		// macOS has only 127.0.0.1 on its loopback by default; the
		// trace logs in from 127.0.0.2 and .3 as other devices.
		t.Skip("needs 127.0.0.2 and 127.0.0.3")
	}
	g := loadHTTPGolden(t)
	runSteps(t, g, true, g.Lan)
}
