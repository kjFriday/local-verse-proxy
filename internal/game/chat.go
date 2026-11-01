package game

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// ChatFunc sends one chat call with a seat key and gives the HTTP
// status and, for a refusal, LiteLLM's error.type; a call that got no
// answer gives status 0 and the kind of failure.
type ChatFunc func(ctx context.Context, url, key string,
	body []byte) (status int, kind string)

// chatTimeout is urllib's timeout=60: for the connect and for each
// read and write on the socket, not for the whole call.
var chatTimeout = 60 * time.Second

// deadlineConn gives every read and write its own deadline, as a
// Python socket timeout does.
type deadlineConn struct{ net.Conn }

func (c deadlineConn) Read(p []byte) (int, error) {
	c.Conn.SetReadDeadline(time.Now().Add(chatTimeout))
	return c.Conn.Read(p)
}

func (c deadlineConn) Write(p []byte) (int, error) {
	c.Conn.SetWriteDeadline(time.Now().Add(chatTimeout))
	return c.Conn.Write(p)
}

// proxyFor is the environment's web proxy, never for localhost or a
// loopback address (ledger O-6).
func proxyFor(req *http.Request) (*url.URL, error) {
	h := req.URL.Hostname()
	if strings.EqualFold(h, "localhost") || pyfmt.IsLoopback(h) {
		return nil, nil
	}
	return http.ProxyFromEnvironment(req)
}

var chatClient = &http.Client{
	Transport: &http.Transport{
		Proxy: proxyFor,
		DialContext: func(ctx context.Context, network,
			addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: chatTimeout}
			c, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return deadlineConn{c}, nil
		},
		DisableKeepAlives:  true, // urllib: Connection: close
		DisableCompression: true,
		ForceAttemptHTTP2:  false,
	},
	// urllib would follow some redirects; the proxy never sends one,
	// and a seat key must not go elsewhere.
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Chat is office.py's chat(): POST <url>/v1/chat/completions with the
// seat key, read to the end. Failures keep the names urllib's errors
// had: URLError before the request was sent; after it, TimeoutError,
// RemoteDisconnected (closed before an answer), ConnectionResetError,
// IncompleteRead (an answer cut short; ledger O-3).
func Chat(ctx context.Context, base, key string, body []byte) (int,
	string) {
	req, err := http.NewRequestWithContext(ctx, "POST",
		base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, "URLError"
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "lvg-office/1")
	req.Header.Set("Accept-Encoding", "identity")
	var wrote atomic.Bool
	trace := &httptrace.ClientTrace{
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err == nil {
				wrote.Store(true)
			}
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(ctx, trace))
	resp, err := chatClient.Do(req)
	if err != nil {
		if !wrote.Load() {
			return 0, "URLError"
		}
		return 0, failure(err, "RemoteDisconnected")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err != nil {
			return 0, failure(err, "IncompleteRead")
		}
		return resp.StatusCode, ""
	}
	// Never the message: it names the key.
	return resp.StatusCode, errorType(data)
}

// failure names an error that came after the request was sent.
func failure(err error, other string) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return "TimeoutError"
	case isReset(err):
		return "ConnectionResetError"
	}
	return other
}

// errorType is str(json.loads(body)["error"]["type"] or ""), or ""
// where Python's lookup failed.
func errorType(body []byte) string {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || dec.More() {
		return ""
	}
	top, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	e, ok := top["error"].(map[string]any)
	if !ok {
		return ""
	}
	t, ok := e["type"]
	if !ok || !truthy(t) {
		return ""
	}
	return str(t)
}
