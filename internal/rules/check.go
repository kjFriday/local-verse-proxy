package rules

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// checker fills Rules from the decoded file, stopping at the first
// missing or wrongly typed key.
type checker struct {
	name  string         // the file's name, for messages
	lines map[string]int // key path -> line, from indexLines
	err   error
}

func (c *checker) failf(path, format string, a ...any) {
	if c.err != nil {
		return
	}
	msg := fmt.Sprintf(format, a...)
	if n := c.line(path); n > 0 {
		c.err = fmt.Errorf("%s line %d: %s %s", c.name, n, path, msg)
		return
	}
	c.err = fmt.Errorf("%s: %s %s", c.name, path, msg)
}

// line is where path is, or where the nearest table above it is.
func (c *checker) line(path string) int {
	for p := path; p != ""; {
		if n, ok := c.lines[p]; ok {
			return n
		}
		k := strings.LastIndexAny(p, ".[")
		if k < 0 {
			break
		}
		p = p[:k]
	}
	return 0
}

// table is the table at path, or nil after a failure.
func (c *checker) table(parent map[string]any,
	path, key string) map[string]any {
	if c.err != nil {
		return nil
	}
	v, ok := parent[key]
	if !ok {
		c.failf(path, "is missing")
		return nil
	}
	t, ok := v.(map[string]any)
	if !ok {
		c.failf(path, "must be a table")
		return nil
	}
	return t
}

func (c *checker) get(t map[string]any, path, key string) (any, bool) {
	if c.err != nil || t == nil {
		return nil, false
	}
	v, ok := t[key]
	if !ok {
		c.failf(path+"."+key, "is missing")
		return nil, false
	}
	return v, true
}

func (c *checker) str(t map[string]any, path, key string) string {
	v, ok := c.get(t, path, key)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		c.failf(path+"."+key, "must be text, in quotes")
	}
	return s
}

func (c *checker) int(t map[string]any, path, key string) int {
	v, ok := c.get(t, path, key)
	if !ok {
		return 0
	}
	n, ok := v.(int64)
	if !ok {
		c.failf(path+"."+key, "must be a whole number")
	} else if n > math.MaxInt32 || n < math.MinInt32 {
		c.failf(path+"."+key, "must be a whole number from %d to %d",
			math.MinInt32, math.MaxInt32)
	}
	return int(n)
}

func (c *checker) num(t map[string]any, path, key string) float64 {
	v, ok := c.get(t, path, key)
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	}
	c.failf(path+"."+key, "must be a number")
	return 0
}

func (c *checker) pyNum(t map[string]any, path, key string,
	text bool) Num {
	v, ok := c.get(t, path, key)
	if !ok {
		return Num{}
	}
	switch x := v.(type) {
	case int64:
		return Num{Text: strconv.FormatInt(x, 10), F: float64(x),
			IsInt: true}
	case float64:
		return Num{Text: pyfmt.Repr(x), F: x}
	case string:
		if text {
			return Num{Text: x}
		}
	}
	if text {
		c.failf(path+"."+key, "must be a number or text")
	} else {
		c.failf(path+"."+key, "must be a number")
	}
	return Num{}
}

func (c *checker) bool(t map[string]any, path, key string) bool {
	v, ok := c.get(t, path, key)
	if !ok {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		c.failf(path+"."+key, "must be true or false")
	}
	return b
}

func (c *checker) strs(t map[string]any, path, key string) []string {
	v, ok := c.get(t, path, key)
	if !ok {
		return nil
	}
	items, ok := v.([]any)
	out := []string{}
	for _, it := range items {
		s, isStr := it.(string)
		ok = ok && isStr
		out = append(out, s)
	}
	if !ok {
		c.failf(path+"."+key, "must be a list of text, like "+
			"[\"local\", \"pro\"]")
	}
	return out
}

func (c *checker) pair(t map[string]any, path, key string,
	whole bool) [2]float64 {
	var out [2]float64
	v, ok := c.get(t, path, key)
	if !ok {
		return out
	}
	items, ok := v.([]any)
	ok = ok && len(items) == 2
	for i := 0; ok && i < 2; i++ {
		switch x := items[i].(type) {
		case int64:
			out[i] = float64(x)
		case float64:
			out[i] = x
			ok = !whole
		default:
			ok = false
		}
	}
	if !ok {
		what := "numbers"
		if whole {
			what = "whole numbers"
		}
		c.failf(path+"."+key, "must be a list of two %s", what)
	}
	return out
}

