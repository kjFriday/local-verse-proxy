package game

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/fsx"
	"github.com/kjFriday/local-verse-proxy/internal/litellm/fakelitellm"
)

const fakeMaster = "sk-fake-master-key-for-game-tests"

// practice is a fake proxy and its database for a whole game: the
// fake LiteLLM, a front for chat calls that writes the spend log, and
// a docker for the test program that answers psql from both.
type practice struct {
	t     *testing.T
	lite  *fakelitellm.Server
	chat  *httptest.Server
	psql  *httptest.Server
	mu    sync.Mutex
	logs  []spendLog
	cost  float64
	stdin []string
}

type spendLog struct {
	alias, seat, quest string
	tags               []string
	ok                 bool
	tokens             int64
}

var totalRE = regexp.MustCompile(`"total_tokens":\s*(\d+)`)

func newPractice(t *testing.T) *practice {
	p := &practice{t: t, cost: 0.000001}
	p.lite = fakelitellm.Start(fakelitellm.Options{MasterKey: fakeMaster,
		Models: []string{"local", "pro"}})
	t.Cleanup(p.lite.Close)
	p.chat = httptest.NewServer(http.HandlerFunc(p.serveChat))
	t.Cleanup(p.chat.Close)
	p.psql = httptest.NewServer(http.HandlerFunc(p.servePsql))
	t.Cleanup(p.psql.Close)
	return p
}

// serveChat passes a chat call to the fake proxy and logs it, as
// LiteLLM_SpendLogs would.
func (p *practice) serveChat(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req, _ := http.NewRequest("POST", p.lite.URL+r.URL.Path,
		bytes.NewReader(body))
	req.Header = r.Header.Clone()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.WriteHeader(502)
		return
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var in struct {
		Metadata struct{ Tags []string } `json:"metadata"`
	}
	json.Unmarshal(body, &in)
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	log := spendLog{tags: in.Metadata.Tags, ok: resp.StatusCode == 200}
	for _, k := range p.lite.Keys() {
		if k.Key == key {
			log.alias = k.Alias
		}
	}
	for _, tag := range in.Metadata.Tags {
		if strings.HasPrefix(tag, "seat:") && log.seat == "" {
			log.seat = tag
		}
		if strings.HasPrefix(tag, "quest:") && log.quest == "" {
			log.quest = tag
		}
	}
	if m := totalRE.FindSubmatch(data); m != nil {
		log.tokens, _ = strconv.ParseInt(string(m[1]), 10, 64)
	}
	p.mu.Lock()
	p.logs = append(p.logs, log)
	p.mu.Unlock()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(data)
}

var (
	innerRE = regexp.MustCompile(`(?s)FROM \((.*)\) q;`)
	aliasRE = regexp.MustCompile(`key_alias IN \((.*)\)$`)
	tagsRE  = regexp.MustCompile(`request_tags @> '([^']*)'::jsonb`)
)

// servePsql is the database's answer to one psql run by the game.
func (p *practice) servePsql(w http.ResponseWriter, r *http.Request) {
	var c dockerCall
	json.NewDecoder(r.Body).Decode(&c)
	p.mu.Lock()
	p.stdin = append(p.stdin, c.Stdin)
	p.mu.Unlock()
	if strings.Join(c.Argv, " ") != strings.Join(OnceArgvForTest()[1:],
		" ") {
		json.NewEncoder(w).Encode(dockerAnswer{Stderr: "bad argv " +
			strings.Join(c.Argv, " "), Code: 1})
		return
	}
	m := innerRE.FindStringSubmatch(c.Stdin)
	if m == nil {
		json.NewEncoder(w).Encode(dockerAnswer{Stderr: "no query",
			Code: 3})
		return
	}
	rows := p.answer(m[1])
	b, _ := json.Marshal(rows)
	json.NewEncoder(w).Encode(dockerAnswer{Stdout: string(b) + "\n"})
}

