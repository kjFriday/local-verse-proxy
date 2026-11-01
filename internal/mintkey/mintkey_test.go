package mintkey

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

const master = "sk-test-master-0123456789abcdef0123"

// fakeLiteLLM answers /key/generate and /key/delete the way LiteLLM
// v1.106.6 does, with the same error bodies.
type fakeLiteLLM struct {
	*httptest.Server
	mu    sync.Mutex
	seats map[string]string // key alias -> key
	n     int
	reqs  []request
	// answer, if set, answers instead.
	answer func(w http.ResponseWriter, r *http.Request)
}

type request struct {
	Method, Path, Auth string
	Body               map[string]any
	Raw                string
}

func liteErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"message": msg, "type": "x", "param": nil,
		"code": fmt.Sprint(code)}})
	w.Write(b)
}

func newFake(t *testing.T) *fakeLiteLLM {
	f := &fakeLiteLLM{seats: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeLiteLLM) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	req := request{Method: r.Method, Path: r.URL.Path,
		Auth: r.Header.Get("Authorization"), Raw: string(raw)}
	json.Unmarshal(raw, &req.Body)
	f.reqs = append(f.reqs, req)
	if f.answer != nil {
		f.answer(w, r)
		return
	}
	if req.Auth != "Bearer "+master {
		liteErr(w, 401, "Authentication Error, Invalid proxy server "+
			"token passed. Received API Key = sk-..."+
			req.Auth[len(req.Auth)-4:]+", Key Hash (Token) ="+
			strings.Repeat("ab", 32)+".")
		return
	}
	switch r.URL.Path {
	case "/key/generate":
		alias, _ := req.Body["key_alias"].(string)
		if _, taken := f.seats[alias]; taken {
			liteErr(w, 400, "Key with alias '"+alias+
				"' already exists. Unique key aliases "+
				"across all keys are required.")
			return
		}
		f.n++
		key := fmt.Sprintf("sk-fake%016d", f.n)
		f.seats[alias] = key
		json.NewEncoder(w).Encode(map[string]any{"key": key,
			"key_alias": alias, "token": "abc"})
	case "/key/delete":
		var gone []string
		for _, k := range list(req.Body["keys"]) {
			for a, v := range f.seats {
				if v == k {
					delete(f.seats, a)
					gone = append(gone, k)
				}
			}
		}
		for _, a := range list(req.Body["key_aliases"]) {
			if k, ok := f.seats[a]; ok {
				delete(f.seats, a)
				gone = append(gone, k)
			}
		}
		if len(gone) == 0 {
			liteErr(w, 404, "{'error': 'No keys found'}")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"deleted_keys": gone})
	default:
		w.WriteHeader(404)
		io.WriteString(w, `{"detail":"Not Found"}`)
	}
}

func list(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			s, _ := x.(string)
			out = append(out, s)
		}
	}
	return out
}

// setAnswer makes fn answer every request; nil goes back to normal.
func (f *fakeLiteLLM) setAnswer(fn func(http.ResponseWriter,
	*http.Request)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = fn
}

func (f *fakeLiteLLM) requests() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]request(nil), f.reqs...)
}

// harness is one folder with a seats.env, and the fake environment.
type harness struct {
	t       *testing.T
	fake    *fakeLiteLLM
	wd, exe string
	env     map[string]string
	timeout time.Duration
}

const template = `# Copy to seats.env and fill in.
LITELLM_MASTER_KEY=%s
LITELLM_SALT_KEY=sk-salt-0123456789
POSTGRES_PASSWORD=0123456789abcdef
DATABASE_URL=postgresql://litellm:0123456789abcdef@db:5432/litellm
OLLAMA_API_BASE=http://192.168.1.50:11434
ANTHROPIC_API_KEY=
PROXY_URL=%s
DEFAULT_MODELS=local
DEFAULT_BUDGET=5
DEFAULT_DURATION=30d
`

func setup(t *testing.T) *harness {
	h := &harness{t: t, fake: newFake(t), wd: t.TempDir(),
		exe: filepath.Join(t.TempDir(), "mint-key"),
		env: map[string]string{}, timeout: 10 * time.Second}
	h.write(fmt.Sprintf(template, master, h.fake.URL))
	return h
}

