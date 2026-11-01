package game

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
	"github.com/kjFriday/local-verse-proxy/internal/rules"
)

// readGolden reads testdata/golden/<name> into v.
func readGolden(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

// rename is spec 11.9's table: what Python said, as Go says it, with
// lvp typed as "lvp".
func rename(s string) string {
	s = strings.ReplaceAll(s, "python3 game/office.py", "lvp game")
	s = strings.ReplaceAll(s, "`office.py ", "`lvp game ")
	return strings.ReplaceAll(s, "mint-key.sh", "mint-key")
}

// typed is a value as the goldens write it: its Python type and str.
func typed(v any) [2]string {
	switch x := v.(type) {
	case nil:
		return [2]string{"n", ""}
	case int64:
		return [2]string{"i", str(x)}
	case float64:
		return [2]string{"f", pyfmt.Repr(x)}
	case string:
		return [2]string{"s", x}
	case bool:
		return [2]string{"b", str(x)}
	}
	return [2]string{"?", str(v)}
}

// untyped is the value a typed pair stands for.
func untyped(p [2]string) any {
	switch p[0] {
	case "n":
		return nil
	case "i":
		return toI(json.Number(p[1]))
	case "f":
		return toF(json.Number(p[1]))
	}
	return p[1]
}

// dump is every row of table in rowid order, typed.
func dump(t *testing.T, db *gamestate.DB, table string) [][][2]string {
	t.Helper()
	rows, err := db.Q("SELECT * FROM " + table + " ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	out := [][][2]string{}
	for _, r := range rows {
		var row [][2]string
		for i := range r.Columns() {
			row = append(row, typed(r.At(i)))
		}
		out = append(out, row)
	}
	return out
}

// recClock is a wall clock that only sleeps move; it records them.
type recClock struct {
	mu     sync.Mutex
	now    float64
	events *[]any
}

func (c *recClock) Wall() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *recClock) Mono() float64 { return c.Wall() }

func (c *recClock) Sleep(ctx context.Context, s float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.events = append(*c.events, []any{"sleep", pyfmt.Repr(s)})
	c.now += s
	return nil
}

func (c *recClock) After(float64) <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// testGame is a game in dir with rules text, writing to buffers.
func testGame(t *testing.T, dir, rulesText string) (*game, *bytes.Buffer,
	*bytes.Buffer) {
	t.Helper()
	path := filepath.Join(dir, "rules.toml")
	if err := os.WriteFile(path, []byte(rulesText), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _, err := rules.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	g := &game{ctx: context.Background(), out: &out, err: &errb,
		root: dir, dir: dir, db: filepath.Join(dir, "office.db"),
		keys: filepath.Join(dir, "keys"), rules: r, prog: "lvp",
		getenv: func(string) string { return "" },
		tokenHex: func(n int) string {
			return strings.Repeat("ab", n)
		}}
	g.mintFn, g.revokeFn = g.mint, g.revoke
	return g, &out, &errb
}

// newOffice is the golden's new_office: a fresh office.db with the
// roster as players.
func newOffice(t *testing.T, g *game, id string) *office {
	t.Helper()
	os.Remove(g.db)
	db, err := gamestate.Create(g.db)
	if err != nil {
		t.Fatal(err)
	}
	q := func(sql string, a ...any) {
		if _, err := db.Q(sql, a...); err != nil {
			t.Fatal(err)
		}
	}
	q("INSERT INTO meta VALUES ('office', ?)", id)
	q("INSERT INTO meta VALUES ('day', '0')")
	seen := map[string]bool{}
	var guilds []string
	for _, s := range g.rules.Roster.Seats {
		if !seen[s.Guild] {
			seen[s.Guild] = true
			guilds = append(guilds, s.Guild)
		}
	}
	sort.Strings(guilds)
	for _, gd := range guilds {
		q("INSERT INTO guilds VALUES (?)", gd)
	}
	for _, s := range g.rules.Roster.Seats {
		q("INSERT INTO players VALUES (?,?,?,?)",
			g.rules.Office.SeatPrefix+s.Name, s.Name, s.Persona,
			s.Guild)
	}
	t.Cleanup(func() { db.Close() })
	return &office{g: g, db: db, rules: g.rules, id: id}
}

// rows decodes a JSON answer as pgread.Once gives it.
func rowsOf(t *testing.T, text string) []map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var rows []map[string]any
	if err := dec.Decode(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// jsonText is v as compact JSON, for comparing.
func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func jsonNumber(s string) json.Number { return json.Number(s) }

func pyReprF(f float64) string { return pyfmt.Repr(f) }

func jsonUnmarshal(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}
