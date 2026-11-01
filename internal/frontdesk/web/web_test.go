package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// dial opens a raw connection to the test server from src.
func dial(t *testing.T, src string, port int) net.Conn {
	t.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(src)},
		Timeout: 5 * time.Second}
	c, err := d.Dial("tcp4", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// sse opens /api/events and reads its head; the reader is left at
// the start of the stream.
func sse(t *testing.T, port int, cookie string) (net.Conn, *bufio.Reader,
	string) {
	t.Helper()
	c := dial(t, "127.0.0.1", port)
	req := "GET /api/events HTTP/1.1\r\nHost: 127.0.0.1:" +
		strconv.Itoa(port) + "\r\n"
	if cookie != "" {
		req += "Cookie: fd_" + strconv.Itoa(port) + "=" + cookie +
			"\r\n"
	}
	c.Write([]byte(req + "\r\n"))
	r := bufio.NewReader(c)
	var head strings.Builder
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("head: %v (%q)", err, head.String())
		}
		head.WriteString(line)
		if line == "\r\n" {
			return c, r, head.String()
		}
	}
}

// event reads one SSE event (up to its blank line).
func event(t *testing.T, c net.Conn, r *bufio.Reader,
	within time.Duration) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(within))
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		b.WriteString(line)
		if err != nil {
			t.Fatalf("event: %v (%q)", err, b.String())
		}
		if line == "\n" {
			return b.String()
		}
	}
}

// tickAfter moves the fake clock 15 s at a time and reads the next
// event, for up to 10 tries.
func tickAfter(t *testing.T, c net.Conn, r *bufio.Reader,
	fake *clock.Fake) string {
	t.Helper()
	var b strings.Builder
	for try := 0; try < 10; try++ {
		fake.Advance(15)
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			line, err := r.ReadString('\n')
			b.WriteString(line)
			if err != nil {
				break // no event yet: move the clock again
			}
			if line == "\n" {
				return b.String()
			}
		}
	}
	t.Fatalf("no tick (%q)", b.String())
	return ""
}

func TestSSE(t *testing.T) {
	g := loadHTTPGolden(t)
	fake := clock.NewFake(float64(time.Now().Unix()), 5000)
	gs := startGoWith(t, g, serverOpts{clock: fake})
	c, r, head := sse(t, gs.port, "")
	defer c.Close()
	low := strings.ToLower(head)
	if !strings.HasPrefix(head, "HTTP/1.1 200 OK\r\n") ||
		!strings.Contains(low, "content-type: text/event-stream\r\n") ||
		!strings.Contains(low, "cache-control: no-store\r\n") ||
		!strings.Contains(low, "connection: close\r\n") ||
		!strings.Contains(low, "server: frontdesk\r\n") ||
		!strings.Contains(low, "x-frame-options: deny\r\n") ||
		strings.Contains(low, "transfer-encoding") ||
		strings.Contains(low, "content-length") {
		t.Fatalf("head:\n%s", head)
	}
	if e := event(t, c, r, 5*time.Second); e != "retry: 3000\n\n" {
		t.Fatalf("prelude %q", e)
	}
	first := event(t, c, r, 5*time.Second)
	if !strings.HasPrefix(first, "event: changed\ndata: {\"v\":") {
		t.Fatalf("first %q", first)
	}
	// A real change: the proxy goes down; the desk checks every 2 s
	// while a page watches.
	gs.fix.mu.Lock()
	gs.fix.down = true
	gs.fix.mu.Unlock()
	next := event(t, c, r, 8*time.Second)
	if !strings.HasPrefix(next, "event: changed\ndata: {\"v\":") ||
		next == first {
		t.Fatalf("change %q after %q", next, first)
	}
	// 15 s without news: a tick, with the answer's age. The stream
	// notes when it wrote "changed" only after the write returns, so
	// on a busy machine that note may come after the clock moved:
	// move it again until the tick comes.
	tick := tickAfter(t, c, r, fake)
	if !strings.HasPrefix(tick, "event: tick\ndata: {\"age\":") ||
		!strings.HasSuffix(tick, "}\n\n") {
		t.Fatalf("tick %q", tick)
	}
	// The viewer hangs up: the stream ends, and the count comes back.
	if gs.desk.Viewers() != 1 {
		t.Fatalf("viewers %d", gs.desk.Viewers())
	}
	c.Close()
	end := time.Now().Add(5 * time.Second)
	for gs.desk.Viewers() != 0 && time.Now().Before(end) {
		time.Sleep(20 * time.Millisecond)
	}
	if gs.desk.Viewers() != 0 {
		t.Fatal("the stream did not end when its viewer left")
	}
}

