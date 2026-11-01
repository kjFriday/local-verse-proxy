package mintkey

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// Exec on a file already read prints what Main prints for it.
func TestExecEqualsMain(t *testing.T) {
	h := setup(t)
	file, err := seats.Load(filepath.Join(h.wd, "seats.env"), h.sys().Env)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{}, {"-x"}, {"x", "--budget", "y"}, {"--version"},
		{"--revoke", "sk-nope"}, {"--revoke-seat", "nobody"},
		{"seat", "--duration", "1y"},
	} {
		o, e, c := h.run(args...)
		r := Exec(context.Background(), file, args, 10*time.Second)
		if r.Stdout != o || r.Stderr != e || r.Code != c {
			t.Errorf("%q:\n Exec %+v\n Main %q %q %d", args, r, o,
				e, c)
		}
	}
	r := Exec(context.Background(), file, []string{"e1", "--models",
		"local"}, 10*time.Second)
	if r.Code != 0 || !strings.HasPrefix(r.Stdout, "sk-fake") ||
		r.Stderr != "" {
		t.Fatalf("Exec mint: %+v", r)
	}
	if o, _, c := h.run("--revoke-seat", "e1"); o != "revoked seat e1\n" ||
		c != 0 {
		t.Fatalf("the key Exec made: %q %d", o, c)
	}
}

// What Front Desk's plain_error looks for must stay in the messages.
func TestFrontDeskContract(t *testing.T) {
	h := setup(t)
	h.run("taken")
	for _, c := range []struct {
		args []string
		has  []string
	}{
		{[]string{"taken"}, []string{"already exists"}},
		{[]string{"--revoke-seat", "nobody"},
			[]string{"error: 404", "No keys found"}},
	} {
		_, e, code := h.run(c.args...)
		for _, s := range c.has {
			if code == 0 || !strings.Contains(e, s) {
				t.Errorf("%q: %q lacks %q", c.args, e, s)
			}
		}
	}
	h.edit(master, "sk-wrong-key-abcd")
	if _, e, _ := h.run("x"); !strings.Contains(e, "error: 401") {
		t.Errorf("401: %q", e)
	}
	h.edit("sk-wrong-key-abcd", master)
	h.edit("LITELLM_SALT_KEY=sk-salt-0123456789",
		"LITELLM_SALT_KEY=sk-change-me")
	if _, e, _ := h.run("x"); !strings.Contains(e, "change-me") {
		t.Errorf("change-me: %q", e)
	}
	h.edit("LITELLM_SALT_KEY=sk-change-me",
		"LITELLM_SALT_KEY=sk-salt-0123456789")
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	h.edit("PROXY_URL="+h.fake.URL, "PROXY_URL=http://"+addr)
	if _, e, _ := h.run("x"); !strings.Contains(e, "Failed to connect") {
		t.Errorf("no proxy: %q", e)
	}
}

// Exec uses a web proxy only when its file's settings name one, as a
// mint-key child given only those settings would.
func TestExecWebProxy(t *testing.T) {
	var hits atomic.Int32
	webProxy := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(502)
		}))
	defer webProxy.Close()
	if envProxy(func(string) (string, bool) { return "", false }) {
		t.Fatal("no settings name a proxy")
	}
	file := seats.FromEnviron([]string{"LITELLM_MASTER_KEY=" + master,
		"PROXY_URL=http://example.invalid:4000"})
	r := Exec(context.Background(), file, []string{"--revoke", "k"},
		2*time.Second)
	if r.Code != 1 || hits.Load() != 0 ||
		!strings.Contains(r.Stderr, "cannot find the computer") {
		t.Fatalf("no proxy settings: %+v, %d hits", r, hits.Load())
	}
	with := seats.FromEnviron([]string{"HTTPS_PROXY=" + webProxy.URL})
	if !envProxy(with.Lookup) {
		t.Fatal("HTTPS_PROXY in the settings")
	}
}
