//go:build e2e

// Package security is the security suite of spec 16.5: the reference
// scripts in tests/reference/security, ported to Go. Each test prints
// the lines its script printed (to E2E_OUT, if set, and the test log)
// and checks the facts that must hold, so a run against Python shows
// the port reproduces the recorded output and a run against Go shows
// Go does the same. Then the secret sweep: no secret in any byte
// that came back, nor in the desk's log.
//
//	E2E_PORT=8023 E2E_PWFILE=fd.pass E2E_LOG=fd.err \
//	  E2E_SEATS_ENV=seats.env go test -tags e2e ./tests/e2e/security
//
// E2E_MODE=lan (the default) needs the desk started with --lan, and
// loopback aliases as devices (Linux has 127.0.0.0/8; macOS only
// 127.0.0.1, so there the multi-device steps are skipped). With
// E2E_MODE=docker every request is one device, as Docker shows them.
package security

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/tests/e2e/e2ekit"
)

var out struct {
	sync.Mutex
	f *os.File
}

func TestMain(m *testing.M) {
	if p := os.Getenv("E2E_OUT"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		out.f = f
	}
	os.Exit(m.Run())
}

// say prints one line as the reference scripts did.
func say(t *testing.T, format string, a ...any) {
	t.Helper()
	line := fmt.Sprintf(format, a...)
	t.Log(line)
	out.Lock()
	if out.f != nil {
		fmt.Fprintln(out.f, line)
	}
	out.Unlock()
}

