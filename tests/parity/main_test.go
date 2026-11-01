// Shared by the twin harness (twin_test.go, tag parity) and the page
// contract (page_test.go, every CI run): the programs both build, the
// fake proxy folder, and how a Front Desk is started and stopped.
package parity

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/litellm/fakelitellm"
)

const (
	master = "sk-parity-master-1"
	salt   = "sk-parity-salt-2"
	deskPW = "parity-desk-password-0003"
)

var (
	bin     string // folder with lvp, mint-key and the fake docker
	lvp     string
	python  string
	repo    string
	skipWhy string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	repo, _ = filepath.Abs("../..")
	python, skipWhy = findPython()
	for _, tool := range []string{"bash", "curl", "jq"} {
		if _, err := exec.LookPath(tool); err != nil && skipWhy == "" {
			skipWhy = tool + " is not installed"
		}
	}
	dir, err := os.MkdirTemp("", "lvp-parity")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	bin = dir
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	lvp = filepath.Join(bin, "lvp"+exe)
	for _, b := range [][2]string{{"lvp", "./cmd/lvp"},
		{"mint-key", "./cmd/mint-key"},
		{"docker", "./internal/pgread/fakedocker"}} {
		cmd := exec.Command("go", "build", "-o",
			filepath.Join(bin, b[0]+exe), b[1])
		cmd.Dir = repo
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "building", b[0]+":", err)
			return 1
		}
	}
	return m.Run()
}

// findPython is a real python3, 3.11 or newer (a Windows python3 may
// be the Store's stub, which only opens the Store).
func findPython() (string, string) {
	for _, name := range []string{"python3", "python"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		out, err := exec.Command(p, "-c", "import sys; "+
			"print(sys.version_info >= (3, 11))").Output()
		if err == nil && strings.TrimSpace(string(out)) == "True" {
			return p, ""
		}
	}
	return "", "no python3 3.11 or newer"
}

// syncBuf is a log both programs write to while tests read it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// side is one running Front Desk.
type side struct {
	name string // "py" or "go"
	port int
	dir  string
	fake *fakelitellm.Server
	cmd  *exec.Cmd
	out  *syncBuf // stdout: the banner
	errb *syncBuf // stderr: the log
	// a session, for the cases that need one
	cookie, csrf string
}

// mode is how both are started.
type mode struct {
	name  string
	flags []string
	pw    bool // FRONTDESK_PASSWORD set
}

var modes = []mode{
	{"local", nil, false},
	{"login", []string{"--login"}, true},
	{"lan", []string{"--lan"}, true},
	{"docker", []string{"--docker"}, true},
}

// dockerWhy says why the --docker mode cannot run here, or "".
func dockerWhy() string {
	if os.Getenv("PARITY_PG_URL") == "" {
		return "PARITY_PG_URL is not set"
	}
	err := exec.Command(python, "-c", "import psycopg").Run()
	if err != nil {
		return "psycopg is not installed"
	}
	return ""
}

var fixedNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// fixture puts the same keys in a fake.
func fixture(s *fakelitellm.Server) {
	one, two := 1.0, 2.5
	add := func(k fakelitellm.Key) { s.AddKey(k) }
	add(fakelitellm.Key{Alias: "sam", Models: []string{"local"},
		MaxBudget: &one, Spend: 0.25, BudgetDuration: "1d",
		BudgetResetAt: "2026-11-01T00:00:00Z",
		Expires:       "2026-12-01T00:00:00Z",
		Created:       "2026-10-01T09:00:00Z"})
	add(fakelitellm.Key{Alias: "game-fern", Models: []string{"local"},
		MaxBudget: &two, Spend: 2.5,
		Created: "2026-10-30T09:00:00Z"})
	add(fakelitellm.Key{Alias: "old", Models: []string{"local"},
		Expires: "2026-10-01T00:00:00Z",
		Created: "2026-09-01T09:00:00Z"})
	add(fakelitellm.Key{Alias: "stopped", Blocked: true,
		Created: "2026-10-02T09:00:00Z"})
	add(fakelitellm.Key{Alias: `<img src=x onerror=alert(1)>`,
		Created: "2026-10-03T09:00:00Z"})
	add(fakelitellm.Key{Name: "sk-...abcd",
		Created: "2026-10-04T09:00:00Z"})
}

