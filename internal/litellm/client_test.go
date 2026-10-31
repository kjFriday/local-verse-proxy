package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const master = "sk-master-0123456789abcdef"

func TestMessage(t *testing.T) {
	cases := []struct{ body, want string }{
		{"", ""},
		{"  \n", ""},
		{`{"error":{"message":"Key with alias 'a' already exists."}}`,
			"Key with alias 'a' already exists."},
		{`{"error":{"message":{"z":1,"a":[]}}}`,
			"{\n  \"z\": 1,\n  \"a\": []\n}"},
		{`{"detail":"Not Found"}`, "Not Found"},
		{`{"detail":[{"loc":["body"],"msg":"x"}]}`,
			"[\n  {\n    \"loc\": [\n      \"body\"\n    ],\n" +
				"    \"msg\": \"x\"\n  }\n]"},
		{`{"error":{"message":null},"detail":"d"}`, "d"},
		{`{"error":{"message":false},"detail":"d"}`, "d"},
		{`{"error":null,"detail":"d"}`, "d"},
		{`{"other":1}`, "{\n  \"other\": 1\n}"},
		// jq stops with an error on these; the script prints them raw.
		{`{"error":"flat"}`, `{"error":"flat"}`},
		{`{"error":false,"detail":"d"}`,
			`{"error":false,"detail":"d"}`},
		{`["a"]`, `["a"]`},
		{`"text"`, `"text"`},
		{`null`, `null`},
		{"Internal Server Error\n", "Internal Server Error"},
		{"<html>oops", "<html>oops"},
	}
	for _, c := range cases {
		if got := Message([]byte(c.body)); got != c.want {
			t.Errorf("%q:\n got %q\nwant %q", c.body, got, c.want)
		}
	}
}

func TestParseBase(t *testing.T) {
	good := map[string]string{
		"http://localhost:4000":       "http://localhost:4000",
		"http://localhost:4000/":      "http://localhost:4000",
		"localhost:4000":              "http://localhost:4000",
		" 192.168.1.20:4000 ":         "http://192.168.1.20:4000",
		"HTTPS://proxy.lan":           "https://proxy.lan",
		"http://proxy.lan/litellm//":  "http://proxy.lan/litellm",
		"http://user:pw@proxy.lan:80": "http://user:pw@proxy.lan:80",
	}
	for in, want := range good {
		u, err := ParseBase(in)
		if err != nil || u.String() != want {
			t.Errorf("%q: got %v, %v; want %s", in, u, err, want)
		}
	}
	for _, in := range []string{"ftp://x", "http://", "http://x?a=1",
		"http://x#f", "http://[::1", ""} {
		_, err := ParseBase(in)
		if err == nil || !strings.HasPrefix(err.Error(),
			"is not a web address") {
			t.Errorf("%q: got %v", in, err)
		}
	}
	_, err := ParseBase("http://user:secret@[bad")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("the address is shown: %v", err)
	}
}

type seen struct {
	method, path, auth, ctype, accept, ua string
	body                                  map[string]any
}

func fake(t *testing.T, h func(w http.ResponseWriter, r *seen)) (
	*httptest.Server, *[]seen) {
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			s := seen{method: r.Method, path: r.URL.RequestURI(),
				auth:   r.Header.Get("Authorization"),
				ctype:  r.Header.Get("Content-Type"),
				accept: r.Header.Get("Accept"),
				ua:     r.Header.Get("User-Agent")}
			b, _ := io.ReadAll(r.Body)
			err := json.Unmarshal(b, &s.body)
			if len(b) > 0 && err != nil {
				t.Errorf("body is not JSON: %q", b)
			}
			got = append(got, s)
			h(w, &s)
		}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func newClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := New(base, master, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.UserAgent = "test/1"
	return c
}

func TestGenerateAndDelete(t *testing.T) {
	srv, got := fake(t, func(w http.ResponseWriter, r *seen) {
		if r.path == "/litellm/key/generate" {
			io.WriteString(w, `{"key":"sk-new","key_alias":"a"}`)
			return
		}
		io.WriteString(w, `{"deleted_keys":["x"]}`)
	})
	c := newClient(t, srv.URL+"/litellm/")
	ctx := context.Background()
	key, err := c.GenerateKey(ctx, GenerateRequest{KeyAlias: "a",
		Models: []string{"local"}, MaxBudget: 2.5, Duration: "1h",
		Metadata: Metadata{Seat: "a"}})
	if key != "sk-new" || err != nil {
		t.Fatalf("got %q, %v", key, err)
	}
	if err := c.DeleteKeys(ctx, "sk-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSeats(ctx, "a", "b"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"duration":"1h","key_alias":"a","max_budget":2.5,` +
			`"metadata":{"seat":"a"},"models":["local"]}`,
		`{"keys":["sk-1"]}`,
		`{"key_aliases":["a","b"]}`,
	}
	paths := []string{"/litellm/key/generate", "/litellm/key/delete",
		"/litellm/key/delete"}
	for i, s := range *got {
		b, _ := json.Marshal(s.body)
		if string(b) != want[i] || s.path != paths[i] ||
			s.method != "POST" || s.auth != "Bearer "+master ||
			s.ctype != "application/json" ||
			s.accept != "application/json" || s.ua != "test/1" {
			t.Errorf("request %d: %+v %s", i, s, b)
		}
	}
	if c.BaseURL() != srv.URL+"/litellm" {
		t.Errorf("BaseURL: %s", c.BaseURL())
	}
}

func TestGenerateRefusesNoModels(t *testing.T) {
	srv, got := fake(t, func(w http.ResponseWriter, r *seen) {})
	_, err := newClient(t, srv.URL).GenerateKey(context.Background(),
		GenerateRequest{KeyAlias: "a"})
	if err == nil || len(*got) != 0 {
		t.Fatalf("got %v and %d requests", err, len(*got))
	}
}

func TestGet(t *testing.T) {
	srv, got := fake(t, func(w http.ResponseWriter, r *seen) {
		io.WriteString(w, `{"info":{"key_alias":"a"}}`)
	})
	var out struct {
		Info struct {
			KeyAlias string `json:"key_alias"`
		} `json:"info"`
	}
	err := newClient(t, srv.URL).Get(context.Background(),
		"/key/info?key=sk-1", &out)
	if err != nil || out.Info.KeyAlias != "a" {
		t.Fatalf("got %+v, %v", out, err)
	}
	if s := (*got)[0]; s.method != "GET" ||
		s.path != "/key/info?key=sk-1" || s.ctype != "" {
		t.Fatalf("request: %+v", s)
	}
	err = newClient(t, srv.URL).Get(context.Background(),
		"http://elsewhere/x", &out)
	if err == nil || len(*got) != 1 {
		t.Fatalf("a full URL as path: %v", err)
	}
}

func TestStatusErrors(t *testing.T) {
	cases := []struct {
		code      int
		body      string
		status    string
		message   string
		hint      string
		errString string
	}{
		{400, `{"error":{"message":"Key with alias 'a' already ` +
			`exists."}}`, "error: 400 (Bad Request)",
			"Key with alias 'a' already exists.", "",
			"the proxy returned error: 400 (Bad Request): " +
				"Key with alias 'a' already exists."},
		{401, `{"error":{"message":"Authentication Error, ` +
			`Received API Key = ` + master + `"}}`,
			"error: 401 (Unauthorized)",
			"Authentication Error, Received API Key = sk-...cdef",
			"It did not take the master key", ""},
		{404, `{"error":{"message":"{'error': 'No keys found'}"}}`,
			"error: 404 (Not Found)", "{'error': 'No keys found'}",
			"", ""},
		{500, "Internal Server Error", "error: 500 " +
			"(Internal Server Error)", "Internal Server Error",
			"database may be down", ""},
		{599, "", "error: 599", "", "database may be down",
			"the proxy returned error: 599"},
	}
	for _, c := range cases {
		srv, _ := fake(t, func(w http.ResponseWriter, r *seen) {
			w.WriteHeader(c.code)
			io.WriteString(w, c.body)
		})
		err := newClient(t, srv.URL).DeleteSeats(context.Background(),
			"a")
		var se *StatusError
		if !errors.As(err, &se) {
			t.Errorf("%d: got %v", c.code, err)
			continue
		}
		if se.Code != c.code || se.Status() != c.status ||
			se.Message != c.message ||
			!strings.Contains(se.Hint(), c.hint) ||
			c.errString != "" && se.Error() != c.errString {
			t.Errorf("%d: got %+v %q %q %q", c.code, se,
				se.Status(), se.Hint(), se.Error())
		}
		if strings.Contains(se.Error(), master) {
			t.Errorf("%d: master key in %q", c.code, se.Error())
		}
	}
}

