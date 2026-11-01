package frontdesk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/litellm/fakelitellm"
)

// The test binary is also `lvp desk`: run with FRONTDESK_TEST_CHILD
// set, it is Front Desk, with the arguments in FRONTDESK_TEST_ARGS.
// So the end-to-end tests need no other package built.
var fakeDockerDir string

func TestMain(m *testing.M) {
	if os.Getenv("FRONTDESK_TEST_CHILD") == "1" {
		var argv []string
		json.Unmarshal([]byte(os.Getenv("FRONTDESK_TEST_ARGS")), &argv)
		os.Unsetenv("FRONTDESK_TEST_CHILD")
		os.Exit(Main(argv, args.OS()))
	}
	dir, err := os.MkdirTemp("", "fd-fakedocker")
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
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const e2eMaster = "sk-fake-e2e-master-key-0001"

// child is one running `lvp desk` child.
type child struct {
	t      *testing.T
	cmd    *exec.Cmd
	port   int
	banner string
	out    *strings.Builder
	errb   *syncBuf
	done   chan struct{}
	dir    string
}

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
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

// proxyDir is a proxy folder: compose.yaml, seats.env, a game folder
// and a fakedocker script that answers the tally.
func proxyDir(t *testing.T, proxyURL string) string {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.yaml"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "seats.env"), []byte(
		"LITELLM_MASTER_KEY="+e2eMaster+"\nLITELLM_SALT_KEY=sk-fake-"+
			"e2e-salt\nPROXY_URL="+proxyURL+
			"\nDEFAULT_MODELS=local\n"),
		0o600)
	os.MkdirAll(filepath.Join(dir, "game"), 0o755)
	os.WriteFile(filepath.Join(dir, "game", "rules.toml"), []byte(
		"[office]\nseat_prefix = \"game-\"\n[roster]\n"+
			"seats = [{ name = \"fern\" }]\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "fd.script"), []byte(
		`ok {"day": "2026-10-31", "rows": [{"k": "seat", "name": `+
			`"sam", "spend": 0.5, "tokens": 3, "reqs": 1, `+
			`"fails": 0}]}`+"\n"), 0o644)
	return dir
}

func startDesk(t *testing.T, dir string, env []string,
	argv ...string) *child {
	t.Helper()
	port := freePort(t)
	argv = append(argv, "--port", strconv.Itoa(port))
	b, _ := json.Marshal(argv)
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FRONTDESK_TEST_CHILD=1",
		"FRONTDESK_TEST_ARGS="+string(b),
		"PATH="+fakeDockerDir+string(os.PathListSeparator)+
			os.Getenv("PATH"),
		"FAKEDOCKER_SCRIPT="+filepath.Join(dir, "fd.script"),
		"FAKEDOCKER_LOG="+filepath.Join(dir, "fdlog"))
	cmd.Env = append(cmd.Env, env...)
	stdout, _ := cmd.StdoutPipe()
	d := &child{t: t, cmd: cmd, port: port, out: &strings.Builder{},
		errb: &syncBuf{}, done: make(chan struct{}), dir: dir}
	cmd.Stderr = d.errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	go func() {
		r := bufio.NewReader(stdout)
		said := false
		for {
			line, err := r.ReadString('\n')
			d.out.WriteString(line)
			if !said && strings.HasPrefix(line, "Stop it with") {
				d.banner = d.out.String()
				said = true
				close(ready)
			}
			if err != nil {
				break
			}
		}
		cmd.Wait()
		close(d.done)
		if !said {
			close(ready)
		}
	}()
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("no banner; stderr %q", d.errb.String())
	}
	if d.banner == "" {
		t.Fatalf("it stopped: %q", d.errb.String())
	}
	t.Cleanup(func() {
		select {
		case <-d.done:
		default:
			cmd.Process.Kill()
			<-d.done
		}
	})
	return d
}

func (d *child) url(path string) string {
	return "http://127.0.0.1:" + strconv.Itoa(d.port) + path
}

func (d *child) do(method, path, body, csrf, cookie string) (int,
	map[string]any, http.Header) {
	d.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, d.url(path), r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if cookie != "" {
		req.Header.Set("Cookie", "fd_"+strconv.Itoa(d.port)+"="+cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	defer resp.Body.Close()
	var j map[string]any
	b, _ := io.ReadAll(resp.Body)
	json.Unmarshal(b, &j)
	return resp.StatusCode, j, resp.Header
}

// stop presses Ctrl+C (or sends sig), and gives the exit code.
func (d *child) stop(sig os.Signal) int {
	d.t.Helper()
	if err := d.cmd.Process.Signal(sig); err != nil {
		d.t.Fatal(err)
	}
	select {
	case <-d.done:
	case <-time.After(10 * time.Second):
		d.cmd.Process.Kill()
		d.t.Fatal("it did not stop")
	}
	return d.cmd.ProcessState.ExitCode()
}

// waitFor polls /api/state until ok holds.
func (d *child) waitFor(cookie string, ok func(map[string]any) bool) {
	d.t.Helper()
	end := time.Now().Add(20 * time.Second)
	for time.Now().Before(end) {
		_, j, _ := d.do("GET", "/api/state", "", "", cookie)
		if j != nil && ok(j) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	d.t.Fatal("the state never got there")
}

func seatsOf(j map[string]any) map[string]bool {
	out := map[string]bool{}
	cards, _ := j["cards"].([]any)
	for _, c := range cards {
		out[c.(map[string]any)["seat"].(string)] = true
	}
	return out
}

// psqlRunning says whether a fakedocker child of this test still
// runs (Linux: from /proc).
func psqlRunning(dir string) bool {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(),
			"environ"))
		if err == nil && strings.Contains(string(b),
			"FAKEDOCKER_SCRIPT="+filepath.Join(dir, "fd.script")) {
			cmd, _ := os.ReadFile(filepath.Join("/proc", e.Name(),
				"cmdline"))
			if strings.Contains(string(cmd), "psql") {
				return true
			}
		}
	}
	return false
}