func folder(t *testing.T, proxyURL string) string {
	dir := t.TempDir()
	write := func(rel, s string, mode os.FileMode) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("compose.yaml", "", 0o644)
	write("seats.env", "LITELLM_MASTER_KEY="+master+
		"\nLITELLM_SALT_KEY="+salt+"\nPROXY_URL="+proxyURL+
		"\nDEFAULT_MODELS=local\nDEFAULT_BUDGET=5\n"+
		"DEFAULT_DURATION=30d\n", 0o600)
	write("game/rules.toml", "[office]\nseat_prefix = \"game-\"\n"+
		"[roster]\nseats = [{ name = \"fern\" }, { name = \"ash\" }]\n",
		0o644)
	write("fd.script", `ok {"day": "2026-10-31", "rows": [{"k": `+
		`"seat", "name": "sam", "spend": 0.25, "tokens": 30, `+
		`"reqs": 2, "fails": 0}, {"k": "model", "name": "local", `+
		`"spend": 0.25, "tokens": 30, "reqs": 2, "fails": 0}]}`+"\n",
		0o644)
	for _, rel := range []string{"gui/frontdesk.py", "mint-key.sh"} {
		b, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatal(err)
		}
		write(rel, string(b), 0o755)
	}
	copyDir(t, filepath.Join(repo, "gui", "static"),
		filepath.Join(dir, "gui", "static"))
	return dir
}

func copyDir(t *testing.T, from, to string) {
	os.MkdirAll(to, 0o755)
	es, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		b, _ := os.ReadFile(filepath.Join(from, e.Name()))
		os.WriteFile(filepath.Join(to, e.Name()), b, 0o644)
	}
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func start(t *testing.T, name string, m mode) *side {
	t.Helper()
	fake := fakelitellm.Start(fakelitellm.Options{MasterKey: master,
		Models: []string{"local", "claude"},
		Now:    func() time.Time { return fixedNow }})
	t.Cleanup(fake.Close)
	fixture(fake)
	s := &side{name: name, port: freePort(t), fake: fake,
		out: &syncBuf{}, errb: &syncBuf{}}
	s.dir = folder(t, fake.URL)
	argv := append([]string{"--port", strconv.Itoa(s.port)},
		m.flags...)
	if name == "py" {
		s.cmd = exec.Command(python, append([]string{"-B",
			filepath.Join(s.dir, "gui", "frontdesk.py")},
			argv...)...)
	} else {
		s.cmd = exec.Command(lvp, append([]string{"desk"}, argv...)...)
	}
	s.cmd.Dir = s.dir
	env := []string{}
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		switch strings.ToUpper(k) {
		case "PATH", "FRONTDESK_PASSWORD", "SEATS_ENV",
			"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
			continue
		}
		env = append(env, e)
	}
	env = append(env, "PATH="+bin+string(os.PathListSeparator)+
		os.Getenv("PATH"),
		"FAKEDOCKER_SCRIPT="+filepath.Join(s.dir, "fd.script"),
		"PYTHONUNBUFFERED=1", "TZ=UTC")
	if m.pw {
		env = append(env, "FRONTDESK_PASSWORD="+deskPW)
	}
	if m.name == "docker" {
		// As compose passes seats.env in: settings in the
		// environment, SEATS_ENV an empty stand-in.
		os.WriteFile(filepath.Join(s.dir, "empty.env"), nil, 0o644)
		env = append(env, "LITELLM_MASTER_KEY="+master,
			"LITELLM_SALT_KEY="+salt, "PROXY_URL="+fake.URL,
			"DEFAULT_MODELS=local", "DEFAULT_BUDGET=5",
			"DEFAULT_DURATION=30d",
			"DATABASE_URL="+pgURL(),
			"SEATS_ENV="+filepath.Join(s.dir, "empty.env"),
			"FRONTDESK_PORT="+strconv.Itoa(s.port))
	}
	s.cmd.Env = env
	s.cmd.Stdout, s.cmd.Stderr = s.out, s.errb
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(s) })
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
		if err == nil {
			c.Close()
			// The first check of the proxy, so cards are there.
			time.Sleep(1500 * time.Millisecond)
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not start: %s %s", name, s.out, s.errb)
	return nil
}

func stop(s *side) {
	if s.cmd.Process == nil || s.cmd.ProcessState != nil {
		return
	}
	if runtime.GOOS == "windows" {
		s.cmd.Process.Kill()
	} else {
		s.cmd.Process.Signal(os.Interrupt)
	}
	done := make(chan struct{})
	go func() { s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.cmd.Process.Kill()
		<-done
	}
}

// pgURL is PARITY_PG_URL, or a database nobody answers at (the page
// contract runs --docker without one: the tally falls back).
func pgURL() string {
	if u := os.Getenv("PARITY_PG_URL"); u != "" {
		return u
	}
	return "postgresql://litellm:none@127.0.0.1:1/litellm"
}
