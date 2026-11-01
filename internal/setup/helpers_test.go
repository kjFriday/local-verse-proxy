package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/fsx"
	"github.com/kjFriday/local-verse-proxy/internal/litellm/fakelitellm"
)

// fakeDockerDir holds pgread's fake docker, built once.
var fakeDockerDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "setup-fakedocker")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exe := "docker"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, exe),
		"../pgread/fakedocker")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building fakedocker:", err)
		os.Exit(1)
	}
	fakeDockerDir = dir
	// No test may reach a real web proxy.
	for _, n := range []string{"HTTPS_PROXY", "https_proxy",
		"HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		os.Unsetenv(n)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// The test master key; "test" marks it as made up.
const masterKey = "sk-test-master-key-0001"

// call is one docker run a test's System made.
type call struct {
	dir  string
	args []string
}

// world is one test's computer: a proxy folder, its environment, a
// fake docker and a fake clock.
type world struct {
	t   *testing.T
	dir string
	env map[string]string
	sys *System

	mu     sync.Mutex
	calls  []call
	answer func(args []string) (string, string, error)
	now    time.Time
	onWait func(n int)
	waits  int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, dir: t.TempDir(), env: map[string]string{},
		now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	write(t, filepath.Join(w.dir, "compose.yaml"), "services: {}\n")
	tmpl, err := os.ReadFile(filepath.Join("..", "..",
		"seats.env.example"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(w.dir, "seats.env.example"), string(tmpl))
	w.answer = func([]string) (string, string, error) {
		return "", "", nil
	}
	w.sys = &System{
		Getwd: func() (string, error) { return w.dir, nil },
		Exe: func() (string, error) {
			return filepath.Join(w.dir, "lvp"), nil
		},
		Env: func(n string) (string, bool) {
			v, ok := w.env[n]
			return v, ok
		},
		Docker: w.docker,
		Sleep: func(ctx context.Context, d time.Duration) error {
			w.mu.Lock()
			w.now = w.now.Add(d)
			w.waits++
			n, hook := w.waits, w.onWait
			w.mu.Unlock()
			if hook != nil {
				hook(n)
			}
			return ctx.Err()
		},
		Now: func() time.Time {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.now
		},
		Rand: rand.Reader,
		Prog: "./lvp",
		GOOS: runtime.GOOS,
	}
	return w
}

func (w *world) docker(ctx context.Context, dir string,
	stdout, stderr io.Writer, argv ...string) error {
	w.mu.Lock()
	w.calls = append(w.calls, call{dir, append([]string(nil),
		argv...)})
	answer := w.answer
	w.mu.Unlock()
	out, errOut, err := answer(argv)
	io.WriteString(stdout, out)
	io.WriteString(stderr, errOut)
	return err
}

// dockerCalls are the docker runs so far, each as one line.
func (w *world) dockerCalls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, c := range w.calls {
		if c.dir != w.dir {
			w.t.Errorf("docker ran in %s, not the proxy folder",
				c.dir)
		}
		out = append(out, strings.Join(c.args, " "))
	}
	return out
}

// run runs one helper; its stdout, stderr and exit code.
func (w *world) run(f func(context.Context, []string, args.Std) int,
	argv ...string) (string, string, int) {
	var o, e bytes.Buffer
	code := f(context.Background(), argv,
		args.Std{In: strings.NewReader(""), Out: &o, Err: &e})
	checkWidth(w.t, o.String()+e.String())
	return o.String(), e.String(), code
}

// seats writes seats.env, private, for a proxy at url.
func (w *world) seats(url string, extra ...string) {
	text := "LITELLM_MASTER_KEY=" + masterKey + "\n" +
		"LITELLM_SALT_KEY=sk-test-salt-0001\n" +
		"POSTGRES_PASSWORD=testpw\n" +
		"DATABASE_URL=postgresql://litellm:testpw@db:5432/litellm\n" +
		"PROXY_URL=" + url + "\n" +
		"DEFAULT_MODELS=local\nDEFAULT_BUDGET=5\n" +
		"DEFAULT_DURATION=30d\n" + strings.Join(extra, "")
	p := filepath.Join(w.dir, "seats.env")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		w.t.Fatal(err)
	}
	// On Windows, an access list of its own (check looks).
	if err := fsx.Private(p); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) read(name string) string {
	b, err := os.ReadFile(filepath.Join(w.dir, name))
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkWidth: every line a helper prints is 80 columns or less, and
// ASCII.
func checkWidth(t *testing.T, out string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if len(l) > 80 {
			t.Errorf("line over 80: %q", l)
		}
		for _, r := range l {
			if r > 126 || r < 32 && r != '\t' {
				t.Errorf("not ASCII: %q", l)
				break
			}
		}
	}
}

// fake starts a fake proxy that knows local and claude.
func fake(t *testing.T) *fakelitellm.Server {
	s := fakelitellm.Start(fakelitellm.Options{MasterKey: masterKey,
		Models: []string{"local", "claude"}})
	t.Cleanup(s.Close)
	return s
}

// ollama is a stand-in Ollama answering as the real one does.
func ollama(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "Ollama is running")
		}))
	t.Cleanup(s.Close)
	return s
}

// notFound is the error exec gives for a missing docker.
var notFound = &exec.Error{Name: "docker", Err: exec.ErrNotFound}

// exit1 is a docker run that exited 1.
var exit1 = errors.New("exit status 1")

// flat is text with its line breaks and runs of spaces as one space.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }
