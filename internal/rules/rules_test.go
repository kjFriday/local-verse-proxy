package rules

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// golden is testdata/golden/rules.json, from tests/golden/rules.py.
type golden struct {
	Python []string             `json:"python"`
	Load   [][2]json.RawMessage `json:"load"`
	Seats  [][2]json.RawMessage `json:"seat_names"`
}

type result struct {
	Die   *string           `json:"die"`
	Crash *string           `json:"crash"`
	Rules map[string]any    `json:"rules"`
	Str   map[string]string `json:"str"`
}

// goMessages is testdata/go_messages.json: Go's words where they
// differ from Python's on purpose, the unknown-key warnings, the
// files Go reads that Python refused, and SeatNames' answers where
// they differ. A message may be split into a list of parts, joined
// with nothing, to keep the file's lines short.
type goMessages struct {
	Words    map[string]parts     `json:"words"`
	Warnings map[string][]string  `json:"warnings"`
	Loads    map[string]string    `json:"loads"`
	Seats    map[string]seatNames `json:"seat_names"`
}

// parts is a text written as one string or a list of pieces.
type parts string

func (p *parts) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*p = parts(one)
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*p = parts(strings.Join(many, ""))
	return nil
}

// seatNames is SeatNames' answer: the prefix and the roster.
type seatNames struct {
	Prefix string   `json:"prefix"`
	Roster []string `json:"roster"`
	Why    string   `json:"why"`
}

