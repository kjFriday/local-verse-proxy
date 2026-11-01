package game

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// serveOnce answers each connection with do, on a loopback port.
func serveOnce(t *testing.T, do func(c net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				do(c)
			}()
		}
	}()
	return "http://" + ln.Addr().String()
}

// readRequest reads one request with its body.
func readRequest(c net.Conn) (*http.Request, string) {
	r, err := http.ReadRequest(bufio.NewReader(c))
	if err != nil {
		return nil, ""
	}
	b, _ := io.ReadAll(r.Body)
	return r, string(b)
}

func TestChatAnswers(t *testing.T) {
	var got *http.Request
	var gotBody string
	answer := ""
	url := serveOnce(t, func(c net.Conn) {
		got, gotBody = readRequest(c)
		io.WriteString(c, answer)
	})
	cases := []struct {
		name, answer string
		status       int
		kind         string
	}{
		{"ok", "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}", 200,
			""},
		{"ok-stream", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked" +
			"\r\n\r\n5\r\ndata:\r\n0\r\n\r\n", 200, ""},
		{"budget", "HTTP/1.1 422 X\r\nContent-Length: 47\r\n\r\n" +
			`{"error": {"type": "budget_exceeded", "x": 1}}` + " ",
			422, "budget_exceeded"},
		{"no-type", "HTTP/1.1 500 X\r\nContent-Length: 12\r\n\r\n" +
			`{"error": 5}`, 500, ""},
		{"null-type", "HTTP/1.1 429 X\r\nContent-Length: 26\r\n\r\n" +
			`{"error": {"type": null}}` + " ", 429, ""},
		{"number-type", "HTTP/1.1 400 X\r\nContent-Length: 23\r\n\r\n" +
			`{"error": {"type": 7}}` + " ", 400, "7"},
		{"html", "HTTP/1.1 502 Bad\r\nContent-Length: 5\r\n\r\n<b>x",
			502,
			""},
		{"redirect", "HTTP/1.1 307 X\r\nLocation: http://e.test/\r\n" +
			"Content-Length: 0\r\n\r\n", 307, ""},
		{"no-answer", "", 0, "RemoteDisconnected"},
		{"cut-short", "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n" +
			"{\"a\"", 0, "IncompleteRead"},
	}
	for _, c := range cases {
		answer = c.answer
		status, kind := Chat(context.Background(), url, "sk-seat",
			[]byte(`{"model": "local"}`))
		if status != c.status || kind != c.kind {
			t.Errorf("%s: %d %q, want %d %q", c.name, status, kind,
				c.status, c.kind)
		}
	}
	h := got.Header
	if got.URL.Path != "/v1/chat/completions" || got.Method != "POST" ||
		h.Get("Authorization") != "Bearer sk-seat" ||
		h.Get("Content-Type") != "application/json" ||
		h.Get("User-Agent") != "lvg-office/1" ||
		h.Get("Accept-Encoding") != "identity" ||
		gotBody != `{"model": "local"}` {
		t.Errorf("request: %s %s %v %q", got.Method, got.URL, h,
			gotBody)
	}
}

func TestChatFailures(t *testing.T) {
	// Nobody listens: refused before anything was sent.
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if s, k := Chat(context.Background(), "http://"+addr, "k",
		nil); s != 0 || k != "URLError" {
		t.Errorf("refused: %d %q", s, k)
	}
	if s, k := Chat(context.Background(), "http://name.invalid", "k",
		nil); s != 0 || k != "URLError" {
		t.Errorf("no such name: %d %q", s, k)
	}
	// A reset after the request.
	url := serveOnce(t, func(c net.Conn) {
		readRequest(c)
		c.(*net.TCPConn).SetLinger(0)
	})
	if s, k := Chat(context.Background(), url, "k", nil); s != 0 ||
		k != "ConnectionResetError" {
		t.Errorf("reset: %d %q", s, k)
	}
	// No answer in time.
	old := chatTimeout
	chatTimeout = 300 * time.Millisecond
	defer func() { chatTimeout = old }()
	stall := make(chan struct{})
	defer close(stall)
	url = serveOnce(t, func(c net.Conn) {
		readRequest(c)
		<-stall
	})
	if s, k := Chat(context.Background(), url, "k", nil); s != 0 ||
		k != "TimeoutError" {
		t.Errorf("stall: %d %q", s, k)
	}
	// A body that stops coming.
	url = serveOnce(t, func(c net.Conn) {
		readRequest(c)
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: "+
			"9\r\n\r\nab")
		<-stall
	})
	if s, k := Chat(context.Background(), url, "k", nil); s != 0 ||
		k != "TimeoutError" {
		t.Errorf("slow body: %d %q", s, k)
	}
}

// TestChatNoProxyForLoopback: a web proxy in the environment is not
// used for the proxy on this machine (ledger O-6).
func TestChatNoProxyForLoopback(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "[::1]",
		"LOCALHOST", "127.9.9.9"} {
		req, _ := http.NewRequest("POST", "http://"+h+":4000/", nil)
		if u, err := proxyFor(req); u != nil || err != nil {
			t.Errorf("%s: proxy %v %v", h, u, err)
		}
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	url := serveOnce(t, func(c net.Conn) {
		readRequest(c)
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: "+
			"0\r\n\r\n")
	})
	if s, _ := Chat(context.Background(), url, "k", nil); s != 200 {
		t.Errorf("with HTTP_PROXY set: %d", s)
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatal(url)
	}
}