func devices(t *testing.T, d *e2ekit.Desk) {
	t.Helper()
	if !d.Devices() {
		t.Skip("needs loopback aliases as devices (E2E_MODE=lan)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("loopback aliases: Linux only")
	}
}

func req(t *testing.T, d *e2ekit.Desk, src, method, path string,
	body any, raw []byte, h map[string]string) *e2ekit.Answer {
	t.Helper()
	a, err := d.Req(src, method, path, body, raw, h)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return a
}

// TestHostCheck is sec_vb's first part: what each Host gets.
func TestHostCheck(t *testing.T) {
	d := e2ekit.FromEnv(t)
	p := d.Port
	me, _ := os.Hostname()
	me = strings.ToLower(me)
	type c struct {
		host string
		ok   bool // must be served
		bad  bool // must be refused
	}
	cases := []c{
		{fmt.Sprintf("192.0.2.2:%d", p), true, false},
		{fmt.Sprintf("127.0.0.1:%d", p), true, false},
		{fmt.Sprintf("localhost:%d", p), true, false},
		{fmt.Sprintf("[::1]:%d", p), true, false},
		{fmt.Sprintf("%s:%d", me, p), false, false},
		{fmt.Sprintf("%s.local:%d", me, p), false, false},
		{fmt.Sprintf("evil.example:%d", p), false, true},
		{"evil.example", false, true},
		{fmt.Sprintf("127.0.0.1.nip.io:%d", p), false, true},
		{"", false, true},
	}
	if d.Mode == "lan" {
		cases = append(cases, c{"192.0.2.2:9999", false, true})
	}
	for _, k := range cases {
		line, _ := d.Raw("127.0.0.30", []byte("GET / HTTP/1.1\r\n"+
			"Host: "+k.host+"\r\nConnection: close\r\n\r\n"),
			12*time.Second)
		say(t, "  Host %-28q -> %s", k.host, line)
		served := strings.Contains(line, " 200 ")
		if k.ok && !served || k.bad && served {
			t.Errorf("Host %q: %s", k.host, line)
		}
	}
	line, _ := d.Raw("127.0.0.30", []byte("GET / HTTP/1.1\r\n"+
		"Connection: close\r\n\r\n"), 12*time.Second)
	say(t, "  (no Host header)             -> %s", line)
	if strings.Contains(line, " 200 ") {
		t.Errorf("no Host: %s", line)
	}
	line, _ = d.Raw("127.0.0.30", []byte(fmt.Sprintf("POST /api/login "+
		"HTTP/1.1\r\nHost: evil.example:%d\r\nContent-Type: "+
		"application/json\r\nContent-Length: 2\r\n\r\n{}", p)),
		12*time.Second)
	say(t, "  POST /api/login Host evil     -> %s", line)
	if !strings.Contains(line, " 421 ") {
		t.Errorf("login with Host evil: %s", line)
	}
}

// TestCookieCSRF is sec_vb's cookie, CSRF and Origin parts.
func TestCookieCSRF(t *testing.T) {
	d := e2ekit.FromEnv(t)
	if d.PW == "" {
		t.Skip("needs a password (E2E_PWFILE)")
	}
	src := "127.0.0.30"
	a, err := d.Login(src, d.PW)
	if err != nil || a.Status != 200 {
		t.Fatalf("login: %v %v", err, a)
	}
	sc := a.Header.Get("Set-Cookie")
	var parts []string
	for _, p := range strings.Split(sc, ";") {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "fd_") {
			k, _, _ := strings.Cut(p, "=")
			p = k + "=<value>"
		}
		parts = append(parts, p)
	}
	say(t, "  Set-Cookie flags: %s", strings.Join(parts, ";"))
	for _, want := range []string{"HttpOnly", "SameSite=Strict",
		"Path=/"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Set-Cookie lacks %s", want)
		}
	}
	ck, _, _ := strings.Cut(sc, ";")
	csrf, _ := a.JSON["csrf"].(string)
	o := "http://" + d.Host
	mint := map[string]any{"seat": "vb-csrf", "models": []string{"local"},
		"budget": "1", "duration": "1d"}
	for _, pb := range []struct {
		path string
		body any
	}{{"/api/mint", mint},
		{"/api/revoke", map[string]string{"seat": "vb-r-none"}},
		{"/api/logout", map[string]string{}}} {
		cases := []struct {
			name string
			h    map[string]string
		}{
			{"no cookie", map[string]string{"Origin": o,
				"X-CSRF-Token": csrf}},
			{"cookie, no token", map[string]string{"Origin": o,
				"Cookie": ck}},
			{"cookie, wrong token", map[string]string{"Origin": o,
				"Cookie": ck, "X-CSRF-Token": strings.Repeat(
					"x",
					32)}},
			{"cookie+token, Origin evil", map[string]string{
				"Origin": "http://evil.example", "Cookie": ck,
				"X-CSRF-Token": csrf}},
			{"cookie+token, Origin null", map[string]string{
				"Origin": "null", "Cookie": ck,
				"X-CSRF-Token": csrf}},
			{"cookie+token, other port", map[string]string{
				"Origin": "http://192.0.2.2:4133", "Cookie": ck,
				"X-CSRF-Token": csrf}},
		}
		for _, c := range cases {
			r := req(t, d, src, "POST", pb.path, pb.body, nil, c.h)
			say(t, "  %-12s %-28s -> %d %q", pb.path, c.name,
				r.Status, r.Err())
			if r.Status < 400 {
				t.Errorf("%s %s: %d", pb.path, c.name, r.Status)
			}
		}
		if d.Devices() && runtime.GOOS == "linux" {
			r := req(t, d, "127.0.0.31", "POST", pb.path, pb.body,
				nil, map[string]string{"Origin": o,
					"Cookie":       ck,
					"X-CSRF-Token": csrf})
			say(t, "  %-12s %-28s -> %d %q", pb.path,
				"cookie+token, other device", r.Status, r.Err())
			if r.Status < 400 {
				t.Errorf("other device: %d", r.Status)
			}
		}
		for _, ct := range []string{"text/plain",
			"application/x-www-form-urlencoded",
			"multipart/form-data; boundary=x"} {
			raw := []byte(`{"seat": "vb-csrf"}`)
			r := req(t, d, src, "POST", pb.path, nil, raw,
				map[string]string{"Origin": o, "Cookie": ck,
					"X-CSRF-Token": csrf,
					"Content-Type": ct})
			say(t, "  %-12s %-28s -> %d %q", pb.path,
				"Content-Type "+cut(ct, 18), r.Status, r.Err())
			if r.Status < 400 {
				t.Errorf("Content-Type %s: %d", ct, r.Status)
			}
		}
	}
	r := req(t, d, src, "GET", "/api/state", nil, nil, nil)
	say(t, "  GET /api/state no cookie -> %d", r.Status)
	if r.Status != 401 {
		t.Errorf("state without cookie: %d", r.Status)
	}
	if d.Devices() && runtime.GOOS == "linux" {
		// The logout above ended that session; a fresh one.
		ck2, _ := d.Session(t, src)
		r = req(t, d, "127.0.0.31", "GET", "/api/state", nil, nil,
			map[string]string{"Cookie": ck2})
		say(t, "  GET /api/state cookie from other device -> %d",
			r.Status)
		if r.Status != 401 {
			t.Errorf("state from other device: %d", r.Status)
		}
	}
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// TestBodyLimits is sec_vb's body part: sizes and shapes.
func TestBodyLimits(t *testing.T) {
	d := e2ekit.FromEnv(t)
	src := "127.0.0.30"
	ck, csrf := d.Session(t, src)
	h := fmt.Sprintf("Origin: http://%s\r\nX-CSRF-Token: %s\r\n"+
		"Content-Type: application/json\r\n", d.Host, csrf)
	if ck != "" {
		h += "Cookie: " + ck + "\r\n"
	}
	cases := []struct {
		name, cl string
		body     []byte
		want     string
	}{
		// As sec_vb built them: 8193 and 8194 bytes in fact.
		{"8192 bytes", "", []byte(`{"seat":"` +
			strings.Repeat("a", 8182) + `"}`), " 400 "},
		{"8193 bytes", "", []byte(`{"seat":"` +
			strings.Repeat("a", 8183) + `"}`), " 400 "},
		{"CL -1", "-1", []byte("{}"), " 400 "},
		{"CL abc", "abc", []byte("{}"), " 400 "},
		{"CL 10**9", "1000000000", []byte("{}"), " 400 "},
		{"JSON array", "", []byte("[1,2]"), " 400 "},
		{"bad JSON", "", []byte("{nope"), " 400 "},
		{"deep nest", "", []byte(strings.Repeat("[", 4000) +
			strings.Repeat("]", 4000)), " 400 "},
	}
	for _, c := range cases {
		cl := c.cl
		if cl == "" {
			cl = fmt.Sprint(len(c.body))
		}
		t0 := time.Now()
		line, all := d.Raw(src, append([]byte(fmt.Sprintf(
			"POST /api/mint HTTP/1.1\r\nHost: %s\r\n%s"+
				"Content-Length: %s\r\n\r\n", d.Host, h, cl)),
			c.body...), 12*time.Second)
		_, body, _ := bytes.Cut(all, []byte("\r\n\r\n"))
		say(t, "  %-12s -> %s %q (%.2f s)", c.name, line,
			cut(string(body), 60), time.Since(t0).Seconds())
		if !strings.Contains(line+" ", c.want) {
			t.Errorf("%s: %s, want%s", c.name, line, c.want)
		}
	}
}

