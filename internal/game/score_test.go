package game

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
)

type table = [][][2]string

type boardRun struct {
	Fn, Seat       string
	Day            *int
	Individuals    *bool
	Stdout, Stderr string
	Code           int
}

type scoreGolden struct {
	Python string
	Weeks  []struct {
		Name, Rules string
		Days        []scoreDayCase
		DB          map[string]table
		Runs        []boardRun
	}
	Reference []struct {
		File string
		Runs []boardRun
	}
	Helpers struct {
		Money    [][2]any
		Seconds  [][3]any
		Clock    [][2]string
		RankCorr [][3]any `json:"rank_corr"`
		Positive [][3]any
	}
	Commands []commandCase
}

type commandCase = struct {
	Name, Cmd, Before string
	Args              map[string]any
	Script            map[string]any
	Stdout, Stderr    string
	Code              int
	MintCalls         [][]string `json:"mint_calls"`
	Keys, Files       []string
	DB                map[string]table
}

var tables = []string{"meta", "guilds", "players", "quests",
	"completions", "days", "daylog", "usage", "scores"}

func loadScore(t *testing.T) scoreGolden {
	var g scoreGolden
	readGolden(t, "office_score.json", &g)
	if len(g.Weeks) == 0 || len(g.Commands) == 0 {
		t.Fatal("golden is empty")
	}
	return g
}