func login(t *testing.T, port int) (cookie, csrf string) {
	t.Helper()
	body := `{"password": "` + goldenPW + `"}`
	req, _ := http.NewRequest("POST", "http://127.0.0.1:"+
		strconv.Itoa(port)+"/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var j struct{ CSRF string }
	json.NewDecoder(resp.Body).Decode(&j)
	for _, ck := range resp.Cookies() {
		if ck.Name == "fd_"+strconv.Itoa(port) {
			cookie = ck.Value
		}
	}
	if resp.StatusCode != 200 || cookie == "" {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	return cookie, j.CSRF
}

func TestSSELogoutEnds(t *testing.T) {
	g := loadHTTPGolden(t)
	gs := startGoWith(t, g, serverOpts{login: true})
	cookie, csrf := login(t, gs.port)
	c, r, _ := sse(t, gs.port, cookie)
	defer c.Close()
	event(t, c, r, 5*time.Second) // retry
	event(t, c, r, 5*time.Second) // changed
	req, _ := http.NewRequest("POST", "http://127.0.0.1:"+
		strconv.Itoa(gs.port)+"/api/logout", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Cookie", "fd_"+strconv.Itoa(gs.port)+"="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("logout: %v %v", err, resp)
	}
	resp.Body.Close()
	start := time.Now()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = io.ReadAll(r)
	if err != nil || time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("stream after logout: %v after %v", err,
			time.Since(start))
	}
}

func TestSSETooMany(t *testing.T) {
	g := loadHTTPGolden(t)
	gs := startGoWith(t, g, serverOpts{})
	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < MaxStreams; i++ {
		c, _, _ := sse(t, gs.port, "")
		conns = append(conns, c)
	}
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(gs.port) +
		"/api/events")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 503 ||
		string(b) != `{"error": "Too many open pages."}` {
		t.Fatalf("101st: %d %s", resp.StatusCode, b)
	}
}

// open reports whether a new connection from src is served: a
// request on it gets an answer.
func served(t *testing.T, src string, port int) bool {
	c := dial(t, src, port)
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte("GET /api/session HTTP/1.1\r\nHost: 127.0.0.1:" +
		strconv.Itoa(port) + "\r\n\r\n"))
	line, err := bufio.NewReader(c).ReadString('\n')
	return err == nil && strings.HasPrefix(line, "HTTP/1.1 200")
}

func waitOpen(l net.Listener, n int) bool {
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if Open(l) == n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestCapPerDevice(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("needs 127.0.0.2 as a second device")
	}
	g := loadHTTPGolden(t)
	gs := startGoWith(t, g, serverOpts{lan: true})
	var held []net.Conn
	for i := 0; i < MaxPerDevice; i++ {
		held = append(held, dial(t, "127.0.0.1", gs.port))
	}
	if !waitOpen(gs.listener, MaxPerDevice) {
		t.Fatalf("open %d", Open(gs.listener))
	}
	// The 33rd from one device is closed at once.
	if served(t, "127.0.0.1", gs.port) {
		t.Fatal("33rd connection from one device was served")
	}
	// Another device still gets in.
	if !served(t, "127.0.0.2", gs.port) {
		t.Fatal("another device was kept out")
	}
	// A slot given back is free again, exactly once.
	held[0].Close()
	held = held[1:]
	if !waitOpen(gs.listener, MaxPerDevice-1) {
		t.Fatalf("open %d after a close", Open(gs.listener))
	}
	if !served(t, "127.0.0.1", gs.port) {
		t.Fatal("a freed slot was not given back")
	}
	for _, c := range held {
		c.Close()
	}
	if !waitOpen(gs.listener, 0) {
		t.Fatalf("open %d at the end", Open(gs.listener))
	}
}

func TestCapAll(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	capped := Cap(l, 3, 0)
	defer capped.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})}
	go srv.Serve(capped)
	defer srv.Close()
	port := l.Addr().(*net.TCPAddr).Port
	var held []net.Conn
	for i := 0; i < 3; i++ {
		held = append(held, dial(t, "127.0.0.1", port))
	}
	if !waitOpen(capped, 3) {
		t.Fatalf("open %d", Open(capped))
	}
	c := dial(t, "127.0.0.1", port)
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if n, err := c.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatal("a connection past the cap was kept")
	}
	c.Close()
	for _, h := range held {
		h.Close()
	}
	if !waitOpen(capped, 0) {
		t.Fatalf("open %d after closing", Open(capped))
	}
}

func TestCap256(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs more open files than macOS allows by default")
	}
	g := loadHTTPGolden(t)
	gs := startGoWith(t, g, serverOpts{})
	var held []net.Conn
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()
	for i := 0; i < MaxConnections; i++ {
		held = append(held, dial(t, "127.0.0.1", gs.port))
	}
	if !waitOpen(gs.listener, MaxConnections) {
		t.Fatalf("open %d", Open(gs.listener))
	}
	if served(t, "127.0.0.1", gs.port) {
		t.Fatal("the 257th connection was served")
	}
	held[0].Close()
	if !waitOpen(gs.listener, MaxConnections-1) || !served(t,
		"127.0.0.1", gs.port) {
		t.Fatal("a freed slot was not given back")
	}
}

