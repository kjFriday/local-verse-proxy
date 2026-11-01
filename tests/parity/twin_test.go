//go:build parity

// The twin harness (spec 16.4): Python's Front Desk (gui/frontdesk.py,
// frozen at py-reference) and Go's (lvp desk) side by side on two
// ports, each with its own fake LiteLLM holding the same keys, the
// fake docker first on PATH, and the same seats.env. Every case sends
// the same request to both and compares what came back. A difference
// must be listed in expected.json (case name to ledger IDs), or the
// run fails.
//
//	go test -tags parity -count=1 ./tests/parity
//
// It needs python3 (3.11 or newer), bash, curl and jq (Python's
// mint-key.sh runs against the fake). Without them it skips. The
// --docker mode also needs PARITY_PG_URL (a Postgres seeded with
// internal/pgread/testdata/schema.sql) and psycopg; parity.yml has
// both. PARITY_OUT, if set, names a file for the full report.
package parity

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// answer is what one side gave, ready to compare.
type answer struct {
	version string
	status  int
	header  map[string]string
	body    any // parsed JSON (masked), or text
	raw     string
}

// compared are the headers spec 16.4 compares.
var compared = []string{"Content-Type", "Cache-Control", "Vary",
	"Content-Encoding", "Connection", "Retry-After", "Server",
	"Content-Security-Policy", "X-Content-Type-Options",
	"X-Frame-Options", "Referrer-Policy", "Transfer-Encoding"}

// send writes raw (with {HOST}, {PORT}, {COOKIE}, {CSRF} filled in)
// and reads one answer.
func (s *side) send(raw string, src string) (*answer, error) {
	raw = s.fill(raw)
	d := net.Dialer{Timeout: 5 * time.Second}
	if src != "" {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(src)}
	}
	c, err := d.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(12 * time.Second))
	if _, err := io.WriteString(c, raw); err != nil {
		return nil, err
	}
	br := bufio.NewReader(c)
	method, _, _ := strings.Cut(raw, " ")
	resp, err := http.ReadResponse(br, &http.Request{Method: method})
	if err != nil {
		return &answer{raw: "no answer: " + kind(err)}, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return s.parse(resp, b), nil
}

func kind(err error) string {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return "closed"
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return "timeout"
	}
	return "error"
}

func (s *side) fill(raw string) string {
	r := strings.NewReplacer("{HOST}", fmt.Sprintf("127.0.0.1:%d",
		s.port), "{PORT}", strconv.Itoa(s.port), "{COOKIE}", s.cookie,
		"{CSRF}", s.csrf)
	return r.Replace(raw)
}

var portRE = regexp.MustCompile(`\b\d{4,5}\b`)

// ports puts names on this side's own port numbers: its own and its
// fake proxy's.
func (s *side) ports(text string) string {
	_, fp, _ := strings.Cut(strings.TrimPrefix(s.fake.URL, "http://"),
		":")
	return strings.NewReplacer(strconv.Itoa(s.port), "<port>", fp,
		"<proxy-port>").Replace(text)
}

func (s *side) parse(resp *http.Response, b []byte) *answer {
	a := &answer{version: resp.Proto, status: resp.StatusCode,
		header: map[string]string{}}
	for _, h := range compared {
		if v := resp.Header.Get(h); v != "" {
			a.header[h] = v
		}
	}
	if len(resp.TransferEncoding) > 0 {
		a.header["Transfer-Encoding"] = strings.Join(
			resp.TransferEncoding, ",")
	}
	if resp.Header.Get("ETag") != "" {
		a.header["ETag"] = "<present>"
	}
	if sc := resp.Header.Get("Set-Cookie"); sc != "" {
		parts := strings.Split(sc, ";")
		k, _, _ := strings.Cut(parts[0], "=")
		parts[0] = strings.ReplaceAll(k, strconv.Itoa(s.port),
			"<port>") + "=<value>"
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		if strings.Contains(sc, "Max-Age=0") ||
			strings.Contains(sc, `=""`) {
			parts[0] += "(empty)"
		}
		a.header["Set-Cookie"] = strings.Join(parts, "; ")
	}
	if a.header["Content-Encoding"] == "gzip" {
		if zr, err := gzip.NewReader(bytes.NewReader(b)); err == nil {
			if u, err := io.ReadAll(zr); err == nil {
				b = u
			}
		}
	}
	text := s.ports(string(b))
	a.raw = text
	var j any
	if strings.HasPrefix(a.header["Content-Type"], "application/json") &&
		json.Unmarshal([]byte(text), &j) == nil {
		a.body = mask(j, "")
	} else {
		a.body = text
	}
	return a
}

