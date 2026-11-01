package game

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type playGolden struct {
	Python string
	Quests []struct {
		Name, Rules string
		Day, N      int
		Rows        [][][2]string
	}
	Bots  []botCase
	Waits []waitCase
}

type botCase = struct {
	Name, Rules, Seat, Tag, Start, End, Script string
	Day                                        int
	Left                                       float64
	Events                                     []any
	Quests, Completions, Daylog                [][][2]string
}

type waitCase = struct {
	Name, Rules, Now, Stdout, Stderr string
	Answers                          []string
	Revokes                          []string
	Mints                            []*string
	Events                           []any
	Result                           []string
}

func loadPlay(t *testing.T) playGolden {
	var g playGolden
	readGolden(t, "office_play.json", &g)
	if len(g.Quests) == 0 || len(g.Bots) == 0 || len(g.Waits) == 0 {
		t.Fatal("golden is empty")
	}
	return g
}

func TestPostQuestsGolden(t *testing.T) {
	gold := loadPlay(t)
	var o *office
	last := ""
	for _, c := range gold.Quests {
		if c.Name != last {
			g, _, _ := testGame(t, t.TempDir(), c.Rules)
			o = newOffice(t, g, "abc123")
			last = c.Name
		}
		n, err := o.postQuests(c.Day)
		if err != nil {
			t.Fatal(err)
		}
		if n != c.N {
			t.Errorf("%s day %d: %d quests, want %d", c.Name, c.Day,
				n,
				c.N)
		}
		var rows [][][2]string
		for _, r := range dump(t, o.db, "quests") {
			if r[1] == typed(int64(c.Day)) {
				rows = append(rows, r)
			}
		}
		if jsonText(rows) != jsonText(c.Rows) {
			t.Errorf("%s day %d:\n got %s\nwant %s", c.Name, c.Day,
				jsonText(rows), jsonText(c.Rows))
		}
	}
}

func TestBotGolden(t *testing.T) {
	gold := loadPlay(t)
	for _, c := range gold.Bots {
		t.Run(c.Name, func(t *testing.T) { botCheck(t, c) })
	}
}

