//go:build e2e

// Package perf is the performance A/B of spec 16.6: the reference
// scripts in tests/reference/perf (load.py, mint_ab.py, startup.py,
// sse_bench2.py), ported to Go. Run it against Python's Front Desk
// and Go's on the same machine, one after the other, in one session;
// tests/e2e/perf-results.md has the numbers and how they were taken.
//
//	E2E_PORT=8023 E2E_PID=<desk pid> E2E_PROXY_URL=http://localhost:4223 \
//	  E2E_SEATS_ENV=seats.env E2E_OUT=go.txt \
//	  go test -tags e2e -timeout 30m ./tests/e2e/perf
//
// The desk is a local one (no password). E2E_SECS sets each load
// window (default 10, as load.py). TestSeed makes 200 seats straight
// in LiteLLM first, once (E2E_SEED=1). TestStartup needs E2E_DESK_CMD
// (the command that starts a desk, run in E2E_DESK_DIR, which gets
// --port <a free port> added) and Linux's /proc for memory.
// TestStreams opens E2E_VIEWERS pages (default 20). TestKeepAlive,
// TestBurst and TestStatic are Appendix A's keep-alive rate, 50 page
// loads at once, and the static files' zipped total.
package perf

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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	port int
	base string
	secs = 10.0
	outf *os.File
)

func TestMain(m *testing.M) {
	port, _ = strconv.Atoi(os.Getenv("E2E_PORT"))
	base = fmt.Sprintf("http://127.0.0.1:%d", port)
	if s, err := strconv.ParseFloat(os.Getenv("E2E_SECS"), 64); err ==
		nil && s > 0 {
		secs = s
	}
	if p := os.Getenv("E2E_OUT"); p != "" {
		outf, _ = os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY,
			0o644)
	}
	os.Exit(m.Run())
}

func need(t *testing.T) {
	if port == 0 {
		t.Skip("E2E_PORT is not set: no Front Desk to measure")
	}
}

func say(t *testing.T, format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	t.Log(line)
	if outf != nil {
		fmt.Fprintln(outf, line)
	}
}

// pct is load.py's pct(): median, 95th, max, count, in ms.
func pct(xs []float64) string {
	if len(xs) == 0 {
		return "n=0"
	}
	sort.Float64s(xs)
	n := len(xs)
	med := xs[n/2]
	if n%2 == 0 {
		med = (xs[n/2-1] + xs[n/2]) / 2
	}
	i := int(0.95 * float64(n))
	if i > n-1 {
		i = n - 1
	}
	return fmt.Sprintf("p50 %7.2f p95 %7.2f max %7.2f ms n=%d", med,
		xs[i], xs[n-1], n)
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// client is one keep-alive connection, as http.client's.
func client() *http.Client {
	return &http.Client{Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 1, DisableCompression: true}}
}