// A Cookie header with other apps' malformed cookies still finds
// Front Desk's (ledger F-8; Python's SimpleCookie gave up).
func TestCookieAmongOthers(t *testing.T) {
	g := loadHTTPGolden(t)
	gs := startGoWith(t, g, serverOpts{login: true})
	cookie, _ := login(t, gs.port)
	for _, h := range []string{
		`other="unclosed; fd_` + strconv.Itoa(gs.port) + "=" + cookie,
		`a b=c; fd_` + strconv.Itoa(gs.port) + "=" + cookie + `; x=[y]`,
	} {
		req, _ := http.NewRequest("GET", "http://127.0.0.1:"+
			strconv.Itoa(gs.port)+"/api/state", nil)
		req.Header.Set("Cookie", h)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%q: %d", h, resp.StatusCode)
		}
	}
}

func post(t *testing.T, port int, path, body, csrf string) (int,
	string) {
	t.Helper()
	req, _ := http.NewRequest("POST", "http://127.0.0.1:"+
		strconv.Itoa(port)+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !resp.Close && resp.Header.Get("Connection") != "close" {
		t.Errorf("POST answer without Connection: close")
	}
	return resp.StatusCode, string(b)
}

func session(t *testing.T, port int) string {
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) +
		"/api/session")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var j struct{ CSRF string }
	json.NewDecoder(resp.Body).Decode(&j)
	return j.CSRF
}

// mint-key that never finishes: the 500 path, as Python's
// TimeoutExpired, with a log line and nothing secret in it.
func TestMintLate(t *testing.T) {
	old := mintWait
	mintWait = 200 * time.Millisecond
	defer func() { mintWait = old }()
	g := loadHTTPGolden(t)
	gs := startGoWith(t, g, serverOpts{mint: func(ctx context.Context,
		_ *seats.File, _ []string, _ time.Duration) mintkey.Result {
		<-ctx.Done()
		return mintkey.Result{Code: 1, Stderr: "late"}
	}})
	csrf := session(t, gs.port)
	code, body := post(t, gs.port, "/api/mint", `{"seat": "made-ok", `+
		`"models": ["local"], "budget": "1", "duration": "1d"}`, csrf)
	want := `{"error": "Something went wrong at the desk. See the ` +
		`terminal where Front Desk runs."}`
	if code != 500 || body != want {
		t.Fatalf("%d %s", code, body)
	}
	logs := gs.logs.take()
	if len(logs) != 1 || logs[0] != "error: mint-key did not finish "+
		"in 30 seconds" {
		t.Fatalf("log %q", logs)
	}
}

// A panic in a handler: 500 with Python's words, logged scrubbed.
func TestPanicIs500(t *testing.T) {
	g := loadHTTPGolden(t)
	gs := startGoWith(t, g, serverOpts{mint: func(context.Context,
		*seats.File, []string, time.Duration) mintkey.Result {
		panic("boom sk-fake-settings-master")
	}})
	csrf := session(t, gs.port)
	code, body := post(t, gs.port, "/api/mint", `{"seat": "made-ok", `+
		`"models": ["local"], "budget": "1", "duration": "1d"}`, csrf)
	if code != 500 || !strings.Contains(body, "See the terminal") {
		t.Fatalf("%d %s", code, body)
	}
	logs := gs.logs.take()
	if len(logs) != 1 || logs[0] != "error: boom [hidden]" {
		t.Fatalf("log %q", logs)
	}
}

// Requests are never logged, odd ones neither.
func TestNoRequestLog(t *testing.T) {
	g := loadHTTPGolden(t)
	gs := startGoWith(t, g, serverOpts{})
	time.Sleep(200 * time.Millisecond)
	gs.logs.take()
	for _, raw := range []string{"GARBAGE\r\n\r\n",
		"GET / HTTP/2.0\r\n\r\n",
		"GET /\x01 HTTP/1.1\r\nHost: x\r\n\r\n",
		"GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\n\r\n",
		"POST / HTTP/1.1\r\nHost: 127.0.0.1\r\n" +
			"Content-Length: x\r\n\r\n"} {
		c := dial(t, "127.0.0.1", gs.port)
		c.Write([]byte(raw))
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		io.ReadAll(c)
		c.Close()
	}
	if logs := gs.logs.take(); len(logs) != 0 {
		t.Fatalf("logged %q", logs)
	}
}

// slowWrites shortens the write wait, so a stand-in mint-key slower
// than it shows that the clock starts at the answer, not at the
// request (Python's timeout counts per write too).
func slowWrites(t *testing.T) time.Duration {
	old := writeWait
	writeWait = 300 * time.Millisecond
	t.Cleanup(func() { writeWait = old })
	return 4 * writeWait
}

