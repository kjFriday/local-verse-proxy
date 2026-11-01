package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/litellm/fakelitellm"
)

// TestFakeMatchesLive plays the recorded steps against fakelitellm:
// each answer's status, error type and field names must be the ones
// the real LiteLLM gave (testdata/golden/captures.json, recorded by
// TestCapture on a live v1.106.6). A field the fake leaves out is
// listed here, so a change is seen.
func TestFakeMatchesLive(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "golden",
		"captures.json"))
	if err != nil {
		t.Skip("no captures recorded yet")
	}
	var want []Step
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	const master = "sk-fake-live-master-1"
	s := fakelitellm.Start(fakelitellm.Options{MasterKey: master,
		Models: []string{"local", "pro"}})
	defer s.Close()
	got := Play(s.URL, master, "cap-fake", Steps(time.Now()))
	byName := map[string]Step{}
	for _, g := range got {
		byName[g.Name] = g
	}
	for _, w := range want {
		g, ok := byName[w.Name]
		if !ok {
			t.Errorf("%s: not played", w.Name)
			continue
		}
		if g.Status != w.Status || g.ErrorType != w.ErrorType {
			t.Errorf("%s: fake %d %q, LiteLLM %d %q", w.Name,
				g.Status,
				g.ErrorType, w.Status, w.ErrorType)
		}
		if missing := minus(w.Fields, g.Fields); len(missing) > 0 &&
			!known[w.Name] {
			t.Errorf("%s: the fake leaves out %s", w.Name,
				strings.Join(missing, ", "))
		}
		if extra := minus(g.Fields, w.Fields); len(extra) > 0 {
			t.Errorf("%s: the fake adds %s", w.Name,
				strings.Join(extra, ", "))
		}
	}
}

// known are steps whose answers the fake keeps smaller on purpose.
var known = map[string]bool{}

func minus(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}