func (h *harness) write(text string) string {
	p := filepath.Join(h.wd, "seats.env")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		h.t.Fatal(err)
	}
	return p
}

func (h *harness) edit(from, to string) {
	p := filepath.Join(h.wd, "seats.env")
	b, err := os.ReadFile(p)
	if err != nil || !bytes.Contains(b, []byte(from)) {
		h.t.Fatalf("edit %q: %v", from, err)
	}
	h.write(strings.Replace(string(b), from, to, 1))
}

func (h *harness) sys() System {
	return System{
		Env: func(k string) (string, bool) {
			v, ok := h.env[k]
			return v, ok
		},
		Getwd:   func() (string, error) { return h.wd, nil },
		Exe:     func() (string, error) { return h.exe, nil },
		Timeout: h.timeout,
	}
}

func (h *harness) run(args ...string) (string, string, int) {
	h.t.Helper()
	var out, errs bytes.Buffer
	code := Main(args, h.sys(), &out, &errs)
	for _, s := range []string{out.String(), errs.String()} {
		if strings.Contains(s, master) ||
			strings.Contains(s, "0123456789abcdef") {
			h.t.Fatalf("%q: a secret was printed: %q", args, s)
		}
	}
	return out.String(), errs.String(), code
}

// expect runs args and checks all three results.
func (h *harness) expect(args []string, stdout, stderr string, code int) {
	h.t.Helper()
	o, e, c := h.run(args...)
	if o != stdout || e != stderr || c != code {
		h.t.Errorf("%q:\n got stdout %q\n     stderr %q\n     exit %d"+
			"\nwant stdout %q\n     stderr %q\n     exit %d",
			args, o, e, c, stdout, stderr, code)
	}
}

func (h *harness) body(i int) string {
	h.t.Helper()
	r := h.fake.requests()
	if i >= len(r) {
		h.t.Fatalf("only %d requests", len(r))
	}
	b, _ := json.Marshal(r[i].Body)
	return string(b)
}

var usageOut = strings.TrimSpace(usageText) + "\n"

func TestMintDefaults(t *testing.T) {
	h := setup(t)
	h.expect([]string{"laptop"}, "sk-fake0000000000000001\n", "", 0)
	r := h.fake.requests()[0]
	if r.Method != "POST" || r.Path != "/key/generate" ||
		r.Auth != "Bearer "+master {
		t.Fatalf("request: %+v", r)
	}
	want := `{"key_alias":"laptop","models":["local"],"max_budget":5,` +
		`"duration":"30d","metadata":{"seat":"laptop"}}`
	if r.Raw != want {
		t.Fatalf("body:\n got %s\nwant %s", r.Raw, want)
	}
}

func TestMintBuiltInDefaults(t *testing.T) {
	h := setup(t)
	h.write("LITELLM_MASTER_KEY=" + master + "\nPROXY_URL=" + h.fake.URL +
		"\nDEFAULT_BUDGET=\n")
	h.expect([]string{"laptop"}, "sk-fake0000000000000001\n", "", 0)
	want := `{"duration":"30d","key_alias":"laptop","max_budget":5,` +
		`"metadata":{"seat":"laptop"},"models":["local"]}`
	if b := h.body(0); b != want {
		t.Fatalf("body:\n got %s\nwant %s", b, want)
	}
}

func TestMintEveryFlag(t *testing.T) {
	h := setup(t)
	h.expect([]string{"desk", "--models", " local , local-small,,",
		"--budget", "2.50", "--duration", "12h", "--budget-duration",
		"1d"}, "sk-fake0000000000000001\n", "", 0)
	want := `{"budget_duration":"1d","duration":"12h",` +
		`"key_alias":"desk","max_budget":2.5,` +
		`"metadata":{"seat":"desk"},"models":["local","local-small"]}`
	if b := h.body(0); b != want {
		t.Fatalf("body:\n got %s\nwant %s", b, want)
	}
	// Flags in any order; the last of a repeated flag wins; a value
	// may start with a dash; an empty --budget-duration means none.
	h.expect([]string{"den", "--budget-duration", "1mo", "--models",
		"a", "--budget", "0", "--models", "-b", "--duration", "1mo",
		"--budget-duration", ""}, "sk-fake0000000000000002\n", "", 0)
	want = `{"duration":"1mo","key_alias":"den","max_budget":0,` +
		`"metadata":{"seat":"den"},"models":["-b"]}`
	if b := h.body(1); b != want {
		t.Fatalf("body:\n got %s\nwant %s", b, want)
	}
	h.expect([]string{"x1", "--budget", "007", "--duration", "0s"},
		"sk-fake0000000000000003\n", "", 0)
	if b := h.body(2); !strings.Contains(b, `"max_budget":7,`) ||
		!strings.Contains(b, `"duration":"0s"`) {
		t.Fatalf("body: %s", b)
	}
}