// botCheck runs one bot through a day with the golden's proxy and
// clock, and wants the same calls, sleeps and rows.
func botCheck(t *testing.T, c botCase) {
	dir := t.TempDir()
	g, _, _ := testGame(t, dir, c.Rules)
	o := newOffice(t, g, "abc123")
	os.MkdirAll(g.keys, 0o700)
	name := c.Seat[len(g.rules.Office.SeatPrefix):]
	os.WriteFile(filepath.Join(g.keys, c.Seat+".key"),
		[]byte("sk-seat-"+name+"\n"), 0o600)
	if _, err := o.postQuests(c.Day); err != nil {
		t.Fatal(err)
	}
	var events []any
	start := toF(jsonNumber(c.Start))
	clk := &recClock{now: start, events: &events}
	g.clk = clk
	calls := 0
	script := scripts[c.Script]
	g.chat = func(ctx context.Context, url, key string,
		body []byte) (int, string) {
		status, kind := script(calls)
		calls++
		text := string(body)
		if calls > 4 { // the golden hashes the rest
			h := sha256.Sum256(body)
			text = "sha256:" + hex.EncodeToString(h[:])
		}
		events = append(events, []any{"chat", url, key, text,
			status, kind})
		return status, kind
	}
	players, err := o.players()
	if err != nil {
		t.Fatal(err)
	}
	var b *bot
	for _, p := range players {
		if p.Str("seat") == c.Seat {
			b, err = newBot(context.Background(), o, p, c.Day,
				c.Tag, start+c.Left, "http://proxy.test:4000")
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	b.run()
	compareEvents(t, events, c.Events)
	if got := pyReprF(clk.now); got != c.End {
		t.Errorf("clock ends at %s, want %s", got, c.End)
	}
	var qs [][][2]string
	for _, r := range dump(t, o.db, "quests") {
		if r[1] == typed(int64(c.Day)) {
			qs = append(qs, r)
		}
	}
	for name, pair := range map[string][2][][][2]string{
		"quests":      {qs, c.Quests},
		"completions": {dump(t, o.db, "completions"), c.Completions},
		"daylog":      {dump(t, o.db, "daylog"), c.Daylog},
	} {
		if jsonText(pair[0]) != jsonText(pair[1]) {
			t.Errorf("%s:\n got %s\nwant %s", name,
				jsonText(pair[0]), jsonText(pair[1]))
		}
	}
}

// scripts are the golden's SCRIPTS: the proxy's answer to call i.
var scripts = map[string]func(i int) (int, string){
	"ok": func(int) (int, string) { return 200, "" },
	"budget-at-9": func(i int) (int, string) {
		if i < 9 {
			return 200, ""
		}
		return 422, "budget_exceeded"
	},
	"budget-400": func(i int) (int, string) {
		if i < 5 {
			return 200, ""
		}
		return 400, "budget_exceeded"
	},
	"flaky": func(i int) (int, string) {
		if i%4 == 1 {
			return 500, "internal_server_error"
		}
		return 200, ""
	},
	"down": func(int) (int, string) { return 0, "URLError" },
	"rate": func(i int) (int, string) {
		if i < 3 {
			return 200, ""
		}
		return 429, "rate_limit_error"
	},
	"late-503": func(i int) (int, string) {
		if i < 20 {
			return 200, ""
		}
		return 503, ""
	},
}

func compareEvents(t *testing.T, got, want []any) {
	t.Helper()
	for i := 0; i < len(got) || i < len(want); i++ {
		var g, w string
		if i < len(got) {
			g = jsonText(got[i])
		}
		if i < len(want) {
			w = jsonText(want[i])
		}
		if g != w {
			g, w = cut(g), cut(w)
			t.Fatalf("event %d of %d (want %d):\n got %s\nwant %s",
				i,
				len(got), len(want), g, w)
		}
	}
}

func TestWaitForDayGolden(t *testing.T) {
	gold := loadPlay(t)
	for _, c := range gold.Waits {
		t.Run(c.Name, func(t *testing.T) { waitCheck(t, c) })
	}
}

// waitCheck runs wait_for_day on the golden's proxy states, and wants
// the same questions, sleeps, revokes, mints, words and answer.
func waitCheck(t *testing.T, c waitCase) {
	g, out, errb := testGame(t, t.TempDir(), c.Rules)
	o := newOffice(t, g, "abc123")
	var events []any
	g.clk = &recClock{now: toF(jsonNumber(c.Now)),
		events: &events}
	left := append([]string{}, c.Answers...)
	g.pgFn = func(ctx context.Context, dir,
		sql string) ([]map[string]any, error) {
		a := left[0]
		if len(left) > 1 {
			left = left[1:]
		}
		events = append(events, []any{"pg", sql})
		return rowsOf(t, a), nil
	}
	rv, mn := append([]string{}, c.Revokes...), c.Mints
	g.revokeFn = func(alias string) (string, error) {
		events = append(events, []any{"revoke", alias})
		r := rv[0]
		rv = rv[1:]
		return r, nil
	}
	g.mintFn = func(alias string) (string, error) {
		events = append(events, []any{"mint", alias})
		m := mn[0]
		mn = mn[1:]
		if m == nil {
			return "", nil
		}
		return *m, nil
	}
	players, err := o.players()
	if err != nil {
		t.Fatal(err)
	}
	started, ends, err := o.waitForDay(players, 2)
	var result []string
	var de *dieError
	switch {
	case errors.As(err, &de):
		errb.WriteString(de.msg + "\n")
	case err != nil:
		t.Fatal(err)
	default:
		result = []string{pyReprF(started), pyReprF(ends)}
	}
	compareEvents(t, events, c.Events)
	if out.String() != c.Stdout {
		t.Errorf("stdout:\n got %q\nwant %q", out, c.Stdout)
	}
	if errb.String() != rename(c.Stderr) {
		t.Errorf("stderr:\n got %q\nwant %q", errb,
			rename(c.Stderr))
	}
	if jsonText(result) != jsonText(c.Result) {
		t.Errorf("result %v, want %v", result, c.Result)
	}
}

func cut(s string) string {
	if len(s) > 600 {
		return s[:600] + "..."
	}
	return s
}