func TestNoRedirects(t *testing.T) {
	var other atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { other.Add(1) }))
	defer elsewhere.Close()
	srv, _ := fake(t, func(w http.ResponseWriter, r *seen) {
		w.Header().Set("Location", elsewhere.URL+"/key/generate")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	err := newClient(t, srv.URL).DeleteKeys(context.Background(), "k")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 307 || other.Load() != 0 ||
		!strings.Contains(se.Hint(), "to "+elsewhere.URL) {
		t.Fatalf("got %v, %d requests elsewhere", err, other.Load())
	}
}

func TestAnswerErrors(t *testing.T) {
	for body, want := range map[string]string{
		`{"token":"x"}`: "the proxy answered, but it has no key in it",
		`{"key":null}`:  "the proxy answered, but it has no key in it",
		`{"key":7}`:     "the proxy answered, but it has no key in it",
		`<html>`: "the proxy answered, but it is not the JSON " +
			"expected: <html>",
	} {
		srv, _ := fake(t, func(w http.ResponseWriter, r *seen) {
			io.WriteString(w, body)
		})
		_, err := newClient(t, srv.URL).GenerateKey(
			context.Background(), GenerateRequest{
				Models: []string{"local"}})
		var ae *AnswerError
		if !errors.As(err, &ae) || err.Error() != want {
			t.Errorf("%s: got %v", body, err)
		}
	}
}

func connErr(t *testing.T, base string, timeout time.Duration) *ConnError {
	t.Helper()
	c, err := New(base, master, timeout)
	if err != nil {
		t.Fatal(err)
	}
	err = c.DeleteSeats(context.Background(), "a")
	var ce *ConnError
	if !errors.As(err, &ce) {
		t.Fatalf("%s: got %v", base, err)
	}
	if !strings.HasPrefix(ce.Error(), "Failed to connect to the "+
		"proxy at ") || ce.Hint == "" ||
		strings.Contains(ce.Error(), master) {
		t.Fatalf("%s: got %q, hint %q", base, ce.Error(), ce.Hint)
	}
	return ce
}

func TestConnErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	ln.Close()
	// Generous: on Windows, a refused connection takes 2 seconds.
	const wait = 10 * time.Second
	if ce := connErr(t, closed, wait); ce.Reason !=
		"connection refused: nothing is listening there" ||
		ce.URL != closed || ce.Late {
		t.Errorf("refused: %+v", ce)
	}

	hangup := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		}))
	defer hangup.Close()
	if ce := connErr(t, hangup.URL, wait); ce.Reason !=
		"it hung up without answering" {
		t.Errorf("hang-up: %+v", ce)
	}

	done := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { <-done }))
	defer slow.Close()
	defer close(done)
	if ce := connErr(t, slow.URL, 200*time.Millisecond); ce.Reason !=
		"no answer within 200ms" || !ce.Late ||
		!strings.Contains(ce.Hint, "may have done it anyway") {
		t.Errorf("timeout: %+v", ce)
	}
	// Answered, then too slow to finish: also late.
	stuck := make(chan struct{})
	slowBody := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-stuck
		}))
	defer slowBody.Close()
	defer close(stuck) // first, so slowBody can close
	if ce := connErr(t, slowBody.URL, 200*time.Millisecond); !ce.Late {
		t.Errorf("slow answer: %+v", ce)
	}

	tlsOnly := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {}))
	tlsOnly.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsOnly.StartTLS()
	defer tlsOnly.Close()
	if ce := connErr(t, tlsOnly.URL, wait); !strings.HasPrefix(
		ce.Reason, "its HTTPS certificate is not trusted") {
		t.Errorf("certificate: %+v", ce)
	}
	plain := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {}))
	defer plain.Close()
	https := strings.Replace(plain.URL, "http:", "https:", 1)
	if ce := connErr(t, https, wait); ce.Reason !=
		"it does not speak HTTPS on that port" {
		t.Errorf("https to http: %+v", ce)
	}
	if ce := connErr(t, "http://user:"+master+"@"+ln.Addr().String(),
		wait); strings.Contains(ce.URL, master) {
		t.Errorf("password shown: %+v", ce)
	}
}