func loadGoMessages(t *testing.T) goMessages {
	t.Helper()
	b, err := os.ReadFile("testdata/go_messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var m goMessages
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// slash is filepath.ToSlash for a system whose separator is sep. The
// tests give Load paths with forward slashes on every system (Windows
// reads them too), so a message shows the case folder in one form
// only, also inside the repr() of an OS error.
func slash(p string, sep rune) string {
	if sep == '/' {
		return p
	}
	return strings.ReplaceAll(p, string(sep), "/")
}

// norm puts "<dir>/" for the slashed case folder in a message, as
// the golden capture did for Python's temporary folder.
func norm(msg, slashedDir string) string {
	return strings.ReplaceAll(msg, slashedDir+"/", "<dir>/")
}

// TestNormWindows checks the rewrite on the paths Windows gives, so a
// Linux run catches a change that would fail only on Windows.
func TestNormWindows(t *testing.T) {
	dir := `C:\Users\runneradmin\AppData\Local\Temp\TestLoadGolden1\001`
	sd := slash(dir, '\\')
	path := sd + "/missing-file/rules.toml"
	msg := "cannot read " + path + ": [Errno 2] No such file or " +
		"directory: " + pyfmt.ReprString(path)
	want := "cannot read <dir>/missing-file/rules.toml: [Errno 2] No " +
		"such file or directory: '<dir>/missing-file/rules.toml'"
	if got := norm(msg, sd); got != want {
		t.Fatalf("norm = %q\nwant %q", got, want)
	}
	toml := "cannot read " + sd + "/bad-utf8/rules.toml: toml: line 1"
	if got := norm(toml, sd); got !=
		"cannot read <dir>/bad-utf8/rules.toml: toml: line 1" {
		t.Fatalf("norm = %q", got)
	}
}

func loadGolden(t *testing.T) golden {
	t.Helper()
	b, err := os.ReadFile("testdata/golden/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

// place writes case name's file into dir as <name>/rules.toml and
// gives its path with forward slashes (see slash).
func place(t *testing.T, dir, name string) string {
	t.Helper()
	p := slash(filepath.Join(dir, name, "rules.toml"), filepath.Separator)
	os.MkdirAll(filepath.Dir(p), 0o755)
	switch name {
	case "missing-file":
		return p
	case "bad-utf8":
		b, err := os.ReadFile("testdata/golden/files/good.toml")
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, "\n# \xff\xfe\n"...)
		os.WriteFile(p, b, 0o644)
		return p
	}
	b, err := os.ReadFile("testdata/golden/files/" + name + ".toml")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, b, 0o644)
	return p
}

func TestLoadGolden(t *testing.T) {
	g := loadGolden(t)
	gm := loadGoMessages(t)
	goWords, goWarnings := gm.Words, gm.Warnings
	if len(g.Load) < 30 {
		t.Fatalf("%d cases; want 30 or more", len(g.Load))
	}
	dir := t.TempDir()
	sd := slash(dir, filepath.Separator)
	for _, c := range g.Load {
		var rel string
		var want result
		json.Unmarshal(c[0], &rel)
		json.Unmarshal(c[1], &want)
		name := strings.TrimSuffix(rel, "/rules.toml")
		path := place(t, dir, name)
		r, warns, err := Load(path)
		msg := ""
		if err != nil {
			msg = norm(err.Error(), sd)
		}
		if w, ok := goWords[name]; ok {
			if msg != string(w) {
				t.Errorf("%s: got %q\n want %q", name, msg, w)
			}
			continue
		}
		if _, ok := gm.Loads[name]; ok {
			if want.Die == nil || err != nil {
				t.Errorf("%s: Go reads what Python "+
					"refused: got %q, Python %v", name, msg,
					want.Die)
			}
			continue
		}
		switch {
		case want.Die != nil:
			if msg != *want.Die {
				t.Errorf("%s: got %q\nPython %q", name, msg,
					*want.Die)
			}
		case want.Crash != nil:
			t.Errorf("%s: Python crashed (%s) and goWords has no "+
				"Go message for it; Go said %q", name,
				*want.Crash, msg)
		default:
			if err != nil {
				t.Errorf("%s: %v; Python read it", name, err)
				continue
			}
			same(t, name, r, want)
		}
		if !reflect.DeepEqual(warns, goWarnings[name]) &&
			!(len(warns) == 0 && goWarnings[name] == nil) {
			t.Errorf("%s: warnings %q, want %q", name, warns,
				goWarnings[name])
		}
	}
}

// same compares Go's Rules with what load_rules returned.
func same(t *testing.T, name string, r *Rules, w result) {
	t.Helper()
	py := w.Rules
	sec := func(table, key string) any {
		return py[table].(map[string]any)[key]
	}
	num := func(v any) float64 { return v.(float64) }
	check := func(what string, got, want any) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %s = %v, Python %v", name, what, got,
				want)
		}
	}
	check("office.seat_prefix", r.Office.SeatPrefix,
		sec("office", "seat_prefix"))
	var models []any
	for _, m := range r.Office.Models {
		models = append(models, m)
	}
	check("office.models", models, sec("office", "models"))
	check("office.key_life", r.Office.KeyLife, sec("office", "key_life"))
	check("day.length", r.Day.Length, sec("day", "length"))
	check("day.close", r.Day.Close, sec("day", "close"))
	check("day.secs", float64(r.Day.Secs), sec("day", "secs"))
	check("day.close_s", float64(r.Day.CloseS), sec("day", "close_s"))
	check("str(per_week)", r.Day.PerWeek.Text, w.Str["per_week"])
	check("str(usd)", r.Mana.USD.Text, w.Str["usd"])
	check("str(daily)", r.Mana.Daily.Text, w.Str["daily"])
	check("str(seed)", r.Sim.Seed.Text, w.Str["seed"])
	check("usd", r.Mana.USD.F, num(sec("mana", "usd")))
	check("quests.per_seat", float64(r.Quests.PerSeat),
		sec("quests", "per_seat"))
	check("quests.redo_share", r.Quests.RedoShare,
		num(sec("quests", "redo_share")))
	sizes := sec("quests", "sizes").(map[string]any)
	if len(sizes) != len(r.Quests.Sizes) {
		t.Errorf("%s: %d sizes, Python %d", name, len(r.Quests.Sizes),
			len(sizes))
	}
	for k, v := range sizes {
		m := v.(map[string]any)
		s := r.Quests.Sizes[k]
		check("size "+k, []float64{s.Weight, s.Value, float64(s.Calls),
			s.ParMana}, []float64{num(m["weight"]), num(m["value"]),
			num(m["calls"]), num(m["par_mana"])})
	}
	check("score.flush_wait", r.Score.FlushWait,
		sec("score", "flush_wait"))
	check("score.eff_max", r.Score.EffMax, num(sec("score", "eff_max")))
	check("board.individuals", r.Board.Individuals,
		sec("board", "individuals"))
	check("sim.pro_boost", r.Sim.ProBoost, num(sec("sim", "pro_boost")))
	personas := py["personas"].(map[string]any)
	for k, v := range personas {
		m := v.(map[string]any)
		p := r.Personas[k]
		pause := m["pause"].([]any)
		chars := m["chars"].([]any)
		check("persona "+k, []float64{p.Pro, float64(p.Chars[0]),
			float64(p.Chars[1]), float64(p.Reply),
			float64(p.PerStep),
			p.Retry, p.Skill, p.Pause[0], p.Pause[1],
			float64(p.Quests),
			p.Bluff}, []float64{num(m["pro"]), num(chars[0]),
			num(chars[1]), num(m["reply"]), num(m["per_step"]),
			num(m["retry"]), num(m["skill"]), num(pause[0]),
			num(pause[1]), num(m["quests"]), num(m["bluff"])})
		check("persona "+k+" stream", p.Stream, m["stream"])
	}
	seats := py["roster"].(map[string]any)["seats"].([]any)
	if len(seats) != len(r.Roster.Seats) {
		t.Fatalf("%s: %d seats, Python %d", name, len(r.Roster.Seats),
			len(seats))
	}
	for i, v := range seats {
		m := v.(map[string]any)
		check("seat", r.Roster.Seats[i], Seat{m["name"].(string),
			m["persona"].(string), m["guild"].(string)})
	}
	if w.Str["daily_comma"] != "" && !strings.HasPrefix(
		w.Str["daily_comma"], "ValueError") {
		_ = math.Abs // the board prints daily with ":,": pyfmt's test
	}
}

