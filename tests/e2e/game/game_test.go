//go:build e2e

// Package game is the office game on a real stack (spec 16.5): a
// practice week played by Go alone, cross-play with Python both ways
// on one office.db, the quests both post for one seed, and a
// Go-played day scored again by Python (tests/golden/rescore.py).
//
//	E2E_PROXY_DIR=<proxy folder> go test -tags e2e -timeout 60m \
//	  ./tests/e2e/game
//
// The proxy folder is a copy of the repo with seats.env filled in and
// the stack up in practice mode (game/compose.game.yaml), with
// COMPOSE_PROJECT_NAME and COMPOSE_FILE set as for that stack. The
// test plays with game/e2e-rules.toml there (rules.toml with 1-minute
// days that close 25 s early) and leaves no office or game keys
// behind. It needs python3 3.11 or newer for the Python sides.
package game

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	dir    string // the proxy folder
	lvp    string
	python string
	rules  = filepath.Join("game", "e2e-rules.toml")
)

func setup(t *testing.T) {
	dir = os.Getenv("E2E_PROXY_DIR")
	if dir == "" {
		t.Skip("E2E_PROXY_DIR is not set: no stack to play on")
	}
	if lvp == "" {
		tmp, err := os.MkdirTemp("", "lvp-e2e-game")
		if err != nil {
			t.Fatal(err)
		}
		lvp = filepath.Join(tmp, "lvp")
		cmd := exec.Command("go", "build", "-o", lvp, "./cmd/lvp")
		cmd.Dir = filepath.Join("..", "..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building lvp: %v\n%s", err, out)
		}
	}
	p, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	python = p
	b, err := os.ReadFile(filepath.Join(dir, "game", "rules.toml"))
	if err != nil {
		t.Fatal(err)
	}
	r := regexp.MustCompile(`(?m)^length = "[^"]*"`).ReplaceAll(b,
		[]byte(`length = "1m"`))
	r = regexp.MustCompile(`(?m)^close = "[^"]*"`).ReplaceAll(r,
		[]byte(`close = "25s"`))
	if err := os.WriteFile(filepath.Join(dir, rules), r,
		0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		goGame(t, "reset")
		os.Remove(filepath.Join(dir, rules))
	})
	goGame(t, "reset")
}