// restore fills a new office.db with the dumped rows.
func restore(t *testing.T, g *game, id string,
	dumpRows map[string]table) *office {
	t.Helper()
	os.Remove(g.db)
	db, err := gamestate.Create(g.db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, name := range tables {
		for _, row := range dumpRows[name] {
			marks := strings.TrimSuffix(strings.Repeat("?,", len(
				row)),
				",")
			vals := make([]any, len(row))
			for i, p := range row {
				vals[i] = untyped(p)
			}
			if _, err := db.Q("INSERT INTO "+name+" VALUES "+
				"("+marks+")",
				vals...); err != nil {
				t.Fatal(name, err)
			}
		}
	}
	o := &office{g: g, db: db, rules: g.rules}
	o.id, _ = o.meta("office")
	return o
}

type scoreDayCase = struct {
	Day     int
	Before  map[string]table
	Answers []string
	Now     string
	Events  []any
	After   map[string]table
}

func TestScoreDayGolden(t *testing.T) {
	gold := loadScore(t)
	for _, w := range gold.Weeks {
		for _, d := range w.Days {
			name := fmt.Sprintf("%s/day%d", w.Name, d.Day)
			t.Run(name, func(t *testing.T) {
				scoreDayCheck(t, w.Rules, d)
			})
		}
	}
}

// scoreDayCheck runs score_day on the day's office.db as it was
// before, with the golden's answers from the proxy, and wants the
// same questions, sleeps and rows.
func scoreDayCheck(t *testing.T, rulesText string, d scoreDayCase) {
	g, _, _ := testGame(t, t.TempDir(), rulesText)
	o := restore(t, g, "", d.Before)
	var events []any
	g.clk = &recClock{now: toF(jsonNumber(d.Now)), events: &events}
	left := append([]string{}, d.Answers...)
	g.pgFn = func(ctx context.Context, dir,
		sql string) ([]map[string]any, error) {
		events = append(events, []any{"pg", sql})
		if strings.Contains(sql, "GROUP BY") {
			return rowsOf(t, left[len(left)-1]), nil
		}
		a := left[0]
		if len(left) > 2 {
			left = left[1:]
		}
		return rowsOf(t, a), nil
	}
	if err := o.scoreDay(d.Day); err != nil {
		t.Fatal(err)
	}
	compareEvents(t, events, d.Events)
	for name, want := range d.After {
		got := jsonText(dump(t, o.db, name))
		if got != jsonText(want) {
			t.Errorf("%s:\n got %s\nwant %s", name, cut(got),
				cut(jsonText(want)))
		}
	}
}

// boardOutput runs one board command on o, as the golden did.
func boardOutput(t *testing.T, o *office, run boardRun) (string,
	string, int) {
	var out, errb bytes.Buffer
	o.g.out, o.g.err = &out, &errb
	day := 0
	if run.Day != nil {
		day = *run.Day
	}
	var err error
	switch run.Fn {
	case "board":
		err = o.board(day)
	case "seat":
		err = o.seatBoard(run.Seat, day)
	case "summary":
		err = o.daySummary(day)
	}
	code := 0
	var de *dieError
	switch {
	case errors.As(err, &de):
		errb.WriteString(de.msg + "\n")
		code = 1
	case err != nil:
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

func checkRuns(t *testing.T, o *office, quiet *office, runs []boardRun) {
	t.Helper()
	for i, run := range runs {
		on := o
		if run.Individuals != nil && !*run.Individuals {
			on = quiet
		}
		out, errs, code := boardOutput(t, on, run)
		label := fmt.Sprintf("run %d (%s %s %v)", i, run.Fn, run.Seat,
			run.Day)
		if out != rename(run.Stdout) {
			t.Errorf("%s stdout:\n%s\nwant:\n%s", label, out,
				rename(run.Stdout))
		}
		if errs != rename(run.Stderr) || code != run.Code {
			t.Errorf("%s stderr %q (%d), want %q (%d)", label, errs,
				code, rename(run.Stderr), run.Code)
		}
	}
}

func TestBoardsGolden(t *testing.T) {
	gold := loadScore(t)
	for _, w := range gold.Weeks {
		t.Run(w.Name, func(t *testing.T) {
			dir := t.TempDir()
			g, _, _ := testGame(t, dir, w.Rules)
			o := restore(t, g, "", w.DB)
			quietText := strings.Replace(w.Rules, "individuals = "+
				"true",
				"individuals = false", 1)
			qg, _, _ := testGame(t, t.TempDir(), quietText)
			quiet := &office{g: qg, db: o.db, rules: qg.rules,
				id: o.id}
			checkRuns(t, o, quiet, w.Runs)
		})
	}
}

func TestReferenceBoards(t *testing.T) {
	gold := loadScore(t)
	rulesText, err := os.ReadFile(filepath.Join("..", "..", "game",
		"rules.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range gold.Reference {
		t.Run(ref.File, func(t *testing.T) {
			dir := t.TempDir()
			g, _, _ := testGame(t, dir, string(rulesText))
			b, err := os.ReadFile(filepath.Join("..", "..", "tests",
				"reference", "game", ref.File))
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(g.db, b, 0o644)
			g.err = io.Discard
			o, err := g.openOffice()
			if err != nil {
				t.Fatal(err)
			}
			defer o.close()
			checkRuns(t, o, o, ref.Runs)
		})
	}
}

func TestHelpersGolden(t *testing.T) {
	h := loadScore(t).Helpers
	for _, c := range h.Money {
		v := toF(jsonNumber(fmt.Sprint(c[0])))
		if got := money(v); got != c[1] {
			t.Errorf("money(%v) = %q, want %q", c[0], got, c[1])
		}
	}
	for _, c := range h.Seconds {
		n, err := seconds(c[0].(string))
		msg := ""
		if err != nil {
			msg, n = err.Error()+"\n", 0
		}
		if float64(n) != c[1].(float64) || msg != c[2] {
			t.Errorf("seconds(%q) = %d %q, want %v %q", c[0], n,
				msg,
				c[1], c[2])
		}
	}
	for _, c := range h.Clock {
		if got := clockText(toF(jsonNumber(c[0]))); got != c[1] {
			t.Errorf("clock(%s) = %s, want %s", c[0], got, c[1])
		}
	}
	for _, c := range h.RankCorr {
		a, b := strs(c[0]), strs(c[1])
		if got := pyReprF(rankCorr(a, b)); got != c[2] {
			t.Errorf("rank_corr(%v, %v) = %s, want %s", a, b, got,
				c[2])
		}
	}
	for _, c := range h.Positive {
		n, err := positive(c[0].(string))
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		want := c[1].(float64)
		if want > 1e18 {
			want = float64(int(^uint(
				0) >> 1)) // held at the largest
		}
		if float64(n) != want || msg != c[2] {
			t.Errorf("positive(%q) = %d %q, want %v %q", c[0], n,
				msg,
				c[1], c[2])
		}
	}
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestCommandsGolden(t *testing.T) {
	gold := loadScore(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rulesText, err := os.ReadFile(filepath.Join("..", "..", "game",
		"rules.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range gold.Commands {
		t.Run(c.Name, func(t *testing.T) {
			commandCheck(t, c, exe, rulesText)
		})
	}
}

// commandCheck runs one command in a new proxy folder with the
// golden's fake mint-key, and wants Python's words, calls and files.
func commandCheck(t *testing.T, c commandCase, exe string,
	rulesText []byte) {
	root := t.TempDir()
	game := filepath.Join(root, "game")
	os.MkdirAll(game, 0o755)
	os.WriteFile(filepath.Join(game, "rules.toml"), rulesText,
		0o644)
	log := filepath.Join(t.TempDir(), "mint.log")
	t.Setenv("GAMETEST_AS", "mint")
	t.Setenv("GOLDEN_MINT_LOG", log)
	var events []any
	o := Options{Clock: &recClock{now: 1700000000.75,
		events: &events}, Dir: root, MintArgv: []string{exe},
		TokenHex: func(n int) string {
			return strings.Repeat("ab", n)
		}, Prog: "lvp", Getenv: func(string) string { return "" }}
	std := args.Std{Out: io.Discard, Err: io.Discard}
	if c.Before != "" {
		t.Setenv("GOLDEN_MINT", `{"*": {"out": "sk-k\n"}}`)
		if Run(context.Background(), []string{"setup"}, std,
			o) != 0 {
			t.Fatal("setup before failed")
		}
	}
	if c.Before == "week-over" {
		db, err := gamestate.Open(filepath.Join(game,
			"office.db"))
		if err != nil {
			t.Fatal(err)
		}
		db.Q("UPDATE meta SET v = '5' WHERE k = 'day'")
		db.Close()
	}
	os.Remove(log)
	t.Setenv("GOLDEN_MINT", jsonText(c.Script))
	var out, errb bytes.Buffer
	code := Run(context.Background(), commandArgv(c.Cmd, c.Args),
		args.Std{Out: &out, Err: &errb}, o)
	if out.String() != rename(c.Stdout) {
		t.Errorf("stdout:\n%s\nwant:\n%s", out.String(),
			rename(c.Stdout))
	}
	if errb.String() != rename(c.Stderr) || code != c.Code {
		t.Errorf("stderr %q (%d), want %q (%d)", errb.String(),
			code, rename(c.Stderr), c.Code)
	}
	calls := [][]string{}
	if b, err := os.ReadFile(log); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(
			string(b)), "\n") {
			var e struct{ Argv []string }
			if err := jsonUnmarshal(line, &e); err != nil {
				t.Fatal(err)
			}
			calls = append(calls, e.Argv)
		}
	}
	if c.MintCalls == nil {
		c.MintCalls = [][]string{}
	}
	if jsonText(calls) != jsonText(c.MintCalls) {
		t.Errorf("mint calls:\n got %s\nwant %s",
			jsonText(calls), jsonText(c.MintCalls))
	}
	if got := listDir(filepath.Join(game, "keys")); jsonText(
		got) != jsonText(c.Keys) {
		t.Errorf("keys %v, want %v", got, c.Keys)
	}
	if got := listDir(game); jsonText(got) != jsonText(c.Files) {
		t.Errorf("files %v, want %v", got, c.Files)
	}
	if c.DB != nil {
		db, err := gamestate.Open(filepath.Join(game,
			"office.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for name, want := range c.DB {
			if got := dump(t, db, name); jsonText(got) !=
				jsonText(want) {
				t.Errorf("%s:\n got %s\nwant %s", name,
					jsonText(got), jsonText(want))
			}
		}
	}
}

// commandArgv is the golden's argparse.Namespace as lvp game's
// arguments.
func commandArgv(cmd string, a map[string]any) []string {
	argv := []string{cmd}
	if d, ok := a["days"].(float64); ok {
		argv = append(argv, "-d", strconv.Itoa(int(d)))
	}
	if q, ok := a["quiet"].(bool); ok && q {
		argv = append(argv, "-q")
	}
	if d, ok := a["day"].(float64); ok {
		argv = append(argv, "--day", strconv.Itoa(int(d)))
	}
	if s, ok := a["seat"].(string); ok {
		argv = append(argv, "--seat="+s)
	}
	return argv
}

func listDir(dir string) []string {
	ents, _ := os.ReadDir(dir)
	out := []string{}
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}