func TestSettingsFromEnvironment(t *testing.T) {
	h := setup(t)
	h.edit("DEFAULT_MODELS=local\n", "")
	h.env["DEFAULT_MODELS"] = "local-small"
	h.env["DEFAULT_BUDGET"] = "99" // the file's 5 wins
	h.expect([]string{"a"}, "sk-fake0000000000000001\n", "", 0)
	if b := h.body(0); !strings.Contains(b, `"models":["local-small"]`) ||
		!strings.Contains(b, `"max_budget":5,`) {
		t.Fatalf("body: %s", b)
	}
}

func TestUsage(t *testing.T) {
	h := setup(t)
	for _, args := range [][]string{
		{}, {""}, {"--help"}, {"-h"}, {"-x"}, {"--models", "a"},
		{"laptop", "--models"}, {"laptop", "--bogus", "1"},
		{"laptop", "--budget", "5", "--duration"},
		{"laptop", "--revoke", "k"}, {"laptop", "extra"},
		{"--revoke"}, {"--revoke", ""}, {"--revoke-seat"},
		{"--revoke-seat", ""}, {"--version", "x"},
	} {
		h.expect(args, "", usageOut, 2)
	}
	if n := len(h.fake.requests()); n != 0 {
		t.Fatalf("%d requests", n)
	}
	for _, line := range strings.Split(usageText, "\n") {
		if len(line) > 80 {
			t.Errorf("usage line over 80 columns: %q", line)
		}
	}
	if !strings.Contains(usageText, "mint-key.exe") ||
		!strings.HasPrefix(usageOut, "Mint a scoped key for one ") {
		t.Errorf("usage: %s", usageText)
	}
}

func TestValidation(t *testing.T) {
	h := setup(t)
	const (
		budget = "--budget must be a number of US dollars, " +
			"like 5 or 2.50\n"
		duration = "--duration must be a number and a unit, " +
			"like 30d, 12h or 2w\n"
		refill = "--budget-duration must look like 30m, 12h, 1d, 1w " +
			"or 1mo\n"
		models = "--models must name at least one model\n"
	)
	cases := []struct {
		args []string
		msg  string
	}{
		{[]string{"--budget", "abc"}, budget},
		{[]string{"--budget", "1e3"}, budget},
		{[]string{"--budget", "$20"}, budget},
		{[]string{"--budget", "-1"}, budget},
		{[]string{"--budget", ""}, budget},
		{[]string{"--budget", "1."}, budget},
		{[]string{"--budget", ".5"}, budget},
		{[]string{"--budget", "1,5"}, budget},
		{[]string{"--budget", " 5"}, budget},
		{[]string{"--budget", "5\n"}, budget},
		{[]string{"--budget", "٥"}, budget},
		{[]string{"--budget", strings.Repeat("9", 400)}, budget},
		{[]string{"--duration", "30"}, duration},
		{[]string{"--duration", "30D"}, duration},
		{[]string{"--duration", "1y"}, duration},
		{[]string{"--duration", ""}, duration},
		{[]string{"--duration", "1.5d"}, duration},
		{[]string{"--duration", "-1d"}, duration},
		{[]string{"--budget-duration", "0d"}, refill},
		{[]string{"--budget-duration", "2mo"}, refill},
		{[]string{"--budget-duration", "1M"}, refill},
		{[]string{"--budget-duration", "01d"}, refill},
		{[]string{"--budget-duration", "1.5h"}, refill},
		{[]string{"--budget-duration", "d"}, refill},
		{[]string{"--models", ""}, models},
		{[]string{"--models", " , ,"}, models},
		// The same order of checks as mint-key.sh.
		{[]string{"--models", "", "--duration", "x", "--budget", "x"},
			budget},
		{[]string{"--models", "", "--budget-duration", "x",
			"--duration", "x"}, duration},
		{[]string{"--models", "", "--budget-duration", "x"}, refill},
	}
	for _, c := range cases {
		h.expect(append([]string{"seat"}, c.args...), "", c.msg, 2)
	}
	h.edit("DEFAULT_BUDGET=5", "DEFAULT_BUDGET=five")
	h.expect([]string{"seat"}, "", budget, 2)
	if n := len(h.fake.requests()); n != 0 {
		t.Fatalf("%d requests", n)
	}
}

