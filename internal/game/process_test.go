package game

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
)

func rulesFolder(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "game"), 0o755)
	b, err := os.ReadFile(filepath.Join("..", "..", "game", "rules.toml"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "game", "rules.toml"), b, 0o644)
	return root
}

func TestFlags(t *testing.T) {
	root := rulesFolder(t)
	o := Options{Dir: root, Prog: "lvp", MintArgv: []string{"none"}}
	top := "usage: lvp game [-h] [--rules FILE] {setup,run,board,reset} " +
		"...\n"
	missing := filepath.Join(root, "nope.toml")
	cases := []struct {
		argv           []string
		code           int
		stdout, stderr string
	}{
		{nil, 2, "", top + "lvp game: error: the following arguments " +
			"are required: cmd\n"},
		{[]string{"--rules", "x"}, 2, "", top + "lvp game: error: " +
			"the " +
			"following arguments are required: cmd\n"},
		{[]string{"x"}, 2, "", top + "lvp game: error: argument cmd: " +
			"invalid choice: 'x' (choose from 'setup', 'run', " +
			"'board', " +
			"'reset')\n"},
		{[]string{"--help"}, 0, "*Office game on top of the proxy", ""},
		{[]string{"-h", "run"}, 0, "*{setup,run,board,reset}", ""},
		{[]string{"run", "--help"}, 0, "*-d N, --days N", ""},
		{[]string{"run", "-d", "0"}, 2, "", "usage: lvp game run " +
			"[-h] " +
			"[--rules FILE] [-d N] [-q]\nlvp game run: error: " +
			"argument " +
			"-d/--days: '0': use 1 or more\n"},
		{[]string{"run", "-d"}, 2, "", "usage: lvp game run [-h] " +
			"[--rules FILE] [-d N] [-q]\nlvp game run: error: " +
			"argument " +
			"-d/--days: expected one argument\n"},
		{[]string{"board", "--day=x"}, 2, "", "usage: lvp game board " +
			"[-h] [--rules FILE] [-d N] [-s NAME]\nlvp game " +
			"board: " +
			"error: argument -d/--day: 'x': use 1 or more\n"},
		{[]string{"reset", "extra"}, 2, "", top + "lvp game: error: " +
			"unrecognized arguments: extra\n"},
		{[]string{"setup", "--po"}, 2, "", "usage: lvp game setup " +
			"[-h] " +
			"[--rules FILE]\nlvp game setup: error: unrecognized " +
			"arguments: --po\n"},
		{[]string{"--rules", missing, "board"}, 1, "", "cannot read " +
			missing + ": [Errno 2] No such file or directory: '" +
			missing + "'\n"},
		{[]string{"board", "--rules", missing}, 1, "", "cannot read " +
			missing + ": [Errno 2] No such file or directory: '" +
			missing + "'\n"},
		{[]string{"board"}, 1, "", "no office yet: run `lvp game " +
			"setup`\n"},
	}
	for _, c := range cases {
		out, errs, code := runGame(t, o, c.argv...)
		okOut := out == c.stdout
		if strings.HasPrefix(c.stdout, "*") {
			okOut = strings.Contains(out, c.stdout[1:])
		}
		if code != c.code || !okOut || errs != c.stderr {
			t.Errorf("%q: %d\n%q\n%q\nwant %d\n%q\n%q", c.argv,
				code, out,
				errs, c.code, c.stdout, c.stderr)
		}
	}
	// A rules file with an unknown key: a warning, then the command.
	b, _ := os.ReadFile(filepath.Join(root, "game", "rules.toml"))
	odd := filepath.Join(root, "odd.toml")
	os.WriteFile(odd, append([]byte("colour = 1\n"), b...), 0o644)
	_, errs, code := runGame(t, o, "board", "--rules", odd)
	if code != 1 || errs != "odd.toml line 1: unknown key colour "+
		"(ignored)\nno office yet: run `lvp game setup`\n" {
		t.Errorf("unknown key: %d %q", code, errs)
	}
}

func TestNoProxyFolder(t *testing.T) {
	_, errs, code := runGame(t, Options{Prog: "lvp"}, "board")
	if code != 1 || !strings.HasPrefix(errs, "cannot find compose.yaml") {
		t.Errorf("%d %q", code, errs)
	}
}

