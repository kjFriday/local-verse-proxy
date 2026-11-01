package litellm

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNewWithAndRaw(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			if r.URL.RequestURI() == "/big" {
				w.Write([]byte(strings.Repeat("x", 3000)))
				return
			}
			w.WriteHeader(418)
			w.Write([]byte(`{"a":1}`))
		}))
	defer srv.Close()
	c, err := NewWith(Options{BaseURL: srv.URL, MasterKey: "sk-master-1",
		MaxAnswer: 1000})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, body, err := c.Raw(ctx, "/x?y=1", false)
	if err != nil || st != 418 || string(body) != `{"a":1}` {
		t.Fatalf("Raw: %d %q %v", st, body, err)
	}
	if got.Get("Authorization") != "" || got.Get("User-Agent") != "" {
		t.Errorf("no auth, no agent: %v", got)
	}
	c.Raw(ctx, "/x", true)
	if got.Get("Authorization") != "Bearer sk-master-1" {
		t.Errorf("auth: %v", got)
	}
	_, body, err = c.Raw(ctx, "/big", true)
	var tb *TooBigError
	if len(body) != 1000 || !errors.As(err, &tb) ||
		err.Error() != "the proxy's answer is over 1000 bytes" {
		t.Errorf("MaxAnswer: read %d bytes, %v", len(body), err)
	}
	srvExact := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(strings.Repeat("y", 1000)))
		}))
	defer srvExact.Close()
	ce, _ := NewWith(Options{BaseURL: srvExact.URL, MasterKey: "k",
		MaxAnswer: 1000})
	if _, body, err := ce.Raw(ctx, "/", false); err != nil ||
		len(body) != 1000 {
		t.Errorf("an answer of exactly MaxAnswer: %d %v", len(body),
			err)
	}
	n, _ := New(srv.URL, "sk-master-1", 0)
	n.Raw(ctx, "/x", true)
	if got.Get("User-Agent") != "local-verse-proxy" {
		t.Errorf("New's agent: %q", got.Get("User-Agent"))
	}
	if _, err := NewWith(Options{BaseURL: "ftp://x",
		MasterKey: "k"}); err == nil {
		t.Error("ftp")
	}
}

// A transport error keeps what it wraps, for the caller's names,
// with the master key cut out of its text.
func TestRawError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	master := "sk-master-secret-1"
	c, _ := NewWith(Options{BaseURL: "http://" + addr, MasterKey: master,
		Timeout: time.Second})
	_, _, err := c.Raw(context.Background(), "/k?key="+master, true)
	if err == nil || strings.Contains(err.Error(), master) {
		t.Fatalf("error %v", err)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) &&
		!strings.Contains(err.Error(), "refused") {
		t.Errorf("not refused underneath: %v", err)
	}
}

// EnvProxy false: no web proxy, whatever the environment says.
func TestNoEnvProxy(t *testing.T) {
	c, _ := NewWith(Options{BaseURL: "http://example.com", MasterKey: "k"})
	tr := c.http.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("a proxy func with EnvProxy false")
	}
	c, _ = NewWith(Options{BaseURL: "http://example.com", MasterKey: "k",
		EnvProxy: true})
	if c.http.Transport.(*http.Transport).Proxy == nil {
		t.Fatal("no proxy func with EnvProxy true")
	}
}

// echoKey is a made-up master key of 51 characters, as README's
// openssl line makes; built here, so no file holds one.
var echoKey = func() string {
	const abc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnoxyz0123456789"
	b := []byte("sk-")
	for i := 0; len(b) < 51; i++ {
		b = append(b, abc[(i*7+3)%len(abc)])
	}
	return string(b)
}()

// leaked is the longest run of 5 or more of the key's characters
// that text shows (Mask shows "sk-" and the last 4).
func leaked(text, key string) string {
	for n := len(key); n >= 5; n-- {
		for i := 0; i+n <= len(key); i++ {
			if part := key[i : i+n]; strings.Contains(text, part) {
				return part
			}
		}
	}
	return ""
}

// A server that echoes the request (a mistake in PROXY_URL, or a
// hostile one) never gets the master key shown: not cut by the 300
// characters an answer shows, nor by the most a Client reads.
func TestEchoedKeyIsHidden(t *testing.T) {
	echo := func(pad, status int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			w.WriteHeader(status)
			io.WriteString(w, strings.Repeat("p", pad)+auth+
				strings.Repeat("q", 2000))
		}
	}
	for _, pad := range []int{0, 1, 250, 270, 280, 296, 299, 300,
		1000 - 60, 1000 - 30, 1000 - 13, 1000 - 4} {
		for _, status := range []int{200, 500} {
			srv := httptest.NewServer(echo(pad, status))
			c, _ := NewWith(Options{BaseURL: srv.URL,
				MasterKey: echoKey, MaxAnswer: 1000})
			_, err := c.GenerateKey(context.Background(),
				GenerateRequest{Models: []string{"local"}})
			srv.Close()
			if err == nil {
				t.Fatalf("pad %d: no error", pad)
			}
			if part := leaked(err.Error(), echoKey); part != "" {
				t.Errorf("pad %d, status %d: the error "+
					"shows %d characters of the key", pad,
					status, len(part))
			}
		}
	}
}

// A redirect's Location shows scheme, host and path only, decoded and
// scrubbed, whatever spelling of the key it carries.
func TestRedirectHidesKey(t *testing.T) {
	enc := strings.ReplaceAll(echoKey, "-", "%2D")
	for _, loc := range []string{
		"/ui?t=" + enc,
		"/ui?t=" + echoKey,
		"/" + enc + "/x",
		"http://u:" + echoKey + "@h.example/p#" + echoKey,
		"https://h.example/a%2Fb?x=1",
	} {
		srv := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", loc)
				w.WriteHeader(http.StatusFound)
			}))
		c, _ := NewWith(Options{BaseURL: srv.URL, MasterKey: echoKey})
		err := c.DeleteKeys(context.Background(), "k")
		srv.Close()
		var se *StatusError
		if !errors.As(err, &se) {
			t.Fatalf("%s: %v", loc, err)
		}
		if part := leaked(se.Hint()+se.Location, echoKey); part != "" {
			t.Errorf("%s: shows %q in %q", loc, part, se.Hint())
		}
	}
}