func get(c *http.Client, path string, h map[string]string) (*http.Response,
	[]byte, error) {
	req, _ := http.NewRequest("GET", base+path, nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	for k, v := range h {
		req.Header.Set(k, v)
	}
	r, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	return r, b, nil
}

// run is load.py's run(): viewers loops of fn for secs.
func run(viewers int, d float64,
	fn func(ctx map[string]any) float64) []float64 {
	var mu sync.Mutex
	var out []float64
	stop := time.Now().Add(time.Duration(d * float64(time.Second)))
	var wg sync.WaitGroup
	for i := 0; i < viewers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := map[string]any{}
			var local []float64
			for time.Now().Before(stop) {
				local = append(local, fn(ctx))
			}
			mu.Lock()
			out = append(out, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func pageLoad(t *testing.T) float64 {
	t0 := time.Now()
	c := client()
	for _, p := range []string{"/", "/app.css", "/app.js", "/icon.svg",
		"/api/session", "/api/state"} {
		r, _, err := get(c, p, nil)
		if err != nil || r.StatusCode != 200 {
			t.Errorf("%s: %v %v", p, err, r)
			break
		}
	}
	c.CloseIdleConnections()
	return ms(time.Since(t0))
}

func conn(ctx map[string]any) *http.Client {
	if c, ok := ctx["c"].(*http.Client); ok {
		return c
	}
	c := client()
	ctx["c"] = c
	return c
}

func stateReq(t *testing.T) func(map[string]any) float64 {
	return func(ctx map[string]any) float64 {
		c := conn(ctx)
		t0 := time.Now()
		r, _, err := get(c, "/api/state", nil)
		if err != nil || r.StatusCode != 200 {
			t.Errorf("state: %v %v", err, r)
		}
		return ms(time.Since(t0))
	}
}

func state304(ctx map[string]any) float64 {
	c := conn(ctx)
	h := map[string]string{}
	if e, ok := ctx["e"].(string); ok {
		h["If-None-Match"] = e
	}
	t0 := time.Now()
	r, _, err := get(c, "/api/state", h)
	if err == nil {
		ctx["e"] = r.Header.Get("ETag")
	}
	return ms(time.Since(t0))
}

var keyRE = regexp.MustCompile(`^sk-[A-Za-z0-9_-]{8,200}$`)

func csrf(t *testing.T) string {
	_, b, err := get(client(), "/api/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	json.Unmarshal(b, &s)
	c, _ := s["csrf"].(string)
	return c
}

func post(t *testing.T, tok, path string, body any) (int,
	map[string]any, float64) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", tok)
	req.Header.Set("Origin", base)
	c := &http.Client{Timeout: 60 * time.Second}
	t0 := time.Now()
	r, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	rb, _ := io.ReadAll(r.Body)
	r.Body.Close()
	dt := ms(time.Since(t0))
	var j map[string]any
	json.Unmarshal(rb, &j)
	return r.StatusCode, j, dt
}

func mintCycle(t *testing.T, tok string, n int, tag string) ([]float64,
	[]float64) {
	var m, v []float64
	for i := 0; i < n; i++ {
		seat := fmt.Sprintf("perf-%s-%d", tag, i)
		code, j, dt := post(t, tok, "/api/mint", map[string]any{
			"seat": seat, "models": []string{"local"},
			"budget":   "1",
			"duration": "1h"})
		k, _ := j["key"].(string)
		if code != 200 || !keyRE.MatchString(k) {
			t.Fatalf("mint %s: %d %v", seat, code, j["error"])
		}
		m = append(m, dt)
		code, j, dt = post(t, tok, "/api/revoke",
			map[string]any{"seat": seat})
		if code != 200 {
			t.Fatalf("revoke %s: %d %v", seat, code, j)
		}
		v = append(v, dt)
	}
	return m, v
}

// TestLoad is load.py.
func TestLoad(t *testing.T) {
	need(t)
	for _, viewers := range []int{1, 20} {
		say(t, "[%2d viewers] page load (6 GETs)  %s", viewers,
			pct(run(viewers, secs, func(map[string]any) float64 {
				return pageLoad(t)
			})))
		say(t, "[%2d viewers] /api/state 200 gz  %s", viewers,
			pct(run(viewers, secs, stateReq(t))))
		say(t, "[%2d viewers] /api/state 304     %s", viewers,
			pct(run(viewers, secs, state304)))
	}
	tok := csrf(t)
	m, v := mintCycle(t, tok, 15, "a")
	say(t, "[ 0 bg] mint   %s", pct(m))
	say(t, "[ 0 bg] revoke %s", pct(v))
	done := make(chan struct{})
	go func() { run(20, 30, stateReq(t)); close(done) }()
	time.Sleep(time.Second)
	m, v = mintCycle(t, tok, 15, "b")
	say(t, "[20 bg] mint   %s", pct(m))
	say(t, "[20 bg] revoke %s", pct(v))
	<-done
}

// TestMintAB is mint_ab.py: times, and the page right at once.
func TestMintAB(t *testing.T) {
	need(t)
	tok := csrf(t)
	seats := func() map[string]bool {
		_, b, err := get(client(), "/api/state", nil)
		if err != nil {
			t.Fatal(err)
		}
		if zr, err := gzip.NewReader(bytes.NewReader(b)); err == nil {
			b, _ = io.ReadAll(zr)
		}
		var s struct {
			Cards []struct{ Seat string } `json:"cards"`
		}
		json.Unmarshal(b, &s)
		out := map[string]bool{}
		for _, c := range s.Cards {
			out[c.Seat] = true
		}
		return out
	}
	var m, v []float64
	bad := 0
	for i := 0; i < 20; i++ {
		seat := fmt.Sprintf("ab-%d-%d", os.Getpid(), i)
		code, j, dt := post(t, tok, "/api/mint", map[string]any{
			"seat": seat, "models": []string{"local"},
			"budget":   "1",
			"duration": "1h"})
		if k, _ := j["key"].(string); code != 200 || !keyRE.MatchString(
			k) {
			t.Fatalf("mint: %d %v", code, j["error"])
		}
		m = append(m, dt)
		if !seats()[seat] {
			bad++
		}
		code, _, dt = post(t, tok, "/api/revoke",
			map[string]any{"seat": seat})
		if code != 200 {
			t.Fatalf("revoke: %d", code)
		}
		v = append(v, dt)
		if seats()[seat] {
			bad++
		}
		time.Sleep(370 * time.Millisecond)
	}
	say(t, "port %d mint   %s", port, pct(m))
	say(t, "port %d revoke %s", port, pct(v))
	say(t, "port %d state wrong right after mint/revoke: %d of 40", port,
		bad)
	if bad != 0 {
		t.Errorf("state wrong %d of 40", bad)
	}
}

func rss(pid int) float64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return -1
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "VmRSS:"); ok {
			kb, _ := strconv.Atoi(strings.Fields(v)[0])
			return float64(kb) / 1024
		}
	}
	return -1
}