// masked are values that differ by design: per-process secrets and
// ids (F-4), and clock readings.
var masked = map[string]bool{"csrf": true, "id": true, "etag": true,
	"key": true, "ends": true}

// maskedNum are numbers that count per process: the SSE version.
var maskedNum = map[string]bool{"v": true}

func mask(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if masked[k] {
				if s, ok := e.(string); ok && s != "" {
					out[k] = "<" + k + ">"
					continue
				}
			}
			if _, ok := e.(float64); ok && maskedNum[k] {
				out[k] = "<" + k + ">"
				continue
			}
			out[k] = mask(e, k)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = mask(e, key)
		}
		return out
	}
	return v
}

// diff lists what differs between two answers.
func diff(a, b *answer) []string {
	var d []string
	if a.raw != b.raw && (a.status == 0 || b.status == 0) {
		return []string{"answer: " + short(a.raw) + " | " +
			short(b.raw)}
	}
	if a.status != b.status {
		d = append(d, fmt.Sprintf("status %d | %d", a.status, b.status))
	}
	if a.version != b.version {
		d = append(d, "version "+a.version+" | "+b.version)
	}
	keys := map[string]bool{}
	for k := range a.header {
		keys[k] = true
	}
	for k := range b.header {
		keys[k] = true
	}
	var ks []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		if a.header[k] != b.header[k] {
			d = append(d, fmt.Sprintf("header %s: %q | %q", k,
				a.header[k], b.header[k]))
		}
	}
	d = append(d, deep("body", a.body, b.body)...)
	return d
}