func TestDNSError(t *testing.T) {
	// Through a web proxy, the proxy does the lookup, not this test.
	// t.Setenv cannot help: Go reads these once per program.
	for _, k := range []string{"HTTP_PROXY", "http_proxy"} {
		if os.Getenv(k) != "" {
			t.Skip(k + " is set")
		}
	}
	ce := connErr(t, "http://no-such-host.invalid:4000", 3*time.Second)
	// With no name server at all, a test machine may time out.
	if ce.Reason != "cannot find the computer named "+
		"no-such-host.invalid" &&
		!strings.Contains(ce.Reason, "within") {
		t.Errorf("dns: %+v", ce)
	}
}

func TestExplainDNSHint(t *testing.T) {
	for _, c := range []struct{ name, hint string }{
		{"proxy", hintStart + " " + hintURL},
		{"proxy.lan", hintURL},
	} {
		_, hint := explain(&net.DNSError{Name: c.name}, "3s")
		if hint != c.hint {
			t.Errorf("%s: hint %q, want %q", c.name, hint, c.hint)
		}
	}
}

// TestProxyEnv runs itself again with HTTP_PROXY and NO_PROXY set:
// Go reads them once per program, so this needs a fresh one.
func TestProxyEnv(t *testing.T) {
	if os.Getenv("LVP_PROXY_CHILD") == "1" {
		proxyChild(t)
		return
	}
	var (
		mu    sync.Mutex
		asked []string
	)
	proxy := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, r.Method+" "+r.URL.String()+" "+
				r.Header.Get("Authorization"))
			io.WriteString(w, `{"key":"sk-via-proxy"}`)
		}))
	defer proxy.Close()
	direct, _ := fake(t, func(w http.ResponseWriter, r *seen) {
		io.WriteString(w, `{"key":"sk-direct"}`)
	})
	cmd := exec.Command(os.Args[0], "-test.run=^TestProxyEnv$")
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
			"REQUEST_METHOD":
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(cmd.Env, "LVP_PROXY_CHILD=1",
		"HTTP_PROXY="+proxy.URL, "NO_PROXY=skip.invalid",
		"LVP_DIRECT="+direct.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	want := "POST http://via.example:4000/key/generate Bearer " + master
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 || asked[0] != want {
		t.Fatalf("the proxy was asked %q, want only %q", asked, want)
	}
}

func proxyChild(t *testing.T) {
	gen := func(base string) (string, error) {
		c, err := New(base, master, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return c.GenerateKey(context.Background(),
			GenerateRequest{Models: []string{"local"}})
	}
	if k, err := gen("http://via.example:4000"); k != "sk-via-proxy" {
		t.Errorf("through HTTP_PROXY: %q, %v", k, err)
	}
	// Loopback is always direct, even with HTTP_PROXY set.
	if k, err := gen(os.Getenv("LVP_DIRECT")); k != "sk-direct" {
		t.Errorf("loopback: %q, %v", k, err)
	}
	// NO_PROXY: straight to a name that does not exist.
	var ce *ConnError
	if _, err := gen("http://skip.invalid:4000"); !errors.As(err, &ce) {
		t.Errorf("NO_PROXY: %v", err)
	}
}