func cpu(pid int) float64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndex(s, ")")+1:])
	u, _ := strconv.Atoi(f[11])
	k, _ := strconv.Atoi(f[12])
	return float64(u+k) / 100 // USER_HZ is 100 on Linux
}

// TestStartup is startup.py: exec to first page and first state, and
// memory 5 s later, five times.
func TestStartup(t *testing.T) {
	cmdline := os.Getenv("E2E_DESK_CMD")
	if cmdline == "" {
		t.Skip("E2E_DESK_CMD is not set")
	}
	var page, first, mem []float64
	for i := 0; i < 5; i++ {
		ln, _ := net.Listen("tcp4", "127.0.0.1:0")
		p := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		f := strings.Fields(cmdline)
		cmd := exec.Command(f[0], append(f[1:], "--port",
			strconv.Itoa(p))...)
		cmd.Dir = os.Getenv("E2E_DESK_DIR")
		t0 := time.Now()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		u := fmt.Sprintf("http://127.0.0.1:%d", p)
		c := &http.Client{Timeout: 5 * time.Second}
		for {
			r, err := c.Get(u + "/")
			if err == nil {
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
				if r.StatusCode == 200 {
					break
				}
			}
			if time.Since(t0) > 20*time.Second {
				t.Fatal("no page in 20 s")
			}
			time.Sleep(5 * time.Millisecond)
		}
		page = append(page, ms(time.Since(t0)))
		r, err := c.Get(u + "/api/state")
		if err == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}
		first = append(first, ms(time.Since(t0)))
		time.Sleep(5 * time.Second)
		mem = append(mem, rss(cmd.Process.Pid))
		cmd.Process.Signal(os.Interrupt)
		cmd.Wait()
		time.Sleep(500 * time.Millisecond)
	}
	med := func(x []float64) float64 {
		sort.Float64s(x)
		return x[len(x)/2]
	}
	say(t, "exec -> first page p50 %.0f ms (%.0f-%.0f); exec -> first "+
		"state p50 %.0f ms (%.0f-%.0f); RSS 5 s later p50 %.1f MB",
		med(page), page[0], page[len(page)-1], med(first), first[0],
		first[len(first)-1], med(mem))
}

// meter counts what the viewers moved, for one window.
type meter struct {
	down, up, events, fetches, chats atomic.Int64
}

func (m *meter) snap() [5]int64 {
	return [5]int64{m.down.Load(), m.up.Load(), m.events.Load(),
		m.fetches.Load(), m.chats.Load()}
}