func TestRevoke(t *testing.T) {
	h := setup(t)
	key, _, _ := h.run("laptop")
	key = strings.TrimSpace(key)
	h.expect([]string{"--revoke", key, "ignored"}, "revoked\n", "", 0)
	if b := h.body(1); b != `{"keys":["`+key+`"]}` {
		t.Fatalf("body %s", b)
	}
	h.run("laptop")
	h.expect([]string{"--revoke-seat", "laptop", "x"},
		"revoked seat laptop\n", "", 0)
	if b := h.body(3); b != `{"key_aliases":["laptop"]}` {
		t.Fatalf("body %s", b)
	}
	notFound := "the proxy returned error: 404 (Not Found)\n" +
		"{'error': 'No keys found'}\n"
	// Unlike a seat to mint, a seat to revoke may start with a dash.
	h.expect([]string{"--revoke-seat", "-dash"}, "", notFound, 1)
	if b := h.body(4); b != `{"key_aliases":["-dash"]}` {
		t.Fatalf("body %s", b)
	}
	h.expect([]string{"--revoke-seat", "laptop"}, "", notFound, 1)
	h.expect([]string{"--revoke", "sk-nope"}, "", notFound, 1)
}

func TestProxyErrors(t *testing.T) {
	h := setup(t)
	h.run("laptop")
	h.expect([]string{"laptop", "--models", "local-small"}, "",
		"the proxy returned error: 400 (Bad Request)\nKey with "+
			"alias 'laptop' already exists. Unique key aliases "+
			"across all keys are required.\n", 1)

	h.edit(master, "sk-wrong-key-abcd")
	_, e, c := h.run("other")
	want := "the proxy returned error: 401 (Unauthorized). It did not " +
		"take the master key. If seats.env changed, run: docker " +
		"compose up -d --force-recreate proxy (or, if Front Desk " +
		"runs in Docker: docker compose --profile frontdesk up -d)\n" +
		"Authentication Error, " +
		"Invalid proxy server token passed. Received API Key = " +
		"sk-...abcd, Key Hash (Token) ="
	if c != 1 || !strings.HasPrefix(e, want) {
		t.Fatalf("401: exit %d\n%s", c, e)
	}
	h.edit("sk-wrong-key-abcd", master)

	answer := func(code int, body string) {
		h.fake.setAnswer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			io.WriteString(w, body)
		})
	}
	answer(422, `{"detail":[{"type":"list_type","loc":["body",`+
		`"models"],"msg":"Input should be a valid list"}]}`)
	h.expect([]string{"a"}, "", "the proxy returned error: 422 "+
		"(Unprocessable Entity)\n[\n  {\n"+
		"    \"type\": \"list_type\",\n"+
		"    \"loc\": [\n      \"body\",\n      \"models\"\n    ],\n"+
		"    \"msg\": \"Input should be a valid list\"\n"+
		"  }\n]\n", 1)

	answer(500, "Internal Server Error\n")
	h.expect([]string{"--revoke", "k"}, "", "the proxy returned error: "+
		"500 (Internal Server Error). Its database may be down: run "+
		"docker compose ps\nInternal Server Error\n", 1)

	answer(502, "")
	h.expect([]string{"--revoke-seat", "a"}, "", "the proxy returned "+
		"error: 502 (Bad Gateway). Its database may be down: run "+
		"docker compose ps\n", 1)

	// LiteLLM's message names the master key: it is hidden.
	answer(400, `{"error":{"message":"bad key `+master+`"}}`)
	h.expect([]string{"a"}, "", "the proxy returned error: 400 "+
		"(Bad Request)\nbad key sk-...0123\n", 1)

	answer(200, `{"token":"abc"}`)
	h.expect([]string{"a"}, "", "the proxy answered, but it has no key "+
		"in it\n", 1)
	answer(200, `<html>hello</html>`)
	h.expect([]string{"a"}, "", "the proxy answered, but it is not the "+
		"JSON expected: <html>hello</html>\n", 1)

	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			elsewhere.Add(1)
		}))
	defer other.Close()
	h.fake.setAnswer(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	})
	_, e, c = h.run("a")
	if c != 1 || elsewhere.Load() != 0 || !strings.HasPrefix(e,
		"the proxy returned error: 302 (Found). It sent the "+
			"request elsewhere (to "+other.URL+"/key/generate). "+
			"Check PROXY_URL in seats.env: http or https, and "+
			"the port.\n") {
		t.Fatalf("redirect: exit %d, %d elsewhere\n%s", c,
			elsewhere.Load(), e)
	}
}

