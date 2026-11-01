package setup

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestTestPasses(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	o, e, c := w.run(w.sys.Test)
	if c != 0 || e != "" {
		t.Fatalf("%d\n%s%s", c, o, e)
	}
	re := regexp.MustCompile(`^Minted a test key for seat ` +
		`test-[0-9a-f]{6}: model local only, 1 dollar\. It is ` +
		`revoked ` +
		`at the end\. PASS 1\. the key works: local answered\. ` +
		`PASS 2\. the wrong model is refused: claude got 403, not ` +
		`available for this key\. PASS 3\. a revoked key is refused: ` +
		`revoked, then 401\. All three checks pass: your proxy ` +
		`works, and the test key is gone\.$`)
	if !re.MatchString(flat(o)) {
		t.Errorf("\n%s", o)
	}
	if len(p.Keys()) != 0 {
		t.Errorf("a key is left: %+v", p.Keys())
	}
	// README Step 6's requests: the mint, the two chats, the
	// revoke, the knock.
	var seen []string
	for _, r := range p.Requests() {
		seen = append(seen, r.Method+" "+r.Path)
		if r.Path == "/key/generate" {
			var in map[string]any
			json.Unmarshal(r.Body, &in)
			if in["max_budget"] != 1.0 || in["models"].([]any)[0] !=
				"local" {
				t.Errorf("mint: %s", r.Body)
			}
		}
	}
	if strings.Join(seen, ",") != "POST /key/generate,"+
		"POST /v1/chat/completions,POST /v1/chat/completions,"+
		"POST /key/delete,GET /v1/models" {
		t.Errorf("requests: %q", seen)
	}
	for _, k := range []string{masterKey} {
		if strings.Contains(o, k) {
			t.Error("printed the master key")
		}
	}
	if strings.Contains(o, "sk-") {
		t.Errorf("printed a key: %q", o)
	}
}

// A check that fails says why; the key is revoked all the same.
func TestTestFails(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	p.Fail("/v1/chat/completions", 500, `{"error":{"message":`+
		`"litellm.APIConnectionError: OllamaException - Cannot `+
		`connect to host","type":null,"param":null,"code":"500"}}`)
	o, e, c := w.run(w.sys.Test)
	if c != 1 {
		t.Fatalf("%d\n%s%s", c, o, e)
	}
	for _, want := range []string{
		"FAIL 1. the key works: error: 500 (Internal Server Error). " +
			"litellm.APIConnectionError",
		"FAIL 2. the wrong model is refused: error: 500 (Internal " +
			"Server Error), not 403.",
		"PASS 3. a revoked key is refused",
	} {
		if !strings.Contains(flat(o), want) {
			t.Errorf("missing %q:\n%s", want, o)
		}
	}
	if !strings.Contains(flat(e), "2 of 3 checks failed") ||
		len(p.Keys()) != 0 {
		t.Errorf("%q %d", e, len(p.Keys()))
	}
}

// Check 3's revoke fails: the seat is revoked by name at the end.
func TestTestRevokesBySeat(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	// In front of the fake: the first revoke gets a 500.
	to, _ := url.Parse(p.URL)
	rp := httputil.NewSingleHostReverseProxy(to)
	var once sync.Once
	front := httptest.NewServer(http.HandlerFunc(
		func(rw http.ResponseWriter, r *http.Request) {
			failed := false
			if r.URL.Path == "/key/delete" {
				once.Do(func() { failed = true })
			}
			if failed {
				rw.WriteHeader(500)
				io.WriteString(rw,
					`{"error":{"message":"db down"}}`)
				return
			}
			rp.ServeHTTP(rw, r)
		}))
	t.Cleanup(front.Close)
	w.seats(front.URL)
	o, e, c := w.run(w.sys.Test)
	if c != 1 || !strings.Contains(flat(o), "FAIL 3. a revoked key is "+
		"refused: revoking it failed") ||
		!strings.Contains(o, "Revoked the test key.") ||
		len(p.Keys()) != 0 {
		t.Errorf("%d\n%s%s%d", c, o, e, len(p.Keys()))
	}
}

// The wrong model answered: the key was not stopped at the door.
func TestTestWrongModelAnswers(t *testing.T) {
	ok, why := chatCheck(t.Context(), fake(t).URL, masterKey,
		"claude", false)
	if ok || !strings.Contains(why, "not stopped at the door") {
		t.Errorf("%v %q", ok, why)
	}
}

func TestTestNoMint(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	p.Down(true)
	o, e, c := w.run(w.sys.Test)
	if c != 1 || o != "" || !strings.Contains(e, "Failed to connect") ||
		!strings.Contains(flat(e), "FAIL could not mint a test key") {
		t.Errorf("%d %q %q", c, o, e)
	}
	if _, e, c := w.run(w.sys.Test, "x"); c != 2 ||
		!strings.Contains(e, "unrecognized") {
		t.Errorf("%d %q", c, e)
	}
}