// TestCaps is cap_vb: 32 connections per device, 256 in all, others
// still served, slots given back.
func TestCaps(t *testing.T) {
	d := e2ekit.FromEnv(t)
	devices(t, d)
	say(t, "baseline: %d threads", d.Threads())
	held := stall(d, "127.0.0.5", 400)
	say(t, "A: 127.0.0.5 opens 400 stalled: %d still open, %d threads",
		len(held), d.Threads())
	if len(held) != 32 {
		t.Errorf("one device held %d, want 32", len(held))
	}
	res := make([]string, 21)
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i] = d.Page(fmt.Sprintf("127.0.6.%d", i))
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, r := range res[1:] {
		if strings.HasPrefix(r, "200") {
			ok++
		}
	}
	say(t, "   20 other devices load the page at once: %d/20 ok; e.g. "+
		"%s | %s", ok, res[1], res[20])
	if ok != 20 {
		t.Errorf("others served: %d/20", ok)
	}
	same := d.Page("127.0.0.5")
	say(t, "   same device 127.0.0.5: %s", same)
	if !strings.HasPrefix(same, "refused") {
		t.Errorf("same device: %s", same)
	}
	stop := make(chan struct{})
	go func() {
		tk := time.NewTicker(4 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				for _, c := range held {
					c.Write([]byte("X"))
				}
			}
		}
	}()
	time.Sleep(13 * time.Second)
	alive := 0
	for _, c := range held {
		if open(c) {
			alive++
		}
	}
	other := d.Page("127.0.6.99")
	say(t, "B: after 13 s of drip, %d of 127.0.0.5's conns alive; "+
		"other device: %s; threads %d", alive, other, d.Threads())
	close(stop)
	if !strings.HasPrefix(other, "200") {
		t.Errorf("other device during drip: %s", other)
	}
	for _, c := range held {
		c.Close()
	}
	time.Sleep(500 * time.Millisecond)
	back := d.Page("127.0.0.5")
	say(t, "   127.0.0.5 after letting go: %s threads %d", back,
		d.Threads())
	if !strings.HasPrefix(back, "200") {
		t.Errorf("after letting go: %s", back)
	}
	h2 := append(stall(d, "127.0.0.11", 100), stall(d, "127.0.0.12",
		100)...)
	other = d.Page("127.0.6.50")
	say(t, "C: 2 addresses x 100 stalled: %d held; other device: %s",
		len(h2), other)
	if len(h2) != 64 || !strings.HasPrefix(other, "200") {
		t.Errorf("C: %d held, other %s", len(h2), other)
	}
	for _, c := range h2 {
		c.Close()
	}
	var h3 []net.Conn
	for i := 0; i < 8; i++ {
		h3 = append(h3, stall(d, fmt.Sprintf("127.0.4.%d", i+1),
			40)...)
	}
	ninth := d.Page("127.0.4.99")
	say(t, "D: 8 addresses x 40 stalled: %d held, threads %d; 9th "+
		"device: %s", len(h3), d.Threads(), ninth)
	if len(h3) != 256 || !strings.HasPrefix(ninth, "refused") {
		t.Errorf("D: %d held, 9th %s", len(h3), ninth)
	}
	for _, c := range h3 {
		c.Close()
	}
	time.Sleep(500 * time.Millisecond)
	ninth = d.Page("127.0.4.99")
	say(t, "   after they let go, 9th device: %s threads %d", ninth,
		d.Threads())
	if !strings.HasPrefix(ninth, "200") {
		t.Errorf("after D: %s", ninth)
	}
}