func TestConnectionErrors(t *testing.T) {
	h := setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	h.edit("PROXY_URL="+h.fake.URL, "PROXY_URL=http://"+addr)
	start := "Is it running? Check with: docker compose ps. Just " +
		"started? Wait 30 seconds, then try again.\n"
	h.expect([]string{"a"}, "", "Failed to connect to the proxy at "+
		"http://"+addr+" (connection refused: nothing is listening "+
		"there).\n"+start, 1)
	h.expect([]string{"--revoke", "k"}, "", "Failed to connect to the "+
		"proxy at http://"+addr+" (connection refused: nothing is "+
		"listening there).\n"+start, 1)

	h.edit("PROXY_URL=http://"+addr, "PROXY_URL="+h.fake.URL)
	h.fake.setAnswer(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	})
	h.expect([]string{"--revoke-seat", "a"}, "", "Failed to connect to "+
		"the proxy at "+h.fake.URL+" (it hung up without answering).\n"+
		start, 1)

	h.fake.setAnswer(nil)
	done := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { <-done }))
	defer slow.Close()
	defer close(done) // first, so slow can close
	h.edit("PROXY_URL="+h.fake.URL, "PROXY_URL="+slow.URL)
	h.timeout = 150 * time.Millisecond
	// The proxy got the request: it may have done it anyway.
	h.expect([]string{"a"}, "", "Failed to connect to the proxy at "+
		slow.URL+" (no answer within 150ms).\nIt got the request but "+
		"did not answer in time, so it may have made the key anyway. "+
		"To be sure, run this program again with --revoke-seat a, "+
		"then mint again.\n",
		1)
	for _, args := range [][]string{{"--revoke", "k"},
		{"--revoke-seat", "a"}} {
		h.expect(args, "", "Failed to connect to the proxy at "+
			slow.URL+" (no answer within 150ms).\nIt got the "+
			"request but did not answer in time, so it may have "+
			"revoked it anyway. Run the same line again to "+
			"check.\n",
			1)
	}
}

func TestProxyURL(t *testing.T) {
	h := setup(t)
	bare := strings.TrimPrefix(h.fake.URL, "http://")
	// No scheme means http://, as with curl; a trailing / is dropped.
	for i, u := range []string{bare, h.fake.URL + "/", "'" + bare + "'"} {
		h.edit("PROXY_URL="+h.fake.URL, "PROXY_URL="+u)
		h.expect([]string{fmt.Sprint("s", i)},
			fmt.Sprintf("sk-fake%016d\n", i+1), "", 0)
		if p := h.fake.requests()[i].Path; p != "/key/generate" {
			t.Fatalf("%s: path %s", u, p)
		}
		h.edit("PROXY_URL="+u, "PROXY_URL="+h.fake.URL)
	}
	p := filepath.Join(h.wd, "seats.env")
	h.edit("PROXY_URL="+h.fake.URL, "PROXY_URL=ftp://x")
	h.expect([]string{"a"}, "", "PROXY_URL in "+p+" is not a web "+
		"address (it must start with http:// or https://). It should "+
		"look like http://localhost:4000\n", 1)
	// Bad usage is still found first, as with mint-key.sh.
	h.expect([]string{"a", "--budget", "x"}, "", "--budget must be a "+
		"number of US dollars, like 5 or 2.50\n", 2)
	// An empty or missing PROXY_URL means the usual address. (Not
	// tried here: something real may be listening on port 4000.)
	// The environment's PROXY_URL counts only if the file has none.
	h.env["PROXY_URL"] = "http://env:1"
	for text, want := range map[string]string{
		"PROXY_URL=\n": "http://localhost:4000",
		"A=1\n":        "http://env:1",
	} {
		f, err := seats.Load(h.write(text), h.sys().Env)
		if err != nil {
			t.Fatal(err)
		}
		if u := proxyURL(f); u != want {
			t.Errorf("%q: got %s, want %s", text, u, want)
		}
	}
	delete(h.env, "PROXY_URL")
	f, _ := seats.Load(h.write("A=1\n"), h.sys().Env)
	if u := proxyURL(f); u != "http://localhost:4000" {
		t.Errorf("no PROXY_URL: got %s", u)
	}
}