// deep lists the paths where two JSON values differ.
func deep(path string, a, b any) []string {
	ma, oka := a.(map[string]any)
	mb, okb := b.(map[string]any)
	if oka && okb {
		var d []string
		keys := map[string]bool{}
		for k := range ma {
			keys[k] = true
		}
		for k := range mb {
			keys[k] = true
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			va, ina := ma[k]
			vb, inb := mb[k]
			switch {
			case !ina:
				d = append(d, path+"."+k+": absent | "+js(vb))
			case !inb:
				d = append(d, path+"."+k+": "+js(
					va)+" | absent")
			default:
				d = append(d, deep(path+"."+k, va, vb)...)
			}
		}
		return d
	}
	la, oka := a.([]any)
	lb, okb := b.([]any)
	if oka && okb && len(la) == len(lb) {
		var d []string
		for i := range la {
			d = append(d, deep(fmt.Sprintf("%s[%d]", path, i),
				la[i],
				lb[i])...)
		}
		return d
	}
	if ja, jb := js(a), js(b); ja != jb {
		return []string{path + ": " + short(ja) + " | " + short(jb)}
	}
	return nil
}

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func short(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// expected is expected.json: each case that may differ, with the
// ledger entries that say why.
type expectedFile struct {
	Cases map[string][]string `json:"cases"`
}

func loadExpected(t *testing.T) map[string][]string {
	b, err := os.ReadFile("expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var e expectedFile
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal("expected.json:", err)
	}
	return e.Cases
}

// report collects every case's result for PARITY_OUT.
var report struct {
	sync.Mutex
	lines []string
}

func note(format string, a ...any) {
	report.Lock()
	report.lines = append(report.lines, fmt.Sprintf(format, a...))
	report.Unlock()
}

func writeReport(t *testing.T) {
	p := os.Getenv("PARITY_OUT")
	if p == "" {
		return
	}
	report.Lock()
	defer report.Unlock()
	os.WriteFile(p, []byte(strings.Join(report.lines, "\n")+"\n"),
		0o644)
}

// pair is the two sides of one mode.
type pair struct {
	t      *testing.T
	m      mode
	py, gs *side
	exp    map[string][]string
	n      int
	// resetSeen: the reset_cmd case was counted once.
	resetSeen bool
	same      int
	known     int
	fail      int
}

// check compares one case: a raw request (or a function that gives
// both answers) on both sides.
func (p *pair) check(name string, a, b *answer) {
	p.t.Helper()
	// F-6: Go's /api/session has reset_cmd. Checked on its own, so
	// the rest of the answer is still compared.
	if mb, ok := b.body.(map[string]any); ok {
		if ma, ok := a.body.(map[string]any); ok {
			if _, in := ma["reset_cmd"]; !in {
				if rc, in := mb["reset_cmd"]; in {
					delete(mb, "reset_cmd")
					if !p.resetSeen {
						p.resetSeen = true
						p.check("session reset_cmd",
							&answer{body: nil},
							&answer{body: rc})
					}
				}
			}
		}
	}
	p.n++
	d := diff(a, b)
	ids, listed := lookup(p.exp, p.m.name+"/"+name)
	if !listed {
		ids, listed = lookup(p.exp, name)
	}
	switch {
	case len(d) == 0:
		p.same++
		note("same     %s/%s", p.m.name, name)
	case listed:
		p.known++
		note("known    %s/%s %v\n    %s", p.m.name, name, ids,
			strings.Join(d, "\n    "))
	default:
		p.fail++
		note("DIFFERS  %s/%s\n    %s", p.m.name, name,
			strings.Join(d, "\n    "))
		p.t.Errorf("%s/%s differs, and expected.json does not list "+
			"it:\n    %s", p.m.name, name, strings.Join(d,
			"\n    "))
	}
}

// lookup finds a case in expected.json: by its name, or by a listed
// name that ends in "*" and starts the case's name (log lines are
// long).
func lookup(exp map[string][]string, name string) ([]string, bool) {
	if ids, ok := exp[name]; ok {
		return ids, true
	}
	for k, ids := range exp {
		if p, ok := strings.CutSuffix(k, "*"); ok &&
			strings.HasPrefix(name, p) {
			return ids, true
		}
	}
	return nil, false
}

func (p *pair) raw(name, raw string) {
	p.t.Helper()
	p.rawFrom(name, raw, "")
}

func (p *pair) rawFrom(name, raw, src string) {
	p.t.Helper()
	a, err := p.py.send(raw, src)
	if err != nil {
		p.t.Fatalf("%s: python: %v", name, err)
	}
	b, err := p.gs.send(raw, src)
	if err != nil {
		p.t.Fatalf("%s: go: %v", name, err)
	}
	p.check(name, a, b)
}

func req(method, path, headers, body string) string {
	h := "Host: {HOST}\r\n" + headers
	if body != "" || method == "POST" {
		h += fmt.Sprintf("Content-Length: %d\r\n", len(body))
	}
	return method + " " + path + " HTTP/1.1\r\n" + h +
		"Connection: close\r\n\r\n" + body
}

const jsonCT = "Content-Type: application/json\r\n"

func authed() string {
	return "Cookie: {COOKIE}\r\nX-CSRF-Token: {CSRF}\r\n" +
		"Origin: http://{HOST}\r\n" + jsonCT
}

// login gives each side a session (or, without a password, the csrf
// of the open session).
func (p *pair) login() {
	p.t.Helper()
	for _, s := range []*side{p.py, p.gs} {
		var raw string
		if p.m.pw {
			raw = req("POST", "/api/login", jsonCT+
				"Origin: http://{HOST}\r\n",
				`{"password": "`+deskPW+`"}`)
		} else {
			raw = req("GET", "/api/session", "", "")
		}
		a, err := s.sendFull(raw)
		if err != nil || a.status != 200 {
			p.t.Fatalf("%s: login: %v %+v", s.name, err, a)
		}
		m, _ := a.json["csrf"].(string)
		s.csrf = m
		if c := a.setCookie; c != "" {
			s.cookie, _, _ = strings.Cut(c, ";")
		}
	}
}

type full struct {
	status    int
	setCookie string
	json      map[string]any
}

func (s *side) sendFull(raw string) (*full, error) {
	raw = s.fill(raw)
	c, err := net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(12 * time.Second))
	io.WriteString(c, raw)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	f := &full{status: resp.StatusCode,
		setCookie: resp.Header.Get("Set-Cookie")}
	json.Unmarshal(b, &f.json)
	return f, nil
}

// TestTwin runs every case in every mode.
func TestTwin(t *testing.T) {
	if skipWhy != "" {
		t.Skip(skipWhy)
	}
	exp := loadExpected(t)
	runs := 1
	if n, _ := strconv.Atoi(os.Getenv("PARITY_RUNS")); n > 0 {
		runs = n
	}
	defer writeReport(t)
	for r := 1; r <= runs; r++ {
		for _, m := range modes {
			m := m
			t.Run(fmt.Sprintf("run%d/%s", r, m.name), func(
				t *testing.T) {
				if m.name == "docker" {
					if why := dockerWhy(); why != "" {
						t.Skip("--docker: " + why)
					}
				}
				p := &pair{t: t, m: m, exp: exp}
				p.py = start(t, "py", m)
				p.gs = start(t, "go", m)
				note("== run %d, mode %s", r, m.name)
				cases(p)
				logs(p)
				note("-- %s: %d cases, %d same, %d known, %d "+
					"unlisted", m.name, p.n, p.same,
					p.known,
					p.fail)
			})
		}
	}
}