func (c *checker) rules(raw map[string]any) *Rules {
	r := &Rules{}
	o := c.table(raw, "office", "office")
	r.Office.SeatPrefix = c.str(o, "office", "seat_prefix")
	r.Office.Models = c.strs(o, "office", "models")
	r.Office.KeyLife = c.str(o, "office", "key_life")

	d := c.table(raw, "day", "day")
	r.Day.Length = c.str(d, "day", "length")
	r.Day.Close = c.str(d, "day", "close")
	r.Day.PerWeek = c.pyNum(d, "day", "per_week", false)

	m := c.table(raw, "mana", "mana")
	r.Mana.USD = c.pyNum(m, "mana", "usd", false)
	r.Mana.Daily = c.pyNum(m, "mana", "daily", false)

	q := c.table(raw, "quests", "quests")
	r.Quests.PerSeat = c.int(q, "quests", "per_seat")
	r.Quests.RedoShare = c.num(q, "quests", "redo_share")
	r.Quests.MaxAttempts = c.int(q, "quests", "max_attempts")
	sizes := c.table(q, "quests.sizes", "sizes")
	r.Quests.Sizes = map[string]Size{}
	for _, name := range c.inFileOrder(sizes, "quests.sizes.") {
		p := "quests.sizes." + tomlKey(name)
		t := c.table(sizes, p, name)
		r.Quests.Sizes[name] = Size{
			Weight:  c.num(t, p, "weight"),
			Value:   c.num(t, p, "value"),
			Calls:   c.int(t, p, "calls"),
			ParMana: c.num(t, p, "par_mana"),
		}
	}
	c.sizes(r.Quests.Sizes)

	s := c.table(raw, "score", "score")
	r.Score.EffMin = c.num(s, "score", "eff_min")
	r.Score.EffMax = c.num(s, "score", "eff_max")
	r.Score.StreakStep = c.num(s, "score", "streak_step")
	r.Score.StreakCap = c.num(s, "score", "streak_cap")
	r.Score.FlushWait = c.str(s, "score", "flush_wait")

	b := c.table(raw, "board", "board")
	r.Board.Individuals = c.bool(b, "board", "individuals")

	sim := c.table(raw, "sim", "sim")
	r.Sim.Seed = c.pyNum(sim, "sim", "seed", true)
	r.Sim.ProBoost = c.num(sim, "sim", "pro_boost")

	ps := c.table(raw, "personas", "personas")
	r.Personas = map[string]Persona{}
	for _, name := range c.inFileOrder(ps, "personas.") {
		p := "personas." + tomlKey(name)
		t := c.table(ps, p, name)
		chars := c.pair(t, p, "chars", true)
		if c.err == nil && chars[0] > chars[1] {
			// Python's randint stopped that bot mid-day.
			c.failf(p+".chars", "must be [low, high], like "+
				"[300, 700]")
		}
		var pe Persona
		pe.Pro = c.num(t, p, "pro")
		pe.Chars = [2]int{int(chars[0]), int(chars[1])}
		pe.Reply = c.int(t, p, "reply")
		pe.PerStep = c.int(t, p, "per_step")
		pe.Retry = c.num(t, p, "retry")
		pe.Skill = c.num(t, p, "skill")
		pe.Pause = c.pair(t, p, "pause", false)
		pe.Quests = c.int(t, p, "quests")
		pe.Bluff = c.num(t, p, "bluff")
		pe.Stream = c.bool(t, p, "stream")
		r.Personas[name] = pe
	}

	ro := c.table(raw, "roster", "roster")
	if v, ok := c.get(ro, "roster", "seats"); ok {
		items, ok := list(v)
		if !ok {
			c.failf("roster.seats", "must be a list of seats, "+
				"like "+seatLike)
		}
		for i, it := range items {
			p := fmt.Sprintf("roster.seats[%d]", i)
			t, ok := it.(map[string]any)
			if !ok {
				c.failf(p, "must be a seat, like "+seatLike)
				break
			}
			r.Roster.Seats = append(r.Roster.Seats, Seat{
				Name:    c.str(t, p, "name"),
				Persona: c.str(t, p, "persona"),
				Guild:   c.str(t, p, "guild"),
			})
		}
	}
	if c.err != nil {
		return nil
	}
	return r
}

