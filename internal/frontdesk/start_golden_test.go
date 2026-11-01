package frontdesk

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/pgread"
)

// nullTally is a tally source that never answers anything useful.
type nullTally struct{ stopped chan struct{} }

func (n *nullTally) Query(ctx context.Context,
	sql string) (json.RawMessage, error) {
	return nil, &pgread.Error{Msg: "no database in this test"}
}

func (n *nullTally) Stop() {
	select {
	case <-n.stopped:
	default:
		close(n.stopped)
	}
}

// fakeSys is a computer with env as its environment, wd as its
// folder, and Ctrl+C pressed once Front Desk is ready.
type fakeSys struct {
	mu    sync.Mutex
	env   map[string]string
	order []string
	tally *nullTally
}

func newFakeSys(env map[string]string) *fakeSys {
	f := &fakeSys{env: map[string]string{}, tally: &nullTally{
		stopped: make(chan struct{})}}
	for k, v := range env {
		f.env[k] = v
		f.order = append(f.order, k)
	}
	return f
}

func (f *fakeSys) system(wd string, ready func(net.Addr)) System {
	sig := make(chan os.Signal, 1)
	return System{
		Environ: func() []string {
			f.mu.Lock()
			defer f.mu.Unlock()
			var out []string
			for _, k := range f.order {
				if v, ok := f.env[k]; ok {
					out = append(out, k+"="+v)
				}
			}
			return out
		},
		LookupEnv: func(k string) (string, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			v, ok := f.env[k]
			return v, ok
		},
		Unsetenv: func(k string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			delete(f.env, k)
			return nil
		},
		Getwd:    func() (string, error) { return wd, nil },
		Exe:      func() (string, error) { return "/nowhere/lvp", nil },
		Hostname: func() (string, error) { return "deskhost", nil },
		LANIP:    func() string { return "<lan-ip>" },
		Prog:     func() string { return "lvp" },
		Clock:    clock.Real(),
		Stop: func() (<-chan os.Signal, func()) {
			return sig, func() {}
		},
		Ready: func(a net.Addr) {
			if ready != nil {
				ready(a)
			}
			sig <- os.Interrupt
		},
		Tally: func(bool, string, string) pgread.Querier {
			return f.tally
		},
	}
}

type startCase struct {
	Name   string
	Argv   []string
	Env    map[string]string
	Stdout string
	Stderr string
	Code   int
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

var pwRE = regexp.MustCompile(
	`Password, (shown once|new at each start): \S+`)

func maskOut(s string, port int) string {
	s = pwRE.ReplaceAllString(s, "Password, $1: <password>")
	s = strings.ReplaceAll(s, ":"+strconv.Itoa(port), ":<port>")
	s = strings.ReplaceAll(s, "--port "+strconv.Itoa(port+1),
		"--port <port+1>")
	return strings.ReplaceAll(s, "port "+strconv.Itoa(port)+":",
		"port <port>:")
}

// The usage text differs (ledger F-18); the error line must not.
func errorLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if i := strings.Index(l, ": error: "); i >= 0 {
			return l[i+len(": error: "):]
		}
	}
	return ""
}