func TestEndToEndLocal(t *testing.T) {
	fake := fakelitellm.Start(fakelitellm.Options{MasterKey: e2eMaster,
		Models: []string{"local", "big"}, Version: "1.106.6"})
	defer fake.Close()
	fake.AddKey(fakelitellm.Key{Alias: "sam", Models: []string{"local"}})
	dir := proxyDir(t, fake.URL)
	d := startDesk(t, dir, nil)
	if !strings.Contains(d.banner, "Anyone logged in to this computer") {
		t.Fatalf("banner %q", d.banner)
	}
	code, s, _ := d.do("GET", "/api/session", "", "", "")
	csrf, _ := s["csrf"].(string)
	if code != 200 || csrf == "" || s["need_login"] != false ||
		!strings.HasSuffix(s["reset_cmd"].(string), " game reset") {
		t.Fatalf("session %d %v", code, s)
	}
	d.waitFor("", func(j map[string]any) bool {
		tl, _ := j["tally"].(map[string]any)
		return seatsOf(j)["sam"] && tl != nil &&
			tl["source"] == "database"
	})
	// Mint in process, through mint-key's rules, against the fake.
	code, m, h := d.do("POST", "/api/mint", `{"seat": "laptop-2", `+
		`"models": ["local"], "budget": "2.50", "duration": "30d", `+
		`"refill": "1w"}`, csrf, "")
	key, _ := m["key"].(string)
	if code != 200 || !strings.HasPrefix(key, "sk-") ||
		h.Get("Content-Type") != "application/json" {
		t.Fatalf("mint %d %v", code, m)
	}
	_, st, _ := d.do("GET", "/api/state", "", "", "")
	if !seatsOf(st)["laptop-2"] {
		t.Fatal("a new card did not show at once")
	}
	var made *fakelitellm.Key
	for _, k := range fake.Keys() {
		if k.Alias == "laptop-2" {
			k := k
			made = &k
		}
	}
	if made == nil || made.BudgetDuration != "1w" || made.MaxBudget ==
		nil || *made.MaxBudget != 2.5 {
		t.Fatalf("minted %+v", made)
	}
	code, m, _ = d.do("POST", "/api/mint", `{"seat": "LAPTOP-2", `+
		`"models": ["local"], "budget": "1", "duration": "1d"}`, csrf,
		"")
	if code != 409 || m["field"] != "seat" {
		t.Fatalf("again %d %v", code, m)
	}
	code, m, _ = d.do("POST", "/api/mint", `{"seat": "game-oak", `+
		`"models": ["local"], "budget": "1", "duration": "1d"}`, csrf,
		"")
	if code != 400 || !strings.HasPrefix(m["error"].(string),
		"Names that start with game- are kept") {
		t.Fatalf("game %d %v", code, m)
	}
	code, m, _ = d.do("POST", "/api/revoke", `{"seat": "laptop-2"}`,
		csrf, "")
	if code != 200 || m["ok"] != true {
		t.Fatalf("revoke %d %v", code, m)
	}
	_, st, _ = d.do("GET", "/api/state", "", "", "")
	if seatsOf(st)["laptop-2"] {
		t.Fatal("a cancelled card stayed")
	}
	// The proxy refuses the master key: in plain words.
	fake.Fail("/key/generate", 401, `{"error": {"message": `+
		`"Authentication Error, bad key"}}`)
	code, m, _ = d.do("POST", "/api/mint", `{"seat": "x1", "models": `+
		`["local"], "budget": "1", "duration": "1d"}`, csrf, "")
	if code != 400 || !strings.HasPrefix(m["error"].(string),
		"The proxy did not accept the master key.") {
		t.Fatalf("401 %d %v", code, m)
	}
	if runtime.GOOS == "linux" && !psqlRunning(dir) {
		t.Fatal("psql does not run (or the check cannot see it)")
	}
	if runtime.GOOS == "windows" {
		d.cmd.Process.Kill()
		return // no Ctrl+C to send a child here
	}
	if c := d.stop(os.Interrupt); c != 0 {
		t.Fatalf("exit %d", c)
	}
	if !strings.HasSuffix(d.out.String(), "Ctrl+C.\n\n") {
		t.Fatalf("stdout %q", d.out.String())
	}
	log := d.errb.String()
	for _, secret := range []string{e2eMaster, "sk-fake-e2e-salt", key} {
		if strings.Contains(log, secret) {
			t.Fatalf("a secret reached the log: %q", log)
		}
	}
	for _, want := range []string{"minted a key card for seat laptop-2",
		"cancelled the key card for seat laptop-2",
		"mint failed for seat x1"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q: %q", want, log)
		}
	}
	if runtime.GOOS == "linux" {
		time.Sleep(200 * time.Millisecond)
		if psqlRunning(dir) {
			t.Fatal("psql still runs after Ctrl+C")
		}
	}
}

