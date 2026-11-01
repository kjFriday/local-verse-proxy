// Package rules reads the office game's rules.toml.
//
// Load is the game's strict read: every table and key the game uses
// must be there with the right type, or it fails with the line; then
// it runs office.py's load_rules checks, in their order and words.
// Unknown keys only warn. SeatNames is Front Desk's tolerant read of
// the seat names alone.
package rules

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/BurntSushi/toml"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// Rules is the whole file.
type Rules struct {
	Office   Office
	Day      Day
	Mana     Mana
	Quests   Quests
	Score    Score
	Board    Board
	Sim      Sim
	Personas map[string]Persona
	Roster   Roster
}

// Office is [office].
type Office struct {
	SeatPrefix string   `toml:"seat_prefix"`
	Models     []string `toml:"models"`
	KeyLife    string   `toml:"key_life"`
}

// Day is [day]. Secs and CloseS are Length and Close in seconds.
type Day struct {
	Length  string `toml:"length"`
	Close   string `toml:"close"`
	PerWeek Num    `toml:"per_week"`
	Secs    int    `toml:"-"` // from Length
	CloseS  int    `toml:"-"` // from Close
}

// Mana is [mana].
type Mana struct {
	USD   Num `toml:"usd"`
	Daily Num `toml:"daily"`
}

// Quests is [quests].
type Quests struct {
	PerSeat     int             `toml:"per_seat"`
	RedoShare   float64         `toml:"redo_share"`
	MaxAttempts int             `toml:"max_attempts"`
	Sizes       map[string]Size `toml:"sizes"`
}

// Size is one [quests.sizes.<name>].
type Size struct {
	Weight  float64 `toml:"weight"`
	Value   float64 `toml:"value"`
	Calls   int     `toml:"calls"`
	ParMana float64 `toml:"par_mana"`
}

// Score is [score].
type Score struct {
	EffMin     float64 `toml:"eff_min"`
	EffMax     float64 `toml:"eff_max"`
	StreakStep float64 `toml:"streak_step"`
	StreakCap  float64 `toml:"streak_cap"`
	FlushWait  string  `toml:"flush_wait"`
}

// Board is [board].
type Board struct {
	Individuals bool `toml:"individuals"`
}

// Sim is [sim].
type Sim struct {
	Seed     Num     `toml:"seed"`
	ProBoost float64 `toml:"pro_boost"`
}

// Persona is one [personas.<name>].
type Persona struct {
	Pro     float64    `toml:"pro"`
	Chars   [2]int     `toml:"chars"`
	Reply   int        `toml:"reply"`
	PerStep int        `toml:"per_step"`
	Retry   float64    `toml:"retry"`
	Skill   float64    `toml:"skill"`
	Pause   [2]float64 `toml:"pause"`
	Quests  int        `toml:"quests"`
	Bluff   float64    `toml:"bluff"`
	Stream  bool       `toml:"stream"`
}

// Roster is [roster].
type Roster struct {
	Seats []Seat `toml:"seats"`
}

// Seat is one roster seat.
type Seat struct{ Name, Persona, Guild string }

// Num is a TOML number (or string) kept as written, so it prints as
// Python's str() would: 1500 as "1500", 1500.0 as "1500.0".
type Num struct {
	Text  string  // Python str()
	F     float64 // the value; 0 for a string
	IsInt bool
}

var (
	durationRE = regexp.MustCompile(`^([1-9][0-9]*)([smhd])$`)
	seatRE     = regexp.MustCompile(`^[a-z][a-z0-9]{0,5}$`)
	prefixRE   = regexp.MustCompile(`^[a-z0-9-]*$`)
	units      = map[string]int{"s": 1, "m": 60, "h": 3600, "d": 86400}
	nextUnit   = map[string]int{"s": 60, "m": 60, "h": 24, "d": 1}
)

// Load reads and checks a rules file. warnings: unknown keys, one
// line each. err text: load_rules' die messages, word for word, or
// for a missing or wrongly typed key "rules.toml line N: <key> ...".
func Load(path string) (r *Rules, warnings []string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read %s: %s", path,
			pyfmt.OSErrorText(err, path))
	}
	raw, err := decode(b)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read %s: %v", path, err)
	}
	c := &checker{name: filepath.Base(path), lines: indexLines(string(b))}
	r = c.rules(raw)
	if c.err != nil {
		return nil, nil, c.err
	}
	warnings = c.unknown(raw)
	if err := check(r); err != nil {
		return nil, warnings, err
	}
	return r, warnings, nil
}