// statics are the five static paths.
var statics = []string{"/", "/index.html", "/app.js", "/app.css",
	"/icon.svg"}

func cases(p *pair) {
	// 1. Every route and method, before logging in.
	paths := append(append([]string{}, statics...), "/api/session",
		"/api/state", "/api/login", "/api/logout", "/api/mint",
		"/api/revoke", "/nope")
	for _, path := range paths {
		for _, m := range []string{"GET", "HEAD", "POST", "PUT",
			"DELETE", "PATCH", "OPTIONS"} {
			body := ""
			if m == "POST" || m == "PUT" || m == "PATCH" {
				body = "{}"
			}
			p.raw("route "+m+" "+path, req(m, path, jsonCT, body))
		}
	}
	p.raw("route OPTIONS *", "OPTIONS * HTTP/1.1\r\nHost: {HOST}\r\n"+
		"Connection: close\r\n\r\n")

	// 2. Host corpus.
	hosts := []string{"127.0.0.1:{PORT}", "localhost:{PORT}",
		"[::1]:{PORT}", "127.0.0.1:1", "localhost", "127.0.0.1",
		"LOCALHOST:{PORT}", "localhost.:{PORT}", "evil.example:{PORT}",
		"192.0.2.2:{PORT}", "192.0.2.2", "[fe80::1%25eth0]:{PORT}",
		"[2001:db8::1]:{PORT}", "0177.0.0.1:{PORT}",
		"user@127.0.0.1:{PORT}", "", "127.0.0.1:{PORT}:1",
		"127.0.0.1:x", "[::1]", "10.0.0.5:{PORT}", "my-pc:{PORT}",
		"my-pc.local:{PORT}",
		// net/http trims the spaces (H-8); bytes outside ASCII
		// are its own 400 (H-1).
		"127.0.0.1:{PORT} \t", "127.0.0.1:{PORT}\u0131"}
	for _, h := range hosts {
		p.raw("host "+h, "GET /api/session HTTP/1.1\r\nHost: "+h+
			"\r\nConnection: close\r\n\r\n")
	}
	p.raw("host absent HTTP/1.0", "GET /api/session HTTP/1.0\r\n\r\n")
	p.raw("host absent HTTP/1.1", "GET /api/session HTTP/1.1\r\n"+
		"Connection: close\r\n\r\n")

	// 5. Login (before a session exists; the lock comes last).
	// Logins one second apart: the slow-down allows one try a second
	// from a device.
	gap := func() {
		if p.m.pw {
			time.Sleep(1100 * time.Millisecond)
		}
	}
	if p.m.pw {
		p.raw("login not a string", req("POST", "/api/login",
			jsonCT+"Origin: http://{HOST}\r\n", `{"password": 5}`))
		gap()
		p.raw("login lone surrogate", req("POST", "/api/login",
			jsonCT+"Origin: http://{HOST}\r\n",
			`{"password": "\ud800"}`))
		gap()
		p.raw("login 201 code points", req("POST", "/api/login",
			jsonCT+"Origin: http://{HOST}\r\n", `{"password": "`+
				strings.Repeat("a", 201)+`"}`))
	}
	gap()
	p.login()
	sessionCases(p)
	if p.m.pw {
		lockCases(p)
	}
}