// TestStreams is sse_bench2.py: 20 viewers that read the live stream
// and fetch the state on each change, idle and with chat traffic:
// the desk's CPU and memory, and bytes a viewer gets an hour. As in
// the reference, only what moves inside the window counts (not each
// viewer's first state).
func TestStreams(t *testing.T) {
	need(t)
	pid, _ := strconv.Atoi(os.Getenv("E2E_PID"))
	if pid == 0 {
		t.Skip("E2E_PID is not set")
	}
	viewers := 20
	if n, err := strconv.Atoi(os.Getenv("E2E_VIEWERS")); err == nil &&
		n > 0 {
		viewers = n
	}
	for _, traffic := range []bool{false, true} {
		d := 45.0
		if traffic {
			d = 90
		}
		if s, err := strconv.ParseFloat(os.Getenv("E2E_SSE_SECS"),
			64); err == nil && s > 0 {
			d = s
		}
		stop := make(chan struct{})
		m := &meter{}
		var wg sync.WaitGroup
		for i := 0; i < viewers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				viewer(stop, m)
			}()
		}
		if traffic {
			wg.Add(1)
			go func() {
				defer wg.Done()
				chat(stop, m)
			}()
		}
		time.Sleep(5 * time.Second)
		a, c0, t0 := m.snap(), cpu(pid), time.Now()
		time.Sleep(time.Duration(d * float64(time.Second)))
		el := time.Since(t0).Seconds()
		used := (cpu(pid) - c0) / el * 100
		b, mem := m.snap(), rss(pid)
		close(stop)
		wg.Wait()
		hours := el / 3600
		perView := func(i int) float64 {
			return float64(b[i]-a[i]) / float64(viewers) / hours /
				1e6
		}
		say(t, "x%d traffic=%v %.0fs: FD CPU %.2f%% core, RSS "+
			"%.1f MB | per viewer: down %.2f MB/h, up %.2f MB/h, "+
			"events %.1f, fetches %.1f in window; chat calls %d",
			viewers, traffic, d, used, mem, perView(0), perView(1),
			float64(b[2]-a[2])/float64(viewers),
			float64(b[3]-a[3])/float64(viewers), b[4]-a[4])
	}
}

// viewer reads /api/events like the page: on "changed" it fetches the
// state (gzip, If-None-Match). Counts wire bytes.
func viewer(stop chan struct{}, m *meter) {
	c, err := net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return
	}
	go func() { <-stop; c.Close() }()
	req := fmt.Sprintf("GET /api/events HTTP/1.1\r\nHost: 127.0.0.1:%d"+
		"\r\n\r\n", port)
	io.WriteString(c, req)
	m.up.Add(int64(len(req)))
	// The page's fetches go on a keep-alive connection; when the desk
	// closes it, a new one is opened, as a browser does.
	var fc net.Conn
	var fr *bufio.Reader
	redial := func() bool {
		if fc != nil {
			fc.Close()
		}
		fc, err = net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return false
		}
		f := fc
		go func() { <-stop; f.Close() }()
		fr = bufio.NewReader(fc)
		return true
	}
	if !redial() {
		return
	}
	etag := ""
	br := bufio.NewReader(c)
	for {
		line, err := br.ReadString('\n')
		m.down.Add(int64(len(line)))
		if err != nil {
			return
		}
		if !strings.HasPrefix(line, "event: changed") {
			continue
		}
		m.events.Add(1)
		r := fmt.Sprintf("GET /api/state HTTP/1.1\r\nHost: "+
			"127.0.0.1:%d\r\nAccept-Encoding: gzip\r\n", port)
		if etag != "" {
			r += "If-None-Match: " + etag + "\r\n"
		}
		r += "\r\n"
		var resp *http.Response
		for try := 0; try < 2; try++ {
			io.WriteString(fc, r)
			m.up.Add(int64(len(r)))
			resp, err = http.ReadResponse(fr, nil)
			if err == nil || !redial() {
				break
			}
		}
		if err != nil {
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var hdr bytes.Buffer
		resp.Header.Write(&hdr)
		m.down.Add(int64(len(b) + hdr.Len() + 20))
		if e := resp.Header.Get("ETag"); e != "" {
			etag = e
		}
		m.fetches.Add(1)
	}
}

// chat sends chat calls through seats TestSeed made, until stop.
func chat(stop chan struct{}, m *meter) {
	keys := seededKeys()
	if len(keys) == 0 || os.Getenv("E2E_PROXY_URL") == "" {
		return
	}
	c := &http.Client{Timeout: 30 * time.Second}
	for i := 0; ; i++ {
		select {
		case <-stop:
			return
		default:
		}
		b := `{"model": "local", "messages": [{"role": "user", ` +
			`"content": "hi"}]}`
		req, _ := http.NewRequest("POST", os.Getenv("E2E_PROXY_URL")+
			"/v1/chat/completions", strings.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+keys[i%len(keys)])
		req.Header.Set("Content-Type", "application/json")
		if r, err := c.Do(req); err == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			m.chats.Add(1)
		}
	}
}