// check is load_rules' own checks, in its order.
func check(r *Rules) error {
	var err error
	if r.Day.Secs, err = seconds(r.Day.Length); err != nil {
		return err
	}
	n, _ := strconv.Atoi(r.Day.Length[:len(r.Day.Length)-1])
	unit := r.Day.Length[len(r.Day.Length)-1:]
	// LiteLLM lines resets up with the clock, so a day must split the
	// next bigger unit evenly, or days would come out uneven.
	if n == 0 || nextUnit[unit]%n != 0 {
		return fmt.Errorf("day.length %s must split the next unit "+
			"evenly, like 1m, 2m, 5m, 1h or 1d",
			pyfmt.ReprString(r.Day.Length))
	}
	if r.Day.CloseS, err = seconds(r.Day.Close); err != nil {
		return err
	}
	if r.Day.CloseS*2 > r.Day.Secs {
		return fmt.Errorf("day.close must be under half of day.length")
	}
	names := map[string]bool{}
	for _, s := range r.Roster.Seats {
		if !seatRE.MatchString(s.Name) {
			return fmt.Errorf("seat name %s: 1-6 lowercase "+
				"letters/digits", pyfmt.ReprString(s.Name))
		}
		if _, ok := r.Personas[s.Persona]; !ok {
			return fmt.Errorf("seat %s: no persona %s", s.Name,
				pyfmt.ReprString(s.Persona))
		}
		if names[s.Name] {
			return fmt.Errorf("seat name %s is used twice",
				pyfmt.ReprString(s.Name))
		}
		names[s.Name] = true
	}
	if !prefixRE.MatchString(r.Office.SeatPrefix) {
		return fmt.Errorf("office.seat_prefix: lowercase letters, " +
			"digits and - only")
	}
	return nil
}

// bom is the byte order mark Windows Notepad can put first.
var bom = []byte("\xef\xbb\xbf")

// errBOM is the TOML reader's refusal of a file that starts with a
// byte order mark. Python's tomllib refuses it too ("Invalid
// statement (at line 1, column 1)"); BurntSushi would skip it.
var errBOM = fmt.Errorf("toml: line 1: the file starts with a byte " +
	"order mark; save it as UTF-8 without BOM")

// decode is the TOML reader, refusing a byte order mark as tomllib
// does, so the game and Front Desk read the files Python read.
func decode(b []byte) (map[string]any, error) {
	if bytes.HasPrefix(b, bom) {
		return nil, errBOM
	}
	var raw map[string]any
	if _, err := toml.Decode(string(b), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// huge stands for a number too big to hold, which no check passes.
// It is far below the largest int, so twice it is still a number.
const huge = 1 << 50

// seconds is office.py's seconds(): "30s", "2m", "1h", "1d". A count
// too big to hold, or whose seconds would be, gives huge.
func seconds(text string) (int, error) {
	m := durationRE.FindStringSubmatch(text)
	if m == nil {
		return 0, fmt.Errorf("bad duration %s: use a whole number and "+
			"s, m, h or d", pyfmt.ReprString(text))
	}
	unit := units[m[2]]
	n, err := strconv.Atoi(m[1])
	if err != nil || n > huge/unit {
		return huge, nil
	}
	return n * unit, nil
}

// SeatNames is Front Desk's tolerant read: str(seat_prefix) and
// prefix + str(name) for each roster seat; any error gives "", nil.
// Python kept the names as a set: a name the file repeats is listed
// once, where it first comes. roster.seats is walked as Python
// iterated it: an empty text or table has no seats, any other text
// or table fails.
func SeatNames(path string) (prefix string, roster []string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	raw, err := decode(b)
	if err != nil {
		return "", nil
	}
	office, ok := raw["office"].(map[string]any)
	if !ok {
		return "", nil
	}
	p, ok := office["seat_prefix"]
	if !ok {
		return "", nil
	}
	prefix = pyfmt.Str(p)
	ros, ok := raw["roster"].(map[string]any)
	if !ok {
		return "", nil
	}
	items, ok := pyIter(ros["seats"])
	if !ok {
		return "", nil
	}
	seen := map[string]bool{}
	for _, it := range items {
		t, ok := it.(map[string]any)
		if !ok {
			return "", nil
		}
		name, ok := t["name"]
		if !ok {
			return "", nil
		}
		seat := prefix + pyfmt.Str(name)
		if !seen[seat] {
			seen[seat] = true
			roster = append(roster, seat)
		}
	}
	return prefix, roster
}

// list is a TOML array, of values or of tables.
func list(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case []map[string]any:
		out := make([]any, len(x))
		for i, t := range x {
			out[i] = t
		}
		return out, true
	}
	return nil, false
}

// pyIter is v as GameSeats' for loop saw it: a list as it is; an
// empty text or table, nothing; any other text or table gave texts,
// which have no "name" (TypeError), so it fails.
func pyIter(v any) ([]any, bool) {
	switch x := v.(type) {
	case string:
		return nil, x == ""
	case map[string]any:
		return nil, len(x) == 0
	}
	return list(v)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