func sessionCases(p *pair) {
	a := authed()
	// Session and state.
	p.raw("session", req("GET", "/api/session",
		"Cookie: {COOKIE}\r\n", ""))
	p.raw("state", req("GET", "/api/state", "Cookie: {COOKIE}\r\n",
		""))
	// 8. Static and state: encodings and conditional GETs.
	for _, path := range append(append([]string{}, statics...),
		"/api/state") {
		for _, ae := range []string{"", "gzip", "x-gzip", "GZIP",
			"gzip;q=0", "br, gzip"} {
			h := "Cookie: {COOKIE}\r\n"
			if ae != "" {
				h += "Accept-Encoding: " + ae + "\r\n"
			}
			p.raw("encoding "+path+" "+ae, req("GET", path, h, ""))
		}
		p.etag(path)
	}
	// 3. Origin on a POST, with a good session.
	for _, o := range []string{"", "http://{HOST}", "http://evil.example",
		"null", "http://127.0.0.1:1"} {
		h := "Cookie: {COOKIE}\r\nX-CSRF-Token: {CSRF}\r\n" + jsonCT
		if o != "" {
			h += "Origin: " + o + "\r\n"
		}
		p.raw("origin "+o, req("POST", "/api/revoke", h,
			`{"seat": "nobody-here"}`))
	}
	// 7. CSRF.
	for name, tok := range map[string]string{"missing": "",
		"wrong": "X-CSRF-Token: nope\r\n"} {
		p.raw("csrf "+name, req("POST", "/api/revoke",
			"Cookie: {COOKIE}\r\nOrigin: http://{HOST}\r\n"+jsonCT+
				tok, `{"seat": "nobody-here"}`))
	}
	// 4. Bodies.
	bodies := map[string][2]string{
		"none":         {"", ""},
		"array":        {jsonCT, "[1, 2]"},
		"string":       {jsonCT, `"seat"`},
		"number":       {jsonCT, "5"},
		"bad json":     {jsonCT, "{nope"},
		"bom":          {jsonCT, "\ufeff{}"},
		"invalid utf8": {jsonCT, "{\"seat\": \"\xff\"}"},
		"utf16":        {jsonCT, "\xff\xfe{\x00}\x00"},
		"nan":          {jsonCT, `{"seat": NaN}`},
		"deep": {jsonCT, strings.Repeat("[", 2000) +
			strings.Repeat("]", 2000)},
		"text type": {"Content-Type: text/plain\r\n", "{}"},
		"8192": {jsonCT, `{"seat": "` + strings.Repeat("a", 8178) +
			`"}`},
		"8193": {jsonCT, `{"seat": "` + strings.Repeat("a", 8179) +
			`"}`},
	}
	var bnames []string
	for k := range bodies {
		bnames = append(bnames, k)
	}
	sort.Strings(bnames)
	for _, k := range bnames {
		b := bodies[k]
		h := "Cookie: {COOKIE}\r\nX-CSRF-Token: {CSRF}\r\n" +
			"Origin: http://{HOST}\r\n" + b[0]
		p.raw("body "+k, req("POST", "/api/mint", h, b[1]))
	}
	for _, cl := range []string{"-1", "abc", "8193", "1000000000"} {
		p.raw("content-length "+cl, "POST /api/mint HTTP/1.1\r\n"+
			"Host: {HOST}\r\n"+a+"Content-Length: "+cl+
			"\r\nConnection: close\r\n\r\n{}")
	}
	// 10. Mint and revoke.
	mints := []struct{ name, body string }{
		{"ok", `{"seat": "dana", "models": ["local"], "budget": "2", ` +
			`"duration": "1d"}`},
		{"again", `{"seat": "dana", "models": ["local"], ` +
			`"budget": "2", "duration": "1d"}`},
		{"Sam", `{"seat": "Sam", "models": ["local"]}`},
		{"no seat", `{"models": ["local"]}`},
		{"empty seat", `{"seat": ""}`},
		{"long seat", `{"seat": "` + strings.Repeat("s", 80) + `"}`},
		{"seat dash", `{"seat": "-x"}`},
		{"seat space", `{"seat": "a b"}`},
		{"game prefix", `{"seat": "game-x"}`},
		{"game roster", `{"seat": "game-ash"}`},
		{"bad model", `{"seat": "eve", "models": ["nope"]}`},
		{"no models", `{"seat": "eve", "models": []}`},
		{"models string", `{"seat": "eve", "models": "local"}`},
		{"budget comma", `{"seat": "eve", "budget": "2,50"}`},
		{"budget minus", `{"seat": "eve", "budget": "-1"}`},
		{"budget number", `{"seat": "eve", "budget": 2.5}`},
		{"budget big", `{"seat": "eve", "budget": "1000000"}`},
		{"duration bad", `{"seat": "eve", "duration": "1y"}`},
		{"refill", `{"seat": "fay", "budget": "1", "refill": "1d"}`},
		{"refill bad", `{"seat": "fay2", "refill": "2mo"}`},
	}
	for _, m := range mints {
		p.raw("mint "+m.name, req("POST", "/api/mint", a, m.body))
	}
	p.raw("state after mints", req("GET", "/api/state",
		"Cookie: {COOKIE}\r\n", ""))
	revokes := []struct{ name, body string }{
		{"seat", `{"seat": "dana"}`},
		{"seat again", `{"seat": "dana"}`},
		{"unknown", `{"seat": "nobody-here"}`},
		{"empty", `{}`},
		{"game seat", `{"seat": "game-fern"}`},
		{"id unknown", `{"id": "zzzz"}`},
	}
	for _, r := range revokes {
		p.raw("revoke "+r.name, req("POST", "/api/revoke", a, r.body))
	}
	// Proxy failures: each fake fails the same way.
	failures := []struct {
		name   string
		status int
		body   string
	}{
		{"generate 500", 500, `{"error": {"message": "boom"}}`},
		{"generate 401", 401, `{"error": {"message": "Authentication ` +
			`Error, Invalid proxy server token passed.", "type": ` +
			`"auth_error"}}`},
	}
	for _, f := range failures {
		p.py.fake.Fail("/key/generate", f.status, f.body)
		p.gs.fake.Fail("/key/generate", f.status, f.body)
		p.raw("mint "+f.name, req("POST", "/api/mint", a,
			`{"seat": "gail"}`))
		p.py.fake.Fail("/key/generate", 0, "")
		p.gs.fake.Fail("/key/generate", 0, "")
	}
	p.py.fake.Down(true)
	p.gs.fake.Down(true)
	p.raw("mint proxy down", req("POST", "/api/mint", a,
		`{"seat": "hal"}`))
	p.raw("revoke proxy down", req("POST", "/api/revoke", a,
		`{"seat": "sam"}`))
	time.Sleep(2500 * time.Millisecond)
	p.raw("state proxy down", req("GET", "/api/state",
		"Cookie: {COOKIE}\r\n", ""))
	p.py.fake.Down(false)
	p.gs.fake.Down(false)
	// 9. SSE: headers and the first event.
	p.sse()
	// 11. The tally when psql fails, then answers again.
	if p.m.name != "docker" {
		for _, s := range []*side{p.py, p.gs} {
			os.WriteFile(filepath.Join(s.dir, "fd.script"),
				[]byte("die psql: could not connect\n"), 0o644)
		}
		time.Sleep(7 * time.Second)
		p.raw("state psql failing", req("GET", "/api/state",
			"Cookie: {COOKIE}\r\n", ""))
	}
	// 13. seats.env edited while running (host mode): new defaults.
	if p.m.name != "docker" {
		for _, s := range []*side{p.py, p.gs} {
			f := filepath.Join(s.dir, "seats.env")
			b, _ := os.ReadFile(f)
			b = bytes.Replace(b, []byte("DEFAULT_BUDGET=5"),
				[]byte("DEFAULT_BUDGET=7"), 1)
			os.WriteFile(f, b, 0o600)
		}
		time.Sleep(3 * time.Second)
		p.raw("state after seats.env edit", req("GET", "/api/state",
			"Cookie: {COOKIE}\r\n", ""))
	}
	// 14. Caps: 33 stalled connections from one device, then 257.
	if (p.m.name == "lan" || p.m.name == "docker") &&
		runtime.GOOS == "linux" {
		p.caps()
	}
	// 6. Sessions: another device's cookie (loopback aliases).
	if p.m.pw && runtime.GOOS == "linux" {
		p.rawFrom("session other device", req("GET", "/api/state",
			"Cookie: {COOKIE}\r\n", ""), "127.0.0.77")
	}
	p.raw("logout", req("POST", "/api/logout", a, "{}"))
	p.raw("state after logout", req("GET", "/api/state",
		"Cookie: {COOKIE}\r\n", ""))
	if p.m.pw {
		time.Sleep(1100 * time.Millisecond)
	}
	p.login()
}