// sizes refuses what random.choices refused when the first quest
// was drawn: no sizes, or weights that do not add up to a number
// above 0. Python added them in sorted order, as Go does.
func (c *checker) sizes(sizes map[string]Size) {
	if c.err != nil {
		return
	}
	if len(sizes) == 0 {
		c.failf("quests.sizes", "must have at least one size, like "+
			"[quests.sizes.small]")
		return
	}
	names := make([]string, 0, len(sizes))
	for n := range sizes {
		names = append(names, n)
	}
	sort.Strings(names)
	total := 0.0
	for _, n := range names {
		total += sizes[n].Weight
	}
	switch {
	case total <= 0:
		c.failf("quests.sizes", "weights must add up to more than 0")
	case math.IsInf(total, 0) || math.IsNaN(total):
		c.failf("quests.sizes", "weights must add up to a finite "+
			"number")
	}
}

// inFileOrder is t's keys in the order the file has them.
func (c *checker) inFileOrder(t map[string]any, prefix string) []string {
	keys := sortedKeys(t)
	at := func(k string) int {
		if n := c.line(prefix + tomlKey(k)); n > 0 {
			return n
		}
		return math.MaxInt32
	}
	sort.SliceStable(keys, func(i, j int) bool {
		return at(keys[i]) < at(keys[j])
	})
	return keys
}

const seatLike = `{name = "fern", persona = "frugal", guild = "owls"}`

// known is every key the game reads; "*" stands for any name.
var known = map[string][]string{
	"": {"office", "day", "mana", "quests", "score", "board", "sim",
		"personas", "roster"},
	"office":         {"seat_prefix", "models", "key_life"},
	"day":            {"length", "close", "per_week"},
	"mana":           {"usd", "daily"},
	"quests":         {"per_seat", "redo_share", "max_attempts", "sizes"},
	"quests.sizes":   {"*"},
	"quests.sizes.*": {"weight", "value", "calls", "par_mana"},
	"score": {"eff_min", "eff_max", "streak_step", "streak_cap",
		"flush_wait"},
	"board":    {"individuals"},
	"sim":      {"seed", "pro_boost"},
	"personas": {"*"},
	"personas.*": {"pro", "chars", "reply", "per_step", "retry",
		"skill", "pause", "quests", "bluff", "stream"},
	"roster":         {"seats"},
	"roster.seats.*": {"name", "persona", "guild"},
}

// unknown is one warning per key the game does not read, in file
// order.
func (c *checker) unknown(raw map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	warn := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		msg := "unknown key " + path + " (ignored)"
		if n := c.line(path); n > 0 {
			out = append(out, fmt.Sprintf("%s line %d: %s",
				c.name, n, msg))
		} else {
			out = append(out, c.name+": "+msg)
		}
	}
	var walk func(v any, path, pattern string)
	walk = func(v any, path, pattern string) {
		t, ok := v.(map[string]any)
		items, isList := list(v)
		if !ok && isList && pattern == "roster.seats" {
			for i, it := range items {
				walk(it, fmt.Sprintf("%s[%d]", path, i),
					pattern+".*")
			}
		}
		if !ok {
			return
		}
		keys, ok := known[pattern]
		if !ok {
			return
		}
		for _, k := range sortedKeys(t) {
			sub := tomlKey(k)
			if path != "" {
				sub = path + "." + tomlKey(k)
			}
			pat := k
			if pattern != "" {
				pat = pattern + "." + k
			}
			match := false
			for _, want := range keys {
				if want == k {
					match = true
				} else if want == "*" {
					match, pat = true, pattern+".*"
				}
			}
			if !match {
				warn(sub)
				continue
			}
			walk(t[k], sub, pat)
		}
	}
	walk(raw, "", "")
	// In file order, as a reader goes through it.
	sortStable(out, func(a, b string) bool {
		return c.lineOf(a) < c.lineOf(b)
	})
	return out
}

func (c *checker) lineOf(warning string) int {
	_, rest, ok := strings.Cut(warning, " line ")
	if !ok {
		return math.MaxInt32
	}
	n, _ := strconv.Atoi(strings.SplitN(rest, ":", 2)[0])
	return n
}

func sortStable(s []string, less func(a, b string) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// tomlKey is a key as TOML writes it in a dotted path.
func tomlKey(k string) string { return toml.Key{k}.String() }