// stall opens n connections from src that send half a request line,
// and gives those still open half a second later.
func stall(d *e2ekit.Desk, src string, n int) []net.Conn {
	var socks []net.Conn
	for i := 0; i < n; i++ {
		c, err := d.Dial(src, 5*time.Second)
		if err != nil {
			continue
		}
		c.Write([]byte("GET / HTTP/1.1\r\n"))
		socks = append(socks, c)
	}
	time.Sleep(500 * time.Millisecond)
	var held []net.Conn
	for _, c := range socks {
		if open(c) {
			held = append(held, c)
		} else {
			c.Close()
		}
	}
	return held
}

// open: nothing came and the connection was not closed.
func open(c net.Conn) bool {
	c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	b := make([]byte, 1)
	_, err := c.Read(b)
	c.SetReadDeadline(time.Time{})
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	return false
}

// TestOddRequestsNoLog is fuzz_vb: odd requests from a device that is
// not logged in write no log line.
func TestOddRequestsNoLog(t *testing.T) {
	d := e2ekit.FromEnv(t)
	if d.Log == "" {
		t.Skip("needs the desk's log file (E2E_LOG)")
	}
	h := d.Host
	post := func(path string, body []byte) []byte {
		return append([]byte(fmt.Sprintf("POST %s HTTP/1.1\r\nHost: "+
			"%s\r\nContent-Type: application/json\r\n"+
			"Content-Length: %d\r\nConnection: close\r\n\r\n", path,
			h, len(body))), body...)
	}
	get := func(path, extra string) []byte {
		return []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\n%s"+
			"Connection: close\r\n\r\n", path, h, extra))
	}
	cases := []struct {
		name string
		data []byte
	}{
		{"login surrogate", post("/api/login",
			[]byte(`{"password":"\udc80"}`))},
		{"login nested 990", post("/api/login",
			[]byte(strings.Repeat("[", 990)+
				strings.Repeat("]", 990)))},
		{"login nested obj", post("/api/login",
			[]byte(strings.Repeat(`{"a":`, 1500)+"1"+
				strings.Repeat("}", 1500)))},
		{"login 5000-digit int", post("/api/login",
			[]byte(`{"password":`+strings.Repeat("9", 5000)+
				"}"))},
		{"login bad utf-8", post("/api/login",
			[]byte("{\"password\":\"\xff\xfe\"}"))},
		{"login NaN", post("/api/login", []byte(`{"password":NaN}`))},
		{"mint surrogate seat", post("/api/mint",
			[]byte(`{"seat":"\ud800"}`))},
		{"bad cookie", get("/api/state", fmt.Sprintf(
			"Cookie: fd_%d=\"\\x00;;=\r\n", d.Port))},
		{"cookie latin1", get("/api/state",
			"Cookie: fd_x=\xe9\xff\r\n")},
		{"no body", []byte(fmt.Sprintf("POST /api/login HTTP/1.1\r\n"+
			"Host: %s\r\nConnection: close\r\n\r\n", h))},
		{"half request", []byte("GET / HTTP/1.1\r\nHost: " + h)},
		{"garbage", []byte(
			"\x16\x03\x01\x00\xa5\x01\x00\x00\xa1\r\n\r\n")},
	}
	for _, c := range cases {
		before := d.LogSize()
		line, _ := d.Raw("127.0.11.1", c.data, 3*time.Second)
		time.Sleep(100 * time.Millisecond)
		grew := d.LogSince(before)
		say(t, "  %-22s -> %-40s log +%d bytes", c.name, cut(line, 40),
			len(grew))
		// A lone surrogate is still a text: a wrong password, which
		// is logged by design, as in Python.
		if c.name == "login surrogate" && wrongOnly(grew) {
			continue
		}
		if grew != "" {
			t.Errorf("%s wrote to the log: %q", c.name, cut(grew,
				200))
		}
	}
}