// etag: a GET, then the same with If-None-Match from that side.
func (p *pair) etag(path string) {
	var got [2]*answer
	for i, s := range []*side{p.py, p.gs} {
		raw := req("GET", path, "Cookie: {COOKIE}\r\n", "")
		c, err := net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
		if err != nil {
			p.t.Fatal(err)
		}
		io.WriteString(c, s.fill(raw))
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			p.t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		c.Close()
		tag := resp.Header.Get("ETag")
		got[i], _ = s.send(req("GET", path, "Cookie: {COOKIE}\r\n"+
			"If-None-Match: "+tag+"\r\nAccept-Encoding: gzip\r\n",
			""), "")
	}
	p.check("if-none-match "+path, got[0], got[1])
	p.raw("if-none-match other "+path, req("GET", path,
		"Cookie: {COOKIE}\r\nIf-None-Match: \"nope\"\r\n", ""))
	p.raw("head "+path, req("HEAD", path, "Cookie: {COOKIE}\r\n", ""))
}

// sse reads each side's stream for a moment: its headers and the
// names and data of the events, and that nothing is chunked.
func (p *pair) sse() {
	var got [2]*answer
	for i, s := range []*side{p.py, p.gs} {
		c, err := net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
		if err != nil {
			p.t.Fatal(err)
		}
		io.WriteString(c, s.fill("GET /api/events HTTP/1.1\r\n"+
			"Host: {HOST}\r\nCookie: {COOKIE}\r\n\r\n"))
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		b, _ := io.ReadAll(c)
		c.Close()
		head, body, _ := strings.Cut(string(b), "\r\n\r\n")
		lines := strings.Split(head, "\r\n")
		a := &answer{header: map[string]string{}}
		fmt.Sscanf(lines[0], "%s %d", &a.version, &a.status)
		for _, l := range lines[1:] {
			k, v, _ := strings.Cut(l, ": ")
			for _, h := range compared {
				if strings.EqualFold(k, h) {
					a.header[h] = v
				}
			}
		}
		var events []any
		for _, ev := range strings.Split(body, "\n\n") {
			if strings.TrimSpace(ev) == "" {
				continue
			}
			e := map[string]any{}
			for _, l := range strings.Split(ev, "\n") {
				k, v, _ := strings.Cut(l, ": ")
				var j any
				if k == "data" && json.Unmarshal([]byte(v),
					&j) == nil {
					e[k] = mask(j, "")
				} else {
					e[k] = v
				}
			}
			events = append(events, e)
		}
		// How many changes land in the window is timing: the
		// prelude and the first event are compared.
		if len(events) > 2 {
			events = events[:2]
		}
		a.body = events
		a.raw = body
		got[i] = a
	}
	p.check("sse first events", got[0], got[1])
}

