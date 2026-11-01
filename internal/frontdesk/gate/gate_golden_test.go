package gate

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
)

// counter is a random source whose nth read starts with n, so a
// token tells which call made it, as the golden's T<n> tokens do.
type counter struct {
	mu sync.Mutex
	n  uint32
}

func (c *counter) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range p {
		p[i] = 0
	}
	binary.BigEndian.PutUint32(p, c.n)
	c.n++
	return len(p), nil
}

func index(t *testing.T, token string) int {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) < 4 {
		t.Fatalf("token %q", token)
	}
	return int(binary.BigEndian.Uint32(b))
}

type op struct {
	Op     string
	Wall   *float64
	Mono   *float64
	Addr   string
	PW     string `json:"pw"`
	Cookie *int
	Result map[string]any
}

func TestGoldenTraces(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "golden",
		"gate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Traces []struct {
			Name     string
			Password *string
			Ops      []op
		}
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Traces) < 9 {
		t.Fatalf("%d traces", len(g.Traces))
	}
	for _, tr := range g.Traces {
		clk := clock.NewFake(1e9, 1000)
		pw := ""
		if tr.Password != nil {
			pw = *tr.Password
		}
		gt := New(pw, tr.Password != nil, clk, &counter{})
		if index(t, gt.OpenCSRF()) != 0 {
			t.Fatal("open csrf is not the first token")
		}
		cookies := map[int]string{}
		for i, o := range tr.Ops {
			if o.Wall != nil {
				clk.SetWall(*o.Wall)
			}
			if o.Mono != nil {
				clk.Advance(*o.Mono - clk.Mono())
				clk.SetWall(wallOr(o.Wall, clk.Wall()))
			}
			got := map[string]any{}
			switch o.Op {
			case "login":
				c, s, err := gt.Login(o.Addr, o.PW)
				var r *Refused
				var w *WrongPassword
				switch {
				case err == nil:
					cookies[index(t, c)] = c
					got = map[string]any{"r": "ok",
						"cookie": float64(index(t, c)),
						"csrf":   float64(index(t, s))}
				case errors.As(err, &r):
					got = map[string]any{"r": "refused",
						"msg":  r.Msg,
						"wait": float64(r.Wait)}
				case errors.As(err, &w):
					got = map[string]any{"r": "wrong",
						"msg":    w.Msg,
						"locked": w.Locked}
				}
			case "csrf":
				c := "T999999"
				if o.Cookie == nil {
					c = ""
				} else if v, ok := cookies[*o.Cookie]; ok {
					c = v
				}
				s, ok := gt.CSRFFor(c, o.Addr)
				if ok {
					got["csrf"] = float64(index(t, s))
				} else {
					got["csrf"] = nil
				}
			case "logout":
				if o.Cookie != nil {
					gt.Logout(cookies[*o.Cookie])
				}
			case "count":
				got["sessions"] = float64(gt.Sessions())
			}
			if !same(got, o.Result) {
				t.Errorf("%s op %d (%s %s at mono %v): "+
					"got %v, want %v", tr.Name, i, o.Op,
					o.Addr, clk.Mono(), got, o.Result)
			}
		}
	}
}

func wallOr(w *float64, d float64) float64 {
	if w != nil {
		return *w
	}
	return d
}

func same(a, b map[string]any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestMakePassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		p := MakePassword()
		parts := strings.Split(p, "-")
		if len(p) != 19 || len(parts) != 4 {
			t.Fatalf("shape: %d", len(p))
		}
		for _, c := range strings.ReplaceAll(p, "-", "") {
			if !strings.ContainsRune(abc, c) {
				t.Fatalf("letter %q", c)
			}
		}
		if seen[p] {
			t.Fatal("a repeat")
		}
		seen[p] = true
	}
}