// wrongOnly: every line of log is a wrong-password line.
func wrongOnly(log string) bool {
	for _, l := range strings.Split(strings.TrimSpace(log), "\n") {
		if !strings.Contains(l, "wrong password from ") {
			return false
		}
	}
	return true
}

// TestSSEAfterResets is misc_vb's stream part: 30 streams reset hard,
// then 30 again are all served.
func TestSSEAfterResets(t *testing.T) {
	d := e2ekit.FromEnv(t)
	src := "127.0.15.1"
	ck, _ := d.Session(t, src)
	open30 := func() (map[string]int, []net.Conn) {
		codes := map[string]int{}
		var socks []net.Conn
		for i := 0; i < 30; i++ {
			c, err := d.Dial(src, 5*time.Second)
			if err != nil {
				codes["refused"]++
				continue
			}
			fmt.Fprintf(c,
				"GET /api/events HTTP/1.1\r\nHost: %s\r\n"+
					"Cookie: %s\r\n\r\n", d.Host, ck)
			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			b := make([]byte, 200)
			n, _ := c.Read(b)
			first, _, _ := strings.Cut(string(b[:n]), "\r\n")
			codes[first]++
			socks = append(socks, c)
		}
		return codes, socks
	}
	codes, socks := open30()
	say(t, "  30 streams: %v", codes)
	for _, c := range socks {
		if tc, ok := c.(*net.TCPConn); ok {
			tc.SetLinger(0)
		}
		c.Close()
	}
	time.Sleep(2500 * time.Millisecond)
	codes2, socks := open30()
	say(t, "  after resets, 30 streams again: %v", codes2)
	for _, c := range socks {
		c.Close()
	}
	if codes2["HTTP/1.1 200 OK"]+codes2["HTTP/1.0 200 OK"] != 30 {
		t.Errorf("after resets: %v", codes2)
	}
}

// The lock test comes last but the sweep: in Docker every request
// is one device, so its lock would refuse every later login.