func run(t *testing.T, name string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if e, ok := err.(*exec.ExitError); ok {
		code = e.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out.String(), code
}

func goGame(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return run(t, lvp, append([]string{"game", "--rules", rules},
		args...)...)
}

func pyGame(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return run(t, python, append([]string{"-B", "game/office.py",
		"--rules", rules}, args...)...)
}

// res is what one command printed, and its exit code.
type res struct {
	out  string
	code int
}

func r(out string, code int) res { return res{out, code} }

func must(t *testing.T, what string, x res) string {
	t.Helper()
	out, code := x.out, x.code
	t.Logf("%s (exit %d):\n%s", what, code, out)
	if code != 0 {
		t.Fatalf("%s: exit %d", what, code)
	}
	return out
}

// sql asks the office.db with Python's sqlite3 (read only).
func sql(t *testing.T, q string) string {
	t.Helper()
	out, code := run(t, python, "-c", "import sqlite3, sys\n"+
		"c = sqlite3.connect('file:game/office.db?mode=ro',\n"+
		"    uri=True)\n"+
		"for r in c.execute(sys.argv[1]): print(*r, sep='|')", q)
	if code != 0 {
		t.Fatalf("sql %q: %s", q, out)
	}
	return strings.TrimSpace(out)
}

var logsRE = regexp.MustCompile(`spend logs: (\d+) of (\d+) rows`)

// TestGoWeek plays the whole practice week with Go only.
func TestGoWeek(t *testing.T) {
	setup(t)
	must(t, "lvp game setup", r(goGame(t, "setup")))
	out := must(t, "lvp game run --days 5", r(goGame(t, "run", "--days",
		"5", "--quiet")))
	ms := logsRE.FindAllStringSubmatch(out, -1)
	if len(ms) != 5 {
		t.Errorf("%d day summaries, want 5", len(ms))
	}
	for i, m := range ms {
		if m[1] != m[2] {
			t.Errorf("day %d: spend logs %s of %s", i+1, m[1], m[2])
		}
	}
	// Each day started within 5 s of a minute boundary (LiteLLM's
	// refills line up with the clock).
	for _, l := range strings.Split(sql(t, "SELECT day, started FROM "+
		"days ORDER BY day"), "\n") {
		f := strings.Split(l, "|")
		s, _ := strconv.ParseFloat(f[1], 64)
		off := math.Mod(s, 60)
		t.Logf("day %s started %.1f s past the minute", f[0], off)
		if off > 5 && off < 55 {
			t.Errorf("day %s started %.1f s past the minute", f[0],
				off)
		}
	}
	// The spending personas ran out of mana: LiteLLM's 422.
	refused := sql(t, "SELECT count(*) FROM daylog WHERE code = 422 "+
		"AND kind = 'budget_exceeded'")
	t.Logf("422 budget_exceeded refusals in the daylog: %s", refused)
	if refused == "0" {
		t.Errorf("no 422 budget_exceeded refusals")
	}
	over, _ := goGame(t, "run")
	t.Logf("a sixth day:\n%s", over)
	if !strings.Contains(over, "The week is over") {
		t.Errorf("sixth day: %q", over)
	}
	// Python's score_day over the same spend rows gives Go's scores.
	out, code := run(t, python, "-B", filepath.Join("tests", "golden",
		"rescore.py"), "--rules", rules, dir,
		filepath.Join(dir, "game", "office.db"))
	t.Logf("rescore.py:\n%s", out)
	if code != 0 {
		t.Errorf("rescore.py: exit %d", code)
	}
	boards(t)
}

// boards: every board each program prints of the same file is the
// same, byte for byte.
func boards(t *testing.T) {
	t.Helper()
	n := sql(t, "SELECT count(*) FROM days WHERE logged IS NOT NULL")
	days, _ := strconv.Atoi(n)
	names := strings.Split(sql(t, "SELECT name FROM players ORDER "+
		"BY seat"), "\n")
	cmds := [][]string{{"board"}}
	for d := 1; d <= days; d++ {
		cmds = append(cmds, []string{"board", "--day", fmt.Sprint(d)})
	}
	for _, name := range names {
		cmds = append(cmds, []string{"board", "--seat", name})
	}
	same := 0
	for _, c := range cmds {
		a, ca := goGame(t, c...)
		b, cb := pyGame(t, c...)
		if a != b || ca != cb {
			t.Errorf("%v: Go and Python differ:\n--- Go (%d)\n%s\n"+
				"--- Python (%d)\n%s", c, ca, a, cb, b)
			continue
		}
		same++
	}
	t.Logf("%d of %d board commands byte-identical", same, len(cmds))
}

// TestCrossPlay: Python sets up, Go plays two days; then Go sets up
// and Python plays. Each time both boards are the same bytes.
func TestCrossPlay(t *testing.T) {
	setup(t)
	must(t, "python setup", r(pyGame(t, "setup")))
	must(t, "lvp game run --days 2", r(goGame(t, "run", "--days", "2",
		"--quiet")))
	boards(t)
	must(t, "python reset", r(pyGame(t, "reset")))
	must(t, "lvp game setup", r(goGame(t, "setup")))
	must(t, "python run --days 2", r(pyGame(t, "run", "--days", "2",
		"--quiet")))
	boards(t)
}

// TestQuestsEqual: for one seed, both post the same quests on day 1.
func TestQuestsEqual(t *testing.T) {
	setup(t)
	q := "SELECT id, day, size, value, calls, par_mana FROM quests " +
		"ORDER BY id"
	must(t, "lvp game setup", r(goGame(t, "setup")))
	// Setup posts nothing; the first day posts. Both are stopped
	// after the posting by a day that has already ended for them:
	// the quests come from the seed alone, so day 1 is played once
	// by each and its quests compared.
	must(t, "lvp game run", r(goGame(t, "run", "--quiet")))
	a := sql(t, q+" LIMIT 1000")
	must(t, "lvp game reset", r(goGame(t, "reset")))
	must(t, "python setup", r(pyGame(t, "setup")))
	must(t, "python run", r(pyGame(t, "run", "--quiet")))
	b := sql(t, q+" LIMIT 1000")
	if a != b {
		t.Errorf("quests differ:\n--- Go\n%s\n--- Python\n%s", a, b)
	}
	t.Logf("%d quests, equal: %v", strings.Count(a, "\n")+1, a == b)
}
