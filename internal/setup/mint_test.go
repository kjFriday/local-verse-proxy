package setup

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/fsx"
)

var seatKeyRE = regexp.MustCompile(`^sk-[A-Za-z0-9_-]{20,}$`)

func TestMintPrints(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	o, e, c := w.run(w.sys.Mint, "laptop", "--budget", "2")
	keys := p.Keys()
	if c != 0 || e != "" || len(keys) != 1 ||
		o != keys[0].Key+"\n" || keys[0].Alias != "laptop" ||
		*keys[0].MaxBudget != 2 {
		t.Errorf("%d %q %q %+v", c, o, e, keys)
	}
	// Mistakes are mint-key's, with lvp's usage.
	_, e, c = w.run(w.sys.Mint)
	if c != 2 || !strings.Contains(e, "the seat name is missing") {
		t.Errorf("%d %q", c, e)
	}
	_, e, c = w.run(w.sys.Mint, "laptop2", "--budget", "$20")
	if c != 2 || !strings.Contains(e, "budget") {
		t.Errorf("%d %q", c, e)
	}
	_, e, c = w.run(w.sys.Mint, "-x")
	if c != 2 || !strings.HasPrefix(e, "usage: ./lvp mint <seat>") ||
		strings.Contains(e, "mint-key <seat>") {
		t.Errorf("%d %q", c, e)
	}
	_, e, c = w.run(w.sys.Mint, "laptop", "--revoke-seat", "x")
	if c != 2 || !strings.Contains(e, "to revoke, use ./lvp revoke") {
		t.Errorf("%d %q", c, e)
	}
	// Taken: mint-key's own words.
	_, e, c = w.run(w.sys.Mint, "laptop")
	if c != 1 || !strings.Contains(e, "already exists") {
		t.Errorf("%d %q", c, e)
	}
}

func TestMintSave(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	path := filepath.Join(w.dir, "laptop.key")
	o, e, c := w.run(w.sys.Mint, "laptop", "--save", path)
	keys := p.Keys()
	if c != 0 || e != "" || len(keys) != 1 {
		t.Fatalf("%d %q %q", c, o, e)
	}
	b, _ := os.ReadFile(path)
	if string(b) != keys[0].Key+"\n" || !seatKeyRE.MatchString(
		strings.TrimSpace(string(b))) {
		t.Errorf("file: %q", b)
	}
	if strings.Contains(o, keys[0].Key) || strings.Contains(o, "sk-") ||
		!strings.Contains(flat(o), "Saved the key for seat laptop in "+
			path+"; only you may open it.") {
		t.Errorf("%q", o)
	}
	if ok, why, err := fsx.IsPrivate(path); !ok || err != nil {
		t.Errorf("not private: %s %v", why, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", st.Mode())
		}
	}
	// There already: refused, and nothing minted.
	o, e, c = w.run(w.sys.Mint, "desk", "--save="+path)
	b2, _ := os.ReadFile(path)
	if c != 1 || o != "" || !strings.Contains(flat(e), path+" is "+
		"there already, so nothing was minted") ||
		len(p.Keys()) != 1 || string(b2) != string(b) {
		t.Errorf("%d %q %q", c, o, e)
	}
	// A failed mint leaves no file behind.
	other := filepath.Join(w.dir, "other.key")
	_, e, c = w.run(w.sys.Mint, "laptop", "--save", other)
	if _, err := os.Stat(other); c != 1 || err == nil {
		t.Errorf("%d %q %v", c, e, err)
	}
	// --save needs a file.
	for _, a := range [][]string{{"x", "--save"}, {"x", "--save="},
		{"x", "--save", "--budget"}} {
		if _, e, c := w.run(w.sys.Mint, a...); c != 2 ||
			!strings.Contains(e, "--save: expected one argument") {
			t.Errorf("%q: %d %q", a, c, e)
		}
	}
}

func TestMintNoSeats(t *testing.T) {
	w := newWorld(t)
	_, e, c := w.run(w.sys.Mint, "laptop")
	if c != 1 || !strings.Contains(e, "missing") {
		t.Errorf("%d %q", c, e)
	}
}

func TestRevoke(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	w.run(w.sys.Mint, "a")
	o, e, c := w.run(w.sys.Revoke, "a")
	if c != 0 || o != "revoked seat a\n" || e != "" ||
		len(p.Keys()) != 0 {
		t.Errorf("%d %q %q", c, o, e)
	}
	path := filepath.Join(w.dir, "b.key")
	w.run(w.sys.Mint, "b", "--save", path)
	o, e, c = w.run(w.sys.Revoke, "--key-file", path)
	if c != 0 || !strings.HasPrefix(o, "revoked\n") ||
		!strings.Contains(flat(o), path+" may go now") ||
		len(p.Keys()) != 0 {
		t.Errorf("%d %q %q", c, o, e)
	}
	key, _, _ := w.run(w.sys.Mint, "c")
	o, e, c = w.run(w.sys.Revoke, "--key", strings.TrimSpace(key))
	if c != 0 || o != "revoked\n" || len(p.Keys()) != 0 {
		t.Errorf("%d %q %q", c, o, e)
	}
	// Gone already: mint-key's 404, exit 1.
	_, e, c = w.run(w.sys.Revoke, "a")
	if c != 1 || !strings.Contains(e, "404") {
		t.Errorf("%d %q", c, e)
	}
}

func TestRevokeMistakes(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	for _, a := range [][]string{{}, {"a", "b"}, {"a", "--key", "k"},
		{"--key-file", "f", "--key", "k"}, {"sk-test-pasted-key"}} {
		if _, e, c := w.run(w.sys.Revoke, a...); c != 2 ||
			!strings.HasPrefix(e, "usage: ./lvp revoke") {
			t.Errorf("%q: %d %q", a, c, e)
		}
	}
	bad := filepath.Join(w.dir, "bad.key")
	write(t, bad, "hello there\n")
	_, e, c := w.run(w.sys.Revoke, "--key-file", bad)
	if c != 1 || !strings.Contains(flat(e), "does not hold one key") ||
		strings.Contains(e, "hello") {
		t.Errorf("%d %q", c, e)
	}
	_, e, c = w.run(w.sys.Revoke, "--key-file",
		filepath.Join(w.dir, "nope.key"))
	if c != 1 || !strings.Contains(e, "cannot read") {
		t.Errorf("%d %q", c, e)
	}
	if len(p.Requests()) != 0 {
		t.Error("asked the proxy")
	}
}

// A key file saved by Notepad: a byte order mark and \r\n.
func TestReadKeyNotepad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.key")
	write(t, path, bom+"sk-test-notepad-0001\r\n")
	if k, err := readKey(path); err != nil ||
		k != "sk-test-notepad-0001" {
		t.Errorf("%q %v", k, err)
	}
}