func TestSeatNamesGolden(t *testing.T) {
	g := loadGolden(t)
	gm := loadGoMessages(t)
	dir := t.TempDir()
	for _, c := range g.Seats {
		var rel string
		var want [2]json.RawMessage
		json.Unmarshal(c[0], &rel)
		json.Unmarshal(c[1], &want)
		var wantPrefix string
		var wantRoster []string
		json.Unmarshal(want[0], &wantPrefix)
		json.Unmarshal(want[1], &wantRoster)
		name := strings.TrimSuffix(rel, "/rules.toml")
		if d, ok := gm.Seats[name]; ok {
			wantPrefix, wantRoster = d.Prefix, d.Roster
		}
		prefix, roster := SeatNames(place(t, dir, name))
		set := map[string]bool{}
		for _, s := range roster {
			if set[s] {
				t.Errorf("%s: SeatNames lists %q twice", name,
					s)
			}
			set[s] = true
		}
		got := append([]string(nil), roster...)
		sort.Strings(got)
		if prefix != wantPrefix || !reflect.DeepEqual(got,
			wantRoster) &&
			!(len(got) == 0 && len(wantRoster) == 0) {
			t.Errorf("%s: SeatNames = %q %q, Python %q %q", name,
				prefix, got, wantPrefix, wantRoster)
		}
	}
}

func TestRealRules(t *testing.T) {
	r, warns, err := Load("../../game/rules.toml")
	if err != nil || len(warns) > 0 {
		t.Fatalf("game/rules.toml: %v %q", err, warns)
	}
	if r.Day.Secs != 120 || r.Day.CloseS != 35 ||
		len(r.Roster.Seats) != 10 || r.Mana.Daily.Text != "1500" ||
		!r.Mana.Daily.IsInt || r.Mana.USD.Text != "1e-05" {
		t.Fatalf("game/rules.toml read as %+v", r)
	}
}