func TestGoldenStart(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "golden",
		"start.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct{ Starts []startCase }
	json.Unmarshal(b, &g)
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "compose.yaml"), nil, 0o644)
	os.WriteFile(filepath.Join(tmp, "seats.env"), []byte(
		"LITELLM_MASTER_KEY=sk-fake-start-master\n"+
			"LITELLM_SALT_KEY=sk-fake-start-salt\n"+
			"PROXY_URL=http://127.0.0.1:9\n"), 0o600)
	os.WriteFile(filepath.Join(tmp, "nomaster.env"), []byte(
		"PROXY_URL=http://127.0.0.1:9\n"), 0o600)
	for _, c := range g.Starts {
		if c.Name == "port-in-use" {
			continue // TestPortInUse
		}
		port := freePort(t)
		argv := fixPaths(c.Argv, tmp)
		for i := 0; i+1 < len(argv); i++ {
			if argv[i] == "--port" {
				next := argv[i+1]
				if _, err := strconv.Atoi(next); err == nil {
					argv[i+1] = strconv.Itoa(port)
				}
			}
		}
		fs := newFakeSys(c.Env)
		fs.env["LANG"] = "C.UTF-8"
		var out, errb bytes.Buffer
		code := Run(argv, args.Std{Out: &out, Err: &errb},
			fs.system(tmp, nil))
		gotOut := maskOut(out.String(), port)
		gotErr := strings.ReplaceAll(maskOut(errb.String(), port), tmp,
			"<tmp>")
		switch {
		case c.Code == 2:
			if code != 2 || errorLine(gotErr) != errorLine(
				c.Stderr) {
				t.Errorf("%s: %d %q, want 2 %q", c.Name, code,
					errorLine(gotErr), errorLine(c.Stderr))
			}
		case c.Code == 1:
			if code != 1 || strings.TrimSpace(gotErr) != c.Stderr ||
				gotOut != "" {
				t.Errorf("%s: %d %q %q, want 1 %q", c.Name,
					code,
					gotOut, gotErr, c.Stderr)
			}
		default:
			// Python's Ctrl+C sometimes ended in a traceback (exit
			// -2, no empty line): Go always stops cleanly (F-20).
			want := c.Stdout
			if i := strings.Index(want, "Stop it with"); i >= 0 {
				want = want[:i] +
					want[i:][:strings.Index(want[i:],
						"\n")+1]
			}
			if code != 0 || gotOut != want+"\n" {
				t.Errorf("%s: %d\n%s\nwant\n%s", c.Name, code,
					gotOut,
					want)
			}
			if errb.Len() != 0 && !strings.Contains(errb.String(),
				"check failed") {
				t.Logf("%s: stderr %q", c.Name, errb.String())
			}
			select {
			case <-fs.tally.stopped:
			default:
				t.Errorf("%s: the tally source was not stopped",
					c.Name)
			}
			if _, ok := fs.env["FRONTDESK_PASSWORD"]; ok {
				t.Errorf("%s: FRONTDESK_PASSWORD still set",
					c.Name)
			}
		}
	}
}

// fixPaths turns the golden's temporary paths into this test's.
func fixPaths(argv []string, tmp string) []string {
	var out []string
	for _, a := range argv {
		if strings.HasSuffix(a, ".env") {
			parts := strings.Split(a, "/")
			n := parts[len(parts)-1]
			if len(parts) >= 2 && parts[len(parts)-2] == "nope" {
				a = filepath.Join(tmp, "nope", n)
			} else {
				a = filepath.Join(tmp, n)
			}
		}
		out = append(out, a)
	}
	return out
}

func TestGoldenPortInUse(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("testdata", "golden", "start.json"))
	var g struct{ Starts []startCase }
	json.Unmarshal(b, &g)
	var want string
	for _, c := range g.Starts {
		if c.Name == "port-in-use" {
			want = c.Stderr
		}
	}
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "compose.yaml"), nil, 0o644)
	os.WriteFile(filepath.Join(tmp, "seats.env"), []byte(
		"LITELLM_MASTER_KEY=sk-fake-start-master\n"), 0o600)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	var out, errb bytes.Buffer
	fs := newFakeSys(nil)
	code := Run([]string{"--port", strconv.Itoa(port)},
		args.Std{Out: &out, Err: &errb}, fs.system(tmp, nil))
	got := strings.TrimSpace(maskOut(errb.String(), port))
	if runtime.GOOS == "windows" {
		// Windows' own words for the error (ledger F-19).
		if code != 1 || !strings.HasPrefix(got, "cannot use port "+
			"<port>: ") || !strings.HasSuffix(got, "Try --port "+
			"<port+1>.") {
			t.Errorf("%d %q", code, got)
		}
		return
	}
	if code != 1 || got != want {
		t.Errorf("%d %q, want %q", code, got, want)
	}
}