// A mint or revoke slower than the write wait still gets its answer:
// the minted key is shown nowhere else.
func TestSlowMintAnswers(t *testing.T) {
	g := loadHTTPGolden(t)
	slow := slowWrites(t)
	gs := startGoWith(t, g, serverOpts{mint: func(ctx context.Context,
		_ *seats.File, args []string, _ time.Duration) mintkey.Result {
		time.Sleep(slow)
		if args[0] == "--revoke-seat" {
			return mintkey.Result{Code: 0, Stdout: "revoked\n"}
		}
		return mintkey.Result{Code: 0,
			Stdout: "sk-slowslowslow1234\n"}
	}})
	csrf := session(t, gs.port)
	code, body := post(t, gs.port, "/api/mint", `{"seat": "slow-one", `+
		`"models": ["local"], "budget": "1", "duration": "1d"}`, csrf)
	if code != 200 || !strings.Contains(body,
		`"key": "sk-slowslowslow1234"`) {
		t.Fatalf("mint: %d %s", code, body)
	}
	code, body = post(t, gs.port, "/api/revoke", `{"seat": "Sam"}`,
		csrf)
	if code != 200 || body != `{"ok": true, "seat": "Sam"}` {
		t.Fatalf("revoke: %d %s", code, body)
	}
	logs := strings.Join(gs.logs.take(), "\n")
	for _, want := range []string{"minted a key card for seat slow-one",
		"cancelled the key card for seat Sam"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log %q lacks %q", logs, want)
		}
	}
}

// The late-mint 500 comes after MintWait, past the write wait too.
func TestMintLateAfterWriteWait(t *testing.T) {
	slow := slowWrites(t)
	old := mintWait
	mintWait = slow
	defer func() { mintWait = old }()
	g := loadHTTPGolden(t)
	gs := startGoWith(t, g, serverOpts{mint: func(ctx context.Context,
		_ *seats.File, _ []string, _ time.Duration) mintkey.Result {
		<-ctx.Done()
		return mintkey.Result{Code: 1, Stderr: "late"}
	}})
	csrf := session(t, gs.port)
	code, body := post(t, gs.port, "/api/mint", `{"seat": "made-ok", `+
		`"models": ["local"], "budget": "1", "duration": "1d"}`, csrf)
	if code != 500 || !strings.Contains(body, "See the terminal") {
		t.Fatalf("%d %s", code, body)
	}
}

// A viewer who hangs up mid-mint or mid-revoke does not stop it: as
// Python's subprocess.run, mint-key runs to its end.
func TestHangUpFinishesMint(t *testing.T) {
	g := loadHTTPGolden(t)
	cancelled := make(chan bool, 2)
	gs := startGoWith(t, g, serverOpts{mint: func(ctx context.Context,
		_ *seats.File, args []string, _ time.Duration) mintkey.Result {
		time.Sleep(500 * time.Millisecond)
		cancelled <- ctx.Err() != nil
		if args[0] == "--revoke-seat" {
			return mintkey.Result{Code: 0}
		}
		return mintkey.Result{Code: 0, Stdout: "sk-hangupkey12345\n"}
	}})
	csrf := session(t, gs.port)
	for _, c := range []struct{ path, body, log string }{
		{"/api/mint", `{"seat": "hang-up", "models": ["local"], ` +
			`"budget": "1", "duration": "1d"}`,
			"minted a key card for seat hang-up"},
		{"/api/revoke", `{"seat": "Sam"}`,
			"cancelled the key card for seat Sam"},
	} {
		gs.logs.take()
		conn := dial(t, "127.0.0.1", gs.port)
		conn.Write([]byte("POST " + c.path + " HTTP/1.1\r\nHost: " +
			"127.0.0.1:" + strconv.Itoa(gs.port) + "\r\n" +
			"Content-Type: application/json\r\nX-CSRF-Token: " +
			csrf + "\r\nContent-Length: " +
			strconv.Itoa(len(c.body)) + "\r\n\r\n" + c.body))
		time.Sleep(100 * time.Millisecond)
		conn.Close()
		select {
		case was := <-cancelled:
			if was {
				t.Errorf("%s: mint-key's context ended when "+
					"the viewer left", c.path)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: mint-key never ran", c.path)
		}
		end := time.Now().Add(5 * time.Second)
		var logs []string
		for time.Now().Before(end) {
			logs = append(logs, gs.logs.take()...)
			if strings.Contains(strings.Join(logs, "\n"), c.log) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !strings.Contains(strings.Join(logs, "\n"), c.log) {
			t.Errorf("%s: log %q, want %q", c.path, logs, c.log)
		}
	}
}