var seedFile = os.Getenv("E2E_KEYS_FILE")

func seededKeys() []string {
	if seedFile == "" {
		return nil
	}
	b, err := os.ReadFile(seedFile)
	if err != nil {
		return nil
	}
	var m map[string]string
	json.Unmarshal(b, &m)
	var out []string
	for _, k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestSeed makes 200 seats straight in LiteLLM (seed.py made them
// with mint-key.sh), their keys in an owner-only file E2E_KEYS_FILE.
func TestSeed(t *testing.T) {
	if os.Getenv("E2E_SEED") != "1" || seedFile == "" {
		t.Skip("E2E_SEED=1 and E2E_KEYS_FILE make the 200 seats")
	}
	master := ""
	b, _ := os.ReadFile(os.Getenv("E2E_SEATS_ENV"))
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "LITELLM_MASTER_KEY="); ok {
			master = strings.TrimSpace(v)
		}
	}
	keys := map[string]string{}
	var mu sync.Mutex
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			seat := fmt.Sprintf("seat%03d", i)
			body, _ := json.Marshal(map[string]any{
				"key_alias": seat, "models": []string{"local"},
				"max_budget": 50, "duration": "30d"})
			u := os.Getenv("E2E_PROXY_URL") + "/key/generate"
			req, _ := http.NewRequest("POST", u,
				bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+master)
			req.Header.Set("Content-Type", "application/json")
			r, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			var j map[string]any
			json.NewDecoder(r.Body).Decode(&j)
			r.Body.Close()
			if k, _ := j["key"].(string); keyRE.MatchString(k) {
				mu.Lock()
				keys[seat] = k
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	out, _ := json.Marshal(keys)
	if err := os.WriteFile(seedFile, out, 0o600); err != nil {
		t.Fatal(err)
	}
	say(t, "seeded %d seats", len(keys))
}

// TestKeepAlive is Appendix A's keep-alive requests a second: 8
// clients, each on one kept connection, for secs, asking for a static
// file (zipped, as a browser asks) and for the state with its ETag
// (a 304), as the prototype was measured.
func TestKeepAlive(t *testing.T) {
	need(t)
	for _, c := range []struct {
		name string
		fn   func(map[string]any) float64
	}{
		{"/app.css gzip", func(ctx map[string]any) float64 {
			r, _, err := get(conn(ctx), "/app.css", nil)
			if err != nil || r.StatusCode != 200 {
				t.Errorf("app.css: %v %v", err, r)
			}
			return 0
		}},
		{"/api/state 304", state304},
	} {
		n := len(run(8, secs, c.fn))
		say(t, "[ 8 clients] keep-alive %-14s %8.0f a second", c.name,
			float64(n)/secs)
	}
}

// TestBurst is Appendix A's 50 page loads at once: 50 browsers each
// load the page (6 GETs on a new connection) at the same moment;
// the worst of them, five rounds. A SYN stall would show as a
// second or more.
func TestBurst(t *testing.T) {
	need(t)
	var worst []float64
	for round := 0; round < 5; round++ {
		var mu sync.Mutex
		var xs []float64
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				x := pageLoad(t)
				mu.Lock()
				xs = append(xs, x)
				mu.Unlock()
			}()
		}
		time.Sleep(200 * time.Millisecond)
		close(start)
		wg.Wait()
		sort.Float64s(xs)
		worst = append(worst, xs[len(xs)-1])
		say(t, "[50 at once] round %d page load %s", round+1, pct(xs))
		time.Sleep(time.Second)
	}
	sort.Float64s(worst)
	say(t, "[50 at once] worst of 5 rounds %.1f ms", worst[len(worst)-1])
	if worst[len(worst)-1] >= 1000 {
		t.Errorf("a page load took %.0f ms: a stall",
			worst[len(worst)-1])
	}
}

// TestStatic is Appendix A's static files zipped in total: the bytes
// of each file's body as a browser gets it (gzip asked).
func TestStatic(t *testing.T) {
	need(t)
	total := 0
	for _, p := range []string{"/", "/app.css", "/app.js", "/icon.svg"} {
		r, b, err := get(client(), p, nil)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("%s: %v %v", p, err, r)
		}
		say(t, "static %-9s %6d bytes, %s", p, len(b),
			r.Header.Get("Content-Encoding"))
		total += len(b)
	}
	say(t, "static files zipped, total %d bytes", total)
}