// lockCases: five wrong passwords, the sixth, then the right one.
func lockCases(p *pair) {
	for i := 1; i <= 6; i++ {
		p.raw(fmt.Sprintf("login wrong %d", i), req("POST",
			"/api/login",
			jsonCT+"Origin: http://{HOST}\r\n",
			`{"password": "wrong-one"}`))
		time.Sleep(1100 * time.Millisecond)
	}
	p.raw("login right while locked", req("POST", "/api/login",
		jsonCT+"Origin: http://{HOST}\r\n",
		`{"password": "`+deskPW+`"}`))
}

var timeRE = regexp.MustCompile(`^\d\d:\d\d:\d\d `)

// logs compares the two logs, times cut, as sets of lines.
func logs(p *pair) {
	clean := func(s string) map[string]int {
		m := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			l = timeRE.ReplaceAllString(strings.TrimRight(l, "\r"),
				"")
			if l == "" {
				continue
			}
			l = portRE.ReplaceAllString(l, "<n>")
			m[l]++
		}
		return m
	}
	a, b := clean(p.py.errb.String()), clean(p.gs.errb.String())
	// Only the lines one side wrote, each compared on its own.
	var only []string
	for k := range a {
		if a[k] != b[k] {
			only = append(only, k)
		}
	}
	for k := range b {
		if _, in := a[k]; !in {
			only = append(only, k)
		}
	}
	sort.Strings(only)
	for _, k := range only {
		p.check("log "+k, &answer{status: 1, body: a[k]},
			&answer{status: 1, body: b[k]})
	}
}

// caps: how many stalled connections each side holds from one device
// and in all, and whether another device is still served.
func (p *pair) caps() {
	var got [2]*answer
	for i, s := range []*side{p.py, p.gs} {
		one := stalled(s, "127.0.0.40", 33)
		other, _ := s.send(req("GET", "/api/session", "", ""),
			"127.0.0.41")
		for _, c := range one {
			c.Close()
		}
		var all []net.Conn
		for k := 0; k < 9; k++ {
			all = append(all, stalled(s, fmt.Sprintf("127.0.8.%d",
				k+1), 30)...)
		}
		ninth, _ := s.send(req("GET", "/api/session", "", ""),
			"127.0.9.1")
		for _, c := range all {
			c.Close()
		}
		time.Sleep(500 * time.Millisecond)
		back, _ := s.send(req("GET", "/api/session", "", ""),
			"127.0.9.1")
		got[i] = &answer{status: 1, body: map[string]any{
			"one device held": len(one),
			"other device":    other.status,
			"all held":        len(all), "past 256": ninth.status,
			"after": back.status}}
	}
	p.check("caps 33 from one device, 270 in all", got[0], got[1])
}

// stalled opens n connections from src, each with half a request
// line, and gives those still open half a second later.
func stalled(s *side, src string, n int) []net.Conn {
	var socks []net.Conn
	for i := 0; i < n; i++ {
		d := net.Dialer{Timeout: 2 * time.Second,
			LocalAddr: &net.TCPAddr{IP: net.ParseIP(src)}}
		c, err := d.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
		if err != nil {
			continue
		}
		io.WriteString(c, "GET / HTTP/1.1\r\n")
		socks = append(socks, c)
	}
	time.Sleep(500 * time.Millisecond)
	var held []net.Conn
	for _, c := range socks {
		c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		_, err := c.Read(make([]byte, 1))
		c.SetReadDeadline(time.Time{})
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			held = append(held, c)
		} else {
			c.Close()
		}
	}
	return held
}