func TestProxyURL(t *testing.T) {
	dir := t.TempDir()
	g := &game{root: dir, getenv: func(string) string { return "" }}
	cases := []struct{ env, file, want string }{
		{"", "", "http://localhost:4000"},
		{"http://a:1/", "PROXY_URL=http://b:2", "http://a:1"},
		{"", "PROXY_URL=http://b:2//\n", "http://b:2"},
		{"", "X=1\r\nPROXY_URL= http://c:3 \r\nPROXY_URL=http://d=4\n",
			"http://d=4"},
		{"", "export PROXY_URL=http://e:5\n#PROXY_URL=x\n",
			"http://localhost:4000"},
		{"", "PROXY_URL=\n", "http://localhost:4000"},
	}
	for _, c := range cases {
		path := filepath.Join(dir, "seats.env")
		os.Remove(path)
		if c.file != "" {
			os.WriteFile(path, []byte(c.file), 0o600)
		}
		env := c.env
		g.getenv = func(string) string { return env }
		if got := g.proxyURL(); got != c.want {
			t.Errorf("%q %q: %q, want %q", c.env, c.file, got,
				c.want)
		}
	}
}

// stopAt is a run of `lvp game run` that presses Ctrl+C at one
// stage: cancel is called there.
func stopRun(t *testing.T, stage string) (string, string, int,
	*gamestate.DB) {
	p := newPractice(t)
	root := proxyFolder(t, p.lite.URL, practiceRules(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []any
	// The join's time never runs out here: the bots end by the
	// deadline, or by Ctrl+C.
	gate := &gateClock{recClock{now: 1700000041.0, events: &events},
		make(chan struct{})}
	clk := &gate.recClock
	o := Options{Dir: root, MintArgv: lvpArgv(t), Prog: "lvp",
		Clock: gate, Getenv: func(string) string { return "" }}
	if _, errs, code := runGame(t, o, "setup"); code != 0 {
		t.Fatal(errs)
	}
	o.PG = func(c context.Context, dir, sql string) ([]map[string]any,
		error) {
		switch {
		case strings.Contains(sql, "LiteLLM_VerificationToken"):
			if stage == "wait" {
				cancel()
			}
			var rows []map[string]any
			for _, s := range []string{"fern", "bea", "spike"} {
				rows = append(rows,
					map[string]any{"seat": "game-" + s,
						"spend":    int64(0),
						"reset_at": nextReset(clk)})
			}
			return rows, nil
		case strings.Contains(sql, "count(*) AS n"):
			if stage == "logs" {
				cancel()
			}
			return []map[string]any{{"n": int64(0)}}, nil
		}
		return nil, nil
	}
	o.Chat = func(c context.Context, url, key string, body []byte) (int,
		string) {
		if stage == "bots" {
			cancel()
			<-c.Done()
			return 0, "URLError"
		}
		return 200, ""
	}
	var out, errb strings.Builder
	code := Run(ctx, []string{"run"}, args.Std{Out: &out, Err: &errb}, o)
	db, err := gamestate.Open(filepath.Join(root, "game", "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return out.String(), errb.String(), code, db
}

func TestStoppedRun(t *testing.T) {
	for _, stage := range []string{"wait", "bots", "logs"} {
		t.Run(stage, func(t *testing.T) {
			out, errs, code, db := stopRun(t, stage)
			if code != 1 || errs != "\nstopped; this day is not "+
				"scored. "+
				"`run` plays it again.\n" {
				t.Errorf("%d %q\n%s", code, errs, out)
			}
			rows, _ := db.Q("SELECT v FROM meta WHERE k = 'day'")
			if rows[0].Str("v") != "0" {
				t.Errorf("day scored: %s", rows[0].Str("v"))
			}
		})
	}
}

func TestStoppedMint(t *testing.T) {
	exe, _ := os.Executable()
	for _, c := range []struct{ cmd, want string }{
		{"setup", "\nstopped. Clean up with `lvp game reset`.\n"},
		{"reset", "\nstopped. Run `lvp game reset` again.\n"},
	} {
		root := rulesFolder(t)
		t.Setenv("GAMETEST_AS", "mint")
		t.Setenv("GOLDEN_MINT_LOG", filepath.Join(t.TempDir(), "log"))
		t.Setenv("GOLDEN_MINT", `{"*": {"out": "sk-x\n", "sleep": 30}}`)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(300 * time.Millisecond)
			cancel()
		}()
		var out, errb strings.Builder
		start := time.Now()
		code := Run(ctx, []string{c.cmd}, args.Std{Out: &out,
			Err: &errb},
			Options{Dir: root, Prog: "lvp",
				MintArgv: []string{exe}})
		if code != 1 || errb.String() != c.want {
			t.Errorf("%s: %d %q", c.cmd, code, errb.String())
		}
		if time.Since(start) > 20*time.Second {
			t.Errorf("%s: the child did not stop", c.cmd)
		}
		cancel()
	}
}

// gateClock is a recClock whose After waits for the test.
type gateClock struct {
	recClock
	gate chan struct{}
}

func (c *gateClock) After(float64) <-chan struct{} { return c.gate }

// TestAbandonedBots: bots still working when the join gives up are
// stopped and write nothing more (ledger O-4); the day is scored.
func TestAbandonedBots(t *testing.T) {
	dir := t.TempDir()
	g, out, _ := testGame(t, dir, practiceRules(t))
	o := newOffice(t, g, "abc123")
	os.MkdirAll(g.keys, 0o700)
	for _, s := range []string{"fern", "bea", "spike"} {
		os.WriteFile(filepath.Join(g.keys, "game-"+s+".key"),
			[]byte("sk-x\n"), 0o600)
	}
	var events []any
	clk := &gateClock{recClock{now: 1700000041.0, events: &events},
		make(chan struct{})}
	g.clk = clk
	stuck := make(chan struct{}, 3)
	g.chat = func(c context.Context, url, key string, body []byte) (int,
		string) {
		stuck <- struct{}{}
		<-c.Done()
		return 0, "TimeoutError"
	}
	g.pgFn = func(c context.Context, dir, sql string) ([]map[string]any,
		error) {
		if strings.Contains(sql, "LiteLLM_VerificationToken") {
			var rows []map[string]any
			for _, s := range []string{"fern", "bea", "spike"} {
				rows = append(rows,
					map[string]any{"seat": "game-" + s,
						"spend": int64(0),
						"reset_at": nextReset(
							&clk.recClock)})
			}
			return rows, nil
		}
		if strings.Contains(sql, "count(*) AS n") {
			return []map[string]any{{"n": int64(0)}}, nil
		}
		return nil, nil
	}
	go func() {
		defer close(clk.gate) // the join's time is up
		for i := 0; i < 3; i++ {
			select {
			case <-stuck:
			case <-time.After(30 * time.Second):
				t.Error("a bot never called the proxy")
				return
			}
		}
	}()
	if _, err := o.playDay(1, "http://x"); err != nil {
		t.Fatal(err)
	}
	rows, _ := o.q("SELECT * FROM daylog")
	if len(rows) != 0 {
		t.Errorf("abandoned bots wrote %d daylog rows", len(rows))
	}
	rows, _ = o.q("SELECT state, count(*) FROM quests GROUP BY 1 " +
		"ORDER BY 1")
	if jsonText(dumpRows(rows)) != `[["expired",27],["unfinished",3]]` {
		t.Errorf("quests: %s", jsonText(dumpRows(rows)))
	}
	if !strings.Contains(out.String(), "30 quests posted") {
		t.Errorf("out: %s", out)
	}
}

func dumpRows(rows []gamestate.Row) [][]any {
	var out [][]any
	for _, r := range rows {
		var row []any
		for i := range r.Columns() {
			row = append(row, r.At(i))
		}
		out = append(out, row)
	}
	return out
}

func TestDieMessages(t *testing.T) {
	var de *dieError
	if err := die("x"); !errors.As(err, &de) || err.Error() != "x" {
		t.Error(err)
	}
}

// nextReset is when a 2-second budget next refills, as LiteLLM lines
// it up with the clock.
func nextReset(c *recClock) float64 {
	return math.Floor(c.Wall()/2)*2 + 2
}
