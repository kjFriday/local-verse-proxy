//go:build e2e

package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"
)

// The live twin (spec 16.5): Python's and Go's Front Desk on one real
// proxy and database, both local (no password):
//
//	E2E_PY_URL=http://127.0.0.1:8123 E2E_GO_URL=http://127.0.0.1:8023 \
//	  go test -tags e2e -run TestLiveTwin ./tests/e2e/live

type desk struct {
	name, url, csrf string
}

func desks(t *testing.T) (*desk, *desk) {
	py, gs := os.Getenv("E2E_PY_URL"), os.Getenv("E2E_GO_URL")
	if py == "" || gs == "" {
		t.Skip("E2E_PY_URL and E2E_GO_URL name the two desks")
	}
	a, b := &desk{name: "python", url: py}, &desk{name: "go", url: gs}
	for _, d := range []*desk{a, b} {
		var s map[string]any
		d.get(t, "/api/session", &s)
		d.csrf, _ = s["csrf"].(string)
	}
	return a, b
}

func (d *desk) get(t *testing.T, path string, v any) {
	r, err := http.Get(d.url + path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	json.NewDecoder(r.Body).Decode(v)
}

func (d *desk) post(t *testing.T, path string, body any) (int,
	map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", d.url+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", d.csrf)
	req.Header.Set("Origin", d.url)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	rb, _ := io.ReadAll(r.Body)
	var j map[string]any
	json.Unmarshal(rb, &j)
	return r.StatusCode, j
}

// view is a state with the per-process card ids taken out, cards
// keyed by (seat, created, ends) as spec 16.4 maps them.
func view(t *testing.T, d *desk) map[string]any {
	var s map[string]any
	d.get(t, "/api/state", &s)
	cards := map[string]any{}
	list, _ := s["cards"].([]any)
	for _, c := range list {
		m, _ := c.(map[string]any)
		delete(m, "id")
		cards[fmt.Sprint(m["seat"], "|", m["created"], "|",
			m["ends"])] = m
	}
	s["cards"] = cards
	return s
}

func seats(t *testing.T, d *desk) map[string]bool {
	var s struct {
		Cards []struct{ Seat string } `json:"cards"`
	}
	d.get(t, "/api/state", &s)
	out := map[string]bool{}
	for _, c := range s.Cards {
		out[c.Seat] = true
	}
	return out
}

func TestLiveTwin(t *testing.T) {
	py, gs := desks(t)
	// 1. Equal at a quiet point. Each polls on its own cadence, so a
	// few tries are allowed for both to have caught up.
	equal := false
	var a, b map[string]any
	for i := 0; i < 6 && !equal; i++ {
		a, b = view(t, py), view(t, gs)
		equal = reflect.DeepEqual(a, b)
		if !equal {
			time.Sleep(5 * time.Second)
		}
	}
	if !equal {
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		t.Errorf(
			"states differ at a quiet point:\npython %s\ngo     %s",
			ja, jb)
	} else {
		t.Logf("states equal at a quiet point: %d cards",
			len(a["cards"].(map[string]any)))
	}
	// 2. A mint on one shows on itself at once, on the other within
	// 10 s; the same for a cancel.
	for _, pair := range [][2]*desk{{gs, py}, {py, gs}} {
		on, other := pair[0], pair[1]
		seat := fmt.Sprintf("twin-%s-%d", on.name, os.Getpid())
		st, j := on.post(t, "/api/mint", map[string]any{"seat": seat,
			"models": []string{"local"}, "budget": "1",
			"duration": "1h"})
		if st != 200 {
			t.Fatalf("mint on %s: %d %v", on.name, st, j["error"])
		}
		if !seats(t, on)[seat] {
			t.Errorf("%s: its own mint is not on its page at once",
				on.name)
		}
		t0 := time.Now()
		for !seats(t, other)[seat] && time.Since(t0) < 12*time.Second {
			time.Sleep(200 * time.Millisecond)
		}
		took := time.Since(t0)
		t.Logf("minted on %s: on %s after %.1f s", on.name, other.name,
			took.Seconds())
		if took > 10*time.Second+500*time.Millisecond {
			t.Errorf("minted on %s: on %s after %.1f s", on.name,
				other.name, took.Seconds())
		}
		st, _ = on.post(t, "/api/revoke", map[string]any{"seat": seat})
		if st != 200 || seats(t, on)[seat] {
			t.Errorf("%s: revoke %d, or still on its page", on.name,
				st)
		}
		t0 = time.Now()
		for seats(t, other)[seat] && time.Since(t0) < 12*time.Second {
			time.Sleep(200 * time.Millisecond)
		}
		t.Logf("cancelled on %s: gone from %s after %.1f s", on.name,
			other.name, time.Since(t0).Seconds())
	}
}