func (p *practice) answer(sql string) []map[string]any {
	rows := []map[string]any{}
	if strings.Contains(sql, `"LiteLLM_VerificationToken"`) {
		// LiteLLM's reset job: the fake runs it on any request.
		if resp, err := http.Get(p.lite.URL +
			"/health/liveliness"); err ==
			nil {
			resp.Body.Close()
		}
		want := map[string]bool{}
		for _, a := range strings.Split(aliasRE.FindStringSubmatch(
			sql)[1], ", ") {
			want[strings.Trim(a, "'")] = true
		}
		for _, k := range p.lite.Keys() {
			if !want[k.Alias] {
				continue
			}
			at, _ := time.Parse(time.RFC3339, k.BudgetResetAt)
			rows = append(rows, map[string]any{"seat": k.Alias,
				"spend": k.Spend, "reset_at": json.Number(
					fmt.Sprintf("%d.000000", at.Unix()))})
		}
		return rows
	}
	var tags []string
	json.Unmarshal([]byte(tagsRE.FindStringSubmatch(sql)[1]), &tags)
	p.mu.Lock()
	defer p.mu.Unlock()
	var hits []spendLog
	for _, l := range p.logs {
		if l.ok && has(l.tags, tags[0]) && has(l.tags, tags[1]) {
			hits = append(hits, l)
		}
	}
	if strings.Contains(sql, "count(*) AS n FROM") {
		return []map[string]any{{"n": len(hits)}}
	}
	type group struct {
		alias, seat, quest string
		calls, tokens      int64
		spend              float64
	}
	var groups []*group
	for _, l := range hits {
		var g *group
		for _, x := range groups {
			if x.alias == l.alias && x.seat == l.seat &&
				x.quest == l.quest {
				g = x
			}
		}
		if g == nil {
			g = &group{alias: l.alias, seat: l.seat, quest: l.quest}
			groups = append(groups, g)
		}
		g.calls++
		g.tokens += l.tokens
		g.spend += float64(float64(l.tokens) * p.cost)
	}
	for _, g := range groups {
		rows = append(rows, map[string]any{"alias": g.alias,
			"seat": g.seat, "quest": g.quest, "calls": g.calls,
			"spend": g.spend, "tokens": g.tokens})
	}
	return rows
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// OnceArgvForTest is pgread.OnceArgv, as the game's psql runs.
func OnceArgvForTest() []string {
	return []string{"docker", "compose", "exec", "-T", "db", "psql",
		"-U", "litellm", "-d", "litellm", "-X", "-q", "-A", "-t", "-v",
		"ON_ERROR_STOP=1"}
}

// fakeDocker puts the test program first on PATH as docker.
func (p *practice) fakeDocker(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "docker"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), b, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GAMETEST_DOCKER_URL", p.psql.URL)
}

// practiceRules is a 2-second day for three seats, with quick bots.
func practiceRules(t *testing.T) string {
	b, err := os.ReadFile(filepath.Join("..", "..", "game", "rules.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for old, new := range map[string]string{
		`length = "2m"`:      `length = "2s"`,
		`close = "35s"`:      `close = "1s"`,
		"per_week = 5":       "per_week = 2",
		`flush_wait = "60s"`: `flush_wait = "3s"`,
		"pause = [1.0, 2.0]": "pause = [0.02, 0.05]",
		"pause = [0.2, 0.6]": "pause = [0.02, 0.05]",
		"pause = [3.0, 6.0]": "pause = [0.02, 0.05]",
	} {
		s = strings.ReplaceAll(s, old, new)
	}
	i := strings.Index(s, "seats = [")
	return s[:i] + "seats = [\n" +
		`  {name = "fern", persona = "frugal", guild = "owls"},` +
		"\n" +
		`  {name = "bea", persona = "balanced", guild = "owls"},` +
		"\n" +
		`  {name = "spike", persona = "spammy", guild = "foxes"},` +
		"\n]\n"
}

// proxyFolder makes a proxy folder: compose.yaml, seats.env naming
// the fake proxy, game/rules.toml.
func proxyFolder(t *testing.T, url, rulesText string) string {
	root := filepath.Join(t.TempDir(), "proxy folder")
	os.MkdirAll(filepath.Join(root, "game"), 0o755)
	os.WriteFile(filepath.Join(root, "compose.yaml"), nil, 0o644)
	env := "LITELLM_MASTER_KEY=" + fakeMaster + "\nLITELLM_SALT_KEY=" +
		"sk-fake-salt-for-game-tests\nPROXY_URL=" + url + "\n"
	if err := fsx.WriteFile(filepath.Join(root, "seats.env"), []byte(env),
		true); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "game", "rules.toml"),
		[]byte(rulesText), 0o644)
	return root
}