var bannerPW = regexp.MustCompile(`Password, shown once: (\S+)`)

func TestEndToEndLogin(t *testing.T) {
	fake := fakelitellm.Start(fakelitellm.Options{MasterKey: e2eMaster,
		Models: []string{"local"}})
	defer fake.Close()
	dir := proxyDir(t, fake.URL)
	d := startDesk(t, dir, nil, "--login")
	m := bannerPW.FindStringSubmatch(d.banner)
	if m == nil {
		t.Fatalf("banner %q", d.banner)
	}
	pw := m[1]
	code, _, _ := d.do("GET", "/api/state", "", "", "")
	if code != 401 {
		t.Fatalf("state before login: %d", code)
	}
	code, j, _ := d.do("POST", "/api/login", `{"password": "nope"}`, "",
		"")
	if code != 401 || !strings.HasPrefix(j["error"].(string),
		"Wrong password. 4 tries left") {
		t.Fatalf("wrong %d %v", code, j)
	}
	time.Sleep(1100 * time.Millisecond)
	code, j, h := d.do("POST", "/api/login", `{"password": "`+pw+`"}`, "",
		"")
	if code != 200 {
		t.Fatalf("login %d %v", code, j)
	}
	sc := h.Get("Set-Cookie")
	cookie := strings.TrimPrefix(strings.Split(sc, ";")[0],
		"fd_"+strconv.Itoa(d.port)+"=")
	if !strings.HasSuffix(sc, "; Max-Age=43200; Path=/; HttpOnly; "+
		"SameSite=Strict") || len(cookie) != 43 {
		t.Fatalf("cookie %q", sc)
	}
	code, _, _ = d.do("GET", "/api/state", "", "", cookie)
	if code != 200 {
		t.Fatalf("state after login: %d", code)
	}
	if strings.Contains(d.errb.String(), pw) {
		t.Fatal("the password reached the log")
	}
	if runtime.GOOS == "windows" {
		return
	}
	// SIGTERM stops it as Ctrl+C does (ledger F-20).
	if c := d.stop(syscall.SIGTERM); c != 0 {
		t.Fatalf("SIGTERM: exit %d", c)
	}
	if runtime.GOOS == "linux" {
		time.Sleep(200 * time.Millisecond)
		if psqlRunning(dir) {
			t.Fatal("psql still runs after SIGTERM")
		}
	}
}

func TestEndToEndLAN(t *testing.T) {
	fake := fakelitellm.Start(fakelitellm.Options{MasterKey: e2eMaster,
		Models: []string{"local"}})
	defer fake.Close()
	dir := proxyDir(t, fake.URL)
	pw := "a-given-password-12"
	d := startDesk(t, dir, []string{"FRONTDESK_PASSWORD=" + pw}, "--lan",
		"--host", "desk.example")
	if !strings.Contains(d.banner, "  On your network:  http://") ||
		!strings.Contains(d.banner, "Password: the one in "+
			"FRONTDESK_PASSWORD\n") {
		t.Fatalf("banner %q", d.banner)
	}
	code, s, _ := d.do("GET", "/api/session", "", "", "")
	if code != 200 || s["lan"] != true || s["need_login"] != true ||
		s["csrf"] != nil {
		t.Fatalf("session %d %v", code, s)
	}
	code, j, h := d.do("POST", "/api/login", `{"password": "`+pw+`"}`, "",
		"")
	if code != 200 || h.Get("Set-Cookie") == "" {
		t.Fatalf("login %d %v", code, j)
	}
	// The psql child never sees the password.
	logs, _ := filepath.Glob(filepath.Join(dir, "fdlog", "*.json"))
	if len(logs) == 0 {
		time.Sleep(time.Second)
		logs, _ = filepath.Glob(filepath.Join(dir, "fdlog", "*.json"))
	}
	for _, l := range logs {
		b, _ := os.ReadFile(l)
		if strings.Contains(string(b), "FRONTDESK_PASSWORD") {
			t.Fatalf("psql's environment has "+
				"FRONTDESK_PASSWORD: %s", b)
		}
	}
	if len(logs) == 0 {
		t.Fatal("psql never ran")
	}
	req, _ := http.NewRequest("GET", d.url("/api/session"), nil)
	req.Host = "desk.example:" + strconv.Itoa(d.port)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("--host name: %v %v", err, resp)
	}
	resp.Body.Close()
	req.Host = "evil.example:" + strconv.Itoa(d.port)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 421 {
		t.Fatalf("other name: %d", resp.StatusCode)
	}
}