func TestSeatsEnv(t *testing.T) {
	h := setup(t)
	p := filepath.Join(h.wd, "seats.env")

	h.edit("LITELLM_MASTER_KEY="+master, "LITELLM_MASTER_KEY=")
	h.expect([]string{"a"}, "", "LITELLM_MASTER_KEY: not set in "+p+
		"\n", 1)
	// Checked before usage, as in mint-key.sh.
	h.expect([]string{}, "", "LITELLM_MASTER_KEY: not set in "+p+"\n", 1)
	h.env["LITELLM_MASTER_KEY"] = master // the file's empty one wins
	h.expect([]string{"a"}, "", "LITELLM_MASTER_KEY: not set in "+p+
		"\n", 1)
	h.edit("LITELLM_MASTER_KEY=\n", "")
	h.expect([]string{"a"}, "sk-fake0000000000000001\n", "", 0)
	delete(h.env, "LITELLM_MASTER_KEY")
	h.edit("LITELLM_SALT_KEY=", "LITELLM_MASTER_KEY="+master+
		"\nLITELLM_SALT_KEY=")

	h.edit("LITELLM_SALT_KEY=sk-salt-0123456789",
		"LITELLM_SALT_KEY=sk-change-me")
	h.expect([]string{"--revoke", "k"}, "", "replace the change-me "+
		"values in "+p+" first\n", 1)
	h.edit("LITELLM_SALT_KEY=sk-change-me",
		"LITELLM_SALT_KEY=sk-salt-0123456789")

	h.edit("DEFAULT_MODELS=local", "DEFAULT_MODELS=$(rm -rf ~)")
	h.expect([]string{"a"}, "", p+" line 9: $(...) would run a command; "+
		"to keep it as is, put the value in single quotes\n", 1)
	h.edit("DEFAULT_MODELS=$(rm -rf ~)", "DEFAULT_MODELS=local")

	// Quotes, export and comments, as bash reads them.
	h.write("export LITELLM_MASTER_KEY='" + master + "'  # the lock\n" +
		"PROXY_URL=\"" + h.fake.URL + "\"\r\nDEFAULT_MODELS=\"a b\"\n")
	h.expect([]string{"q"}, "sk-fake0000000000000002\n", "", 0)
	if b := h.body(1); !strings.Contains(b, `"models":["a b"]`) {
		t.Fatalf("body %s", b)
	}

	// Where it is found: SEATS_ENV, this folder, the program's folder.
	other := filepath.Join(t.TempDir(), "other.env")
	h.env["SEATS_ENV"] = other
	h.expect([]string{"a"}, "", "missing "+other+"\n", 1)
	os.Rename(p, other)
	h.expect([]string{"b"}, "sk-fake0000000000000003\n", "", 0)
	delete(h.env, "SEATS_ENV")
	exeEnv := filepath.Join(filepath.Dir(h.exe), "seats.env")
	h.expect([]string{"c"}, "", "missing "+p+"\n(also looked for "+
		exeEnv+")\nMake it as README.md Step 3 shows, or set "+
		"SEATS_ENV to its path.\n", 1)
	os.Rename(other, exeEnv)
	h.expect([]string{"c"}, "sk-fake0000000000000004\n", "", 0)
}

func TestVersion(t *testing.T) {
	h := setup(t)
	os.Remove(filepath.Join(h.wd, "seats.env"))
	h.expect([]string{"--version"}, "mint-key dev\n", "", 0)
}