func lvpArgv(t *testing.T) []string {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAMETEST_AS", "lvp")
	return []string{exe, "mint-key"}
}

func runGame(t *testing.T, o Options, argv ...string) (string, string,
	int) {
	var out, errb bytes.Buffer
	code := Run(context.Background(), argv, args.Std{Out: &out,
		Err: &errb}, o)
	return out.String(), errb.String(), code
}

// TestPracticeWeek plays a whole week of two 2-second days against
// the fake proxy, end to end: setup mints through a child lvp
// mint-key, run plays and scores, board shows it, reset revokes and
// wipes.
func TestPracticeWeek(t *testing.T) {
	p := newPractice(t)
	p.fakeDocker(t)
	root := proxyFolder(t, p.lite.URL, practiceRules(t))
	o := Options{Dir: root, MintArgv: lvpArgv(t), Prog: "lvp",
		Getenv: func(k string) string {
			if k == "LVG_PROXY_URL" {
				return p.chat.URL
			}
			return ""
		}}
	out, errs, code := runGame(t, o, "setup")
	if code != 0 || !strings.Contains(out, "Minting 3 game seats: 1500 "+
		"mana ($0.015) each, refilled every 2s.") {
		t.Fatalf("setup: %d\n%s%s", code, out, errs)
	}
	for _, k := range p.lite.Keys() {
		if k.BudgetDuration != "2s" || k.MaxBudget == nil ||
			*k.MaxBudget != 0.015 || strings.Join(k.Models, ",") !=
			"local,pro" {
			t.Errorf("key %s: %+v", k.Alias, k)
		}
	}
	for _, s := range []string{"fern", "bea", "spike"} {
		path := filepath.Join(root, "game", "keys", "game-"+s+".key")
		b, err := os.ReadFile(path)
		if err != nil || !strings.HasPrefix(string(b), "sk-") {
			t.Fatalf("key file %s: %q %v", s, b, err)
		}
		if ok, why, _ := fsx.IsPrivate(path); !ok {
			t.Errorf("%s is not private: %s", path, why)
		}
	}
	out, errs, code = runGame(t, o, "run", "--days", "3")
	if code != 0 {
		t.Fatalf("run: %d\n%s%s", code, out, errs)
	}
	t.Log(out)
	for _, want := range []string{"Day 1 ", "Day 1 done: bots worked",
		"Day 2 done", "The week is over (2 days). Start a new one " +
			"with" +
			"\n`lvp game reset`, then `lvp game setup`.\n",
		"WHO FINISHED WORK", "GOODHART CHECK"} {
		if !strings.Contains(out, want) {
			t.Errorf("run: no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "INCOMPLETE") {
		t.Errorf("spend logs incomplete:\n%s", out)
	}
	board, errs, code := runGame(t, o, "board")
	if code != 0 || !strings.Contains(board, "after day 2 of 2 (2s days)") {
		t.Fatalf("board: %d\n%s%s", code, board, errs)
	}
	seat, _, code := runGame(t, o, "board", "-s", "fern", "-d", "1")
	if code != 0 || !strings.Contains(seat, "fern (frugal, guild owls)") {
		t.Errorf("board -s: %d\n%s", code, seat)
	}
	pythonBoard(t, root, board)
	out, errs, code = runGame(t, o, "reset")
	if code != 0 || out != "Revoked 3 game seats.\nGame state wiped. "+
		"New week: lvp game setup\n" {
		t.Errorf("reset: %d %q %q", code, out, errs)
	}
	if len(p.lite.Keys()) != 0 {
		t.Errorf("keys left: %+v", p.lite.Keys())
	}
	if files, _ := filepath.Glob(filepath.Join(root, "game",
		"office.db*")); len(files) != 0 {
		t.Errorf("left: %v", files)
	}
	if keys := listDir(filepath.Join(root, "game", "keys")); len(keys) !=
		0 {
		t.Errorf("key files left: %v", keys)
	}
}

// pythonBoard: office.py's board, on the office.db Go wrote, prints
// what Go's printed. Skipped without Python 3.11 or newer.
func pythonBoard(t *testing.T, root, want string) {
	py := python(t)
	if py == "" {
		t.Log("no Python 3.11 here: the cross-check is skipped")
		return
	}
	dir := filepath.Join(t.TempDir(), "game")
	os.MkdirAll(dir, 0o755)
	src, err := os.ReadFile(filepath.Join("..", "..", "game", "office.py"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "office.py"), src, 0o644)
	for _, f := range []string{"rules.toml", "office.db"} {
		b, _ := os.ReadFile(filepath.Join(root, "game", f))
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	out, err := exec.Command(py, "-B", filepath.Join(dir, "office.py"),
		"board").CombinedOutput()
	if err != nil {
		t.Fatalf("python board: %v\n%s", err, out)
	}
	if got := strings.ReplaceAll(string(out), "\r\n", "\n"); got != want {
		t.Errorf("Python's board of Go's file:\n%s\nGo's:\n%s", got,
			want)
	}
}

// python is a Python 3.11 or newer that really runs (not Windows'
// store stub), or "".
func python(t *testing.T) string {
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(),
			10*time.Second)
		out, err := exec.CommandContext(ctx, path, "-c",
			"import sys; print(sys.version_info >= (3, "+
				"11))").Output()
		cancel()
		if err == nil && strings.TrimSpace(string(out)) == "True" {
			return path
		}
	}
	return ""
}

// TestMinter mints and revokes through a child lvp mint-key against
// the fake proxy.
func TestMinter(t *testing.T) {
	p := newPractice(t)
	root := proxyFolder(t, p.lite.URL, practiceRules(t))
	minterChecks(t, p, root, lvpArgv(t))
}

// TestMinterScript does the same through mint-key.sh (bash, curl and
// jq), as office.py did.
func TestMinterScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mint-key.sh is for Linux and macOS")
	}
	for _, tool := range []string{"bash", "curl", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}
	p := newPractice(t)
	root := proxyFolder(t, p.lite.URL, practiceRules(t))
	script, _ := filepath.Abs(filepath.Join("..", "..", "mint-key.sh"))
	t.Setenv("SEATS_ENV", filepath.Join(root, "seats.env"))
	minterChecks(t, p, root, []string{"bash", script})
}