// TestTwinFakeOllama (16.4 case 15): the same requests to Python's
// fake_ollama.py and to lvp fake-ollama. Compared: status, type, the
// field names and their JSON types, token counts and stream line
// counts (the words are random by design, P-4). lvp fake-ollama
// always listens on 11434, so this skips when that port is taken.
func TestTwinFakeOllama(t *testing.T) {
	if skipWhy != "" {
		t.Skip(skipWhy)
	}
	ln, err := net.Listen("tcp4", "0.0.0.0:11434")
	if err != nil {
		t.Skip("port 11434 is in use")
	}
	ln.Close()
	exp := loadExpected(t)
	pyPort := freePort(t)
	src, err := os.ReadFile(filepath.Join(repo, "game",
		"fake_ollama.py"))
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(src), "PORT = 11434",
		"PORT = "+strconv.Itoa(pyPort), 1)
	f := filepath.Join(t.TempDir(), "fake_ollama.py")
	os.WriteFile(f, []byte(patched), 0o644)
	py := exec.Command(python, "-B", f)
	gs := exec.Command(lvp, "fake-ollama")
	for _, c := range []*exec.Cmd{py, gs} {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		c := c
		t.Cleanup(func() { c.Process.Kill(); c.Wait() })
	}
	for _, port := range []int{pyPort, 11434} {
		for i := 0; i < 100; i++ {
			c, err := net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d",
				port))
			if err == nil {
				c.Close()
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	chat := func(stream bool, msg string) string {
		return fmt.Sprintf(`{"model": "local", "stream": %v, `+
			`"messages": [{"role": "user", "content": %q}]}`,
			stream,
			msg)
	}
	cases := []struct{ name, method, path, body string }{
		{"root", "GET", "/", ""},
		{"version", "GET", "/api/version", ""},
		{"tags", "GET", "/api/tags", ""},
		{"show", "POST", "/api/show", `{"model": "local"}`},
		{"chat", "POST", "/api/chat", chat(false, "hello there")},
		{"chat long", "POST", "/api/chat", chat(false,
			strings.Repeat("word ", 300))},
		{"chat stream", "POST", "/api/chat", chat(true, "hi")},
		{"chat empty", "POST", "/api/chat", `{}`},
		{"unknown GET", "GET", "/nope", ""},
		{"unknown POST", "POST", "/nope", "{}"},
	}
	p := &pair{t: t, m: mode{name: "fake-ollama"}, exp: exp}
	defer writeReport(t)
	for _, c := range cases {
		var got [2]*answer
		for i, port := range []int{pyPort, 11434} {
			got[i] = ollama(t, port, c.method, c.path, c.body)
		}
		p.check(c.name, got[0], got[1])
	}
	note("-- fake-ollama: %d cases, %d same, %d known, %d unlisted",
		p.n, p.same, p.known, p.fail)
}

func ollama(t *testing.T, port int, method, path,
	body string) *answer {
	r, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s",
		port, path), strings.NewReader(body))
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return &answer{raw: "no answer"}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	a := &answer{status: resp.StatusCode, header: map[string]string{
		"Content-Type": resp.Header.Get("Content-Type")}}
	var shapes []any
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for _, l := range lines {
		var j any
		if json.Unmarshal([]byte(l), &j) != nil {
			shapes = append(shapes, "text")
			continue
		}
		shapes = append(shapes, shape(j, ""))
	}
	// How long a reply is, is random by design (P-4): a stream's
	// line count follows it. So the first and last lines' shapes
	// are compared, and that a stream has more than one.
	if len(shapes) > 2 {
		shapes = []any{shapes[0], shapes[len(shapes)-1]}
	}
	a.body = map[string]any{"several lines": len(lines) > 1,
		"shapes": shapes}
	return a
}

// shape is a JSON value's field names and types, with the numbers
// that count tokens kept and other values dropped.
func shape(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = shape(e, k)
		}
		return out
	case []any:
		out := []any{}
		for _, e := range x {
			out = append(out, shape(e, key))
		}
		return out
	case float64:
		switch key {
		case "prompt_eval_count":
			return x // from the prompt: equal
		case "eval_count":
			// random: half the limit (128) to the limit, about
			if x >= 60 && x <= 135 {
				return "64 to 128"
			}
			return x
		}
		return "number"
	case string:
		return "string"
	case bool:
		return "bool"
	}
	return "null"
}