const wrong = "wrong-password-xyz"

type try struct {
	st  int
	msg string
	ra  string
}

func (r try) String() string {
	ra := "None"
	if r.ra != "" {
		ra = "'" + r.ra + "'"
	}
	return fmt.Sprintf("(%d, '%s', %s)", r.st, r.msg, ra)
}

func login(t *testing.T, d *e2ekit.Desk, src, pw string) try {
	a, err := d.Login(src, pw)
	if err != nil {
		t.Errorf("login from %s: %v", src, err)
		return try{}
	}
	msg := a.Err()
	if msg == "" {
		msg = "ok"
	}
	return try{a.Status, msg, a.Header.Get("Retry-After")}
}

// TestLock is lock_vb: the login lock and slow-down. Step 1's lines
// must equal the recorded output (tests/reference/security/
// lock4163.out) but for the Retry-After count, which is a clock.
func TestLock(t *testing.T) {
	d := e2ekit.FromEnv(t)
	if d.PW == "" {
		t.Skip("needs a password (E2E_PWFILE)")
	}
	ref, err := os.ReadFile("../../reference/security/lock4163.out")
	if err != nil {
		t.Fatal(err)
	}
	refLines := strings.Split(string(ref), "\n")
	say(t, "== 1. one device, 6 wrong tries 1.1 s apart, then right")
	var got []string
	for i := 0; i < 6; i++ {
		got = append(got, fmt.Sprintf("  .7 wrong %d %s", i+1,
			login(t, d, "127.0.0.7", wrong)))
		say(t, "%s", got[len(got)-1])
		time.Sleep(1100 * time.Millisecond)
	}
	got = append(got, fmt.Sprintf("  .7 RIGHT (locked): %s",
		login(t, d, "127.0.0.7", d.PW)))
	say(t, "%s", got[len(got)-1])
	for i, g := range got {
		if norm(g) != norm(refLines[1+i]) {
			t.Errorf("line %d:\n got %s\nwant %s", i+2, g,
				refLines[1+i])
		}
	}
	if !d.Devices() || runtime.GOOS != "linux" {
		return
	}
	time.Sleep(1100 * time.Millisecond)
	r := login(t, d, "127.0.0.8", d.PW)
	say(t, "  .8 RIGHT: (%d,)", r.st)
	if r.st != 200 {
		t.Errorf(".8 right: %v", r)
	}

	say(t, "== 2. locked .7 floods 300 tries; fresh device logs in "+
		"meanwhile")
	var mu sync.Mutex
	codes := map[int]int{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			st := login(t, d, "127.0.0.7", wrong).st
			mu.Lock()
			codes[st]++
			mu.Unlock()
		}
	}()
	time.Sleep(200 * time.Millisecond)
	r = login(t, d, "127.0.0.9", d.PW)
	<-done
	say(t, "  locked flood codes: %v | fresh .9 RIGHT during it: (%d,)",
		codes, r.st)
	if codes[429] != 300 || r.st != 200 {
		t.Errorf("flood %v, fresh %d", codes, r.st)
	}

	say(t, "== 3. 5 wrong each from 3 devices, then a fresh device")
	for _, a := range []string{"127.0.1.1", "127.0.1.2", "127.0.1.3"} {
		var st try
		for i := 0; i < 5; i++ {
			st = login(t, d, a, wrong)
			time.Sleep(1050 * time.Millisecond)
		}
		say(t, "  %s 5th answer: %d %s", a, st.st, none(st.ra))
		if st.st != 401 {
			t.Errorf("%s 5th: %v", a, st)
		}
	}
	time.Sleep(1100 * time.Millisecond)
	r = login(t, d, "127.0.2.50", d.PW)
	say(t, "  fresh 127.0.2.50 RIGHT: (%d,)", r.st)
	if r.st != 200 {
		t.Errorf("fresh after 3: %v", r)
	}

	say(t, "== 4. 200 wrong from 40 devices, 8 threads at once")
	type key struct {
		st int
		ra string
	}
	c4 := map[key]int{}
	t0 := time.Now()
	var wg sync.WaitGroup
	for k := 0; k < 8; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				st := login(t, d, fmt.Sprintf("127.0.3.%d",
					(k*25+i)%40+1), wrong)
				mu.Lock()
				c4[key{st.st, st.ra}]++
				mu.Unlock()
			}
		}(k)
	}
	wg.Wait()
	var ks []string
	for k, n := range c4 {
		ks = append(ks, fmt.Sprintf("(%d, %s): %d", k.st, none(k.ra),
			n))
	}
	sort.Strings(ks)
	say(t, "  %.2f s, (status, Retry-After): {%s}",
		time.Since(t0).Seconds(), strings.Join(ks, ", "))
	if c4[key{401, ""}] != 40 || c4[key{429, "1"}] != 160 {
		t.Errorf("flood of 200: %v", c4)
	}
	r = login(t, d, "127.0.3.200", d.PW)
	say(t, "  RIGHT password at once: %s", r)
	if r.st != 200 {
		t.Errorf("right at once: %v", r)
	}
	time.Sleep(1100 * time.Millisecond)
	r = login(t, d, "127.0.3.201", d.PW)
	say(t, "  RIGHT password 1.1 s later: (%d,)", r.st)

	say(t, "== 5. sustained: 10 attacker addresses hammer, legit obeys "+
		"Retry-After")
	stop := make(chan struct{})
	var checked int
	var aw sync.WaitGroup
	for k := 0; k < 2; k++ {
		aw.Add(1)
		go func() {
			defer aw.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if login(t, d, fmt.Sprintf("127.0.5.%d",
					i%10+1),
					wrong).st == 401 {
					mu.Lock()
					checked++
					mu.Unlock()
				}
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	t0 = time.Now()
	tries, got5 := 0, -1.0
	for time.Since(t0) < 90*time.Second {
		tries++
		r := login(t, d, "127.0.5.200", d.PW)
		if r.st == 200 {
			got5 = time.Since(t0).Seconds()
			break
		}
		wait := 1.0
		fmt.Sscan(r.ra, &wait)
		time.Sleep(time.Duration(wait * float64(time.Second)))
	}
	close(stop)
	aw.Wait()
	say(t, "  attacker checked tries: %d; legit got in after %.1f s and "+
		"%d tries", checked, got5, tries)
	if got5 < 0 || checked > 20 {
		t.Errorf("sustained: legit %.1f s, attacker checked %d", got5,
			checked)
	}
}

func none(s string) string {
	if s == "" {
		return "None"
	}
	return "'" + s + "'"
}

// norm drops Retry-After's seconds, a clock reading.
func norm(s string) string {
	if i := strings.LastIndex(s, ", '"); i > 0 &&
		strings.HasSuffix(s, "')") {
		return s[:i] + ", '<n>')"
	}
	return s
}

// TestSecretSweep runs last (go test keeps source order): every byte
// this suite got back, and the desk's whole log, hold no secret.
func TestSecretSweep(t *testing.T) {
	d := e2ekit.FromEnv(t)
	secrets := e2ekit.Secrets(d)
	if len(secrets) == 0 {
		t.Skip("no secrets known (E2E_SEATS_ENV, E2E_PWFILE)")
	}
	// What the suite sent holds the password on purpose (the
	// logins): the sweep looks at the answers and the log.
	answers := e2ekit.Answers()
	var logb []byte
	if d.Log != "" {
		logb, _ = os.ReadFile(d.Log)
	}
	names := make([]string, 0, len(secrets))
	for k := range secrets {
		names = append(names, k)
	}
	sort.Strings(names)
	say(t, "  sweep: %d bytes of answers, %d bytes of log, secrets "+
		"looked for: %s", len(answers), len(logb),
		strings.Join(names, ", "))
	if hits := e2ekit.Sweep(answers, secrets); len(hits) > 0 {
		t.Errorf("secrets in answers: %v", hits)
	}
	if hits := e2ekit.Sweep(logb, secrets); len(hits) > 0 {
		t.Errorf("secrets in the log: %v", hits)
	}
	say(t, "  sweep hits: 0 expected")
}