func minterChecks(t *testing.T, p *practice, root string, argv []string) {
	g, _, _ := testGame(t, filepath.Join(root, "game"), practiceRules(t))
	g.root, g.mintArgv, g.ctx = root, argv, context.Background()
	if why, err := g.mint("game-ann"); why != "" || err != nil {
		t.Fatalf("mint: %q %v", why, err)
	}
	keys := p.lite.Keys()
	if len(keys) != 1 || keys[0].Alias != "game-ann" ||
		*keys[0].MaxBudget != 0.015 || keys[0].BudgetDuration != "2s" {
		t.Fatalf("keys: %+v", keys)
	}
	path := filepath.Join(root, "game", "keys", "game-ann.key")
	b, _ := os.ReadFile(path)
	if string(b) != keys[0].Key+"\n" {
		t.Errorf("key file %q", b)
	}
	if ok, why, _ := fsx.IsPrivate(path); !ok {
		t.Errorf("key file: %s", why)
	}
	why, err := g.mint("game-ann")
	if err != nil || !strings.Contains(why, "already exists") {
		t.Errorf("mint twice: %q %v", why, err)
	}
	for _, want := range []string{"revoked", "no key"} {
		if got, err := g.revoke("game-ann"); got != want || err != nil {
			t.Errorf("revoke: %q %v, want %q", got, err, want)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("key file still there")
		}
	}
	p.lite.Down(true)
	got, err := g.revoke("game-ann")
	p.lite.Down(false)
	if err != nil || got == "revoked" || got == "no key" || got == "" {
		t.Errorf("revoke with the proxy down: %q %v", got, err)
	}
	g.mintArgv = []string{filepath.Join(root, "no-such-program")}
	if _, err := g.mint("game-x"); err == nil ||
		!strings.HasPrefix(err.Error(), "cannot run mint-key: ") {
		t.Errorf("missing program: %v", err)
	}
}
