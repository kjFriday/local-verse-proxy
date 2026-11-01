package game

import (
	"sort"
	"strconv"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
	"github.com/kjFriday/local-verse-proxy/internal/rules"
)

// f is pyfmt.Format, short.
func f(v any, spec string) string { return pyfmt.Format(pyValue(v), spec) }

// rankCorr is office.py's rank_corr(): Spearman rank correlation of
// two orderings of the same names.
func rankCorr(a, b []string) float64 {
	n := len(a)
	if n < 2 {
		return 0.0
	}
	pos := map[string]int{}
	for i, x := range b {
		pos[x] = i
	}
	d2 := 0
	for i, x := range a {
		d := i - pos[x]
		d2 += d * d
	}
	return 1 - float64(6*d2)/float64(n*(n*n-1))
}

// scoredDays is office.py's scored_days(): the days a board covers,
// one day or every scored day; nil when no day was played. day 0 is
// Python's None.
func (o *office) scoredDays(day int) ([]int, int, error) {
	v, err := o.meta("day")
	if err != nil {
		return nil, 0, err
	}
	last := 0
	if truthy(v) {
		n, ok := pyInt(str(v))
		if !ok {
			return nil, 0, die("office.db: meta day " +
				pyfmt.ReprString(str(v)) + " is not a number")
		}
		last = n
	}
	if last == 0 {
		o.g.say("No day played yet: run `%s game run`.", o.g.prog)
		return nil, last, nil
	}
	if day != 0 && !(1 <= day && day <= last) {
		return nil, 0, die("no day " + strconv.Itoa(day) +
			" yet: days " +
			"1 to " + strconv.Itoa(last) + " are scored.")
	}
	if day != 0 {
		return []int{day}, last, nil
	}
	days := make([]int, last)
	for i := range days {
		days[i] = i + 1
	}
	return days, last, nil
}

// heading is office.py's heading().
func (o *office) heading(day, last int) {
	r := o.rules
	when := "after day " + strconv.Itoa(last)
	if day != 0 {
		when = "day " + strconv.Itoa(day)
	}
	o.g.say("Office %s, practice week: %s of %s (%s days)", o.idText(),
		when, r.Day.PerWeek.Text, r.Day.Length)
	o.g.say("1 mana = $%s; daily mana %s per seat", money(r.Mana.USD.F),
		numFormat(r.Mana.Daily, ","))
	o.g.say("")
}

// numFormat is format() of a rules number, an int or a float as it
// was written.
func numFormat(n rules.Num, spec string) string {
	if n.IsInt {
		if i, err := strconv.ParseInt(n.Text, 10, 64); err == nil {
			return pyfmt.Format(i, spec)
		}
	}
	return pyfmt.Format(n.F, spec)
}

func marks(days []int) (string, []any) {
	q := make([]string, len(days))
	a := make([]any, len(days))
	for i, d := range days {
		q[i], a[i] = "?", int64(d)
	}
	return strings.Join(q, ","), a
}

type points struct {
	points float64
	done   int64
	eff    float64
}

type usage struct {
	tokens any
	mana   float64
}

// board is office.py's board(): the guilds, then the two individual
// boards side by side, then the Goodhart check. day 0: every day.
func (o *office) board(day int) error {
	g, r := o.g, o.rules
	days, last, err := o.scoredDays(day)
	if err != nil || len(days) == 0 {
		return err
	}
	m, dayArgs := marks(days)
	players, err := o.players()
	if err != nil {
		return err
	}
	var seats []string
	pts := map[string]*points{}
	use := map[string]*usage{}
	info := map[string]gamestate.Row{}
	for _, p := range players {
		s := p.Str("seat")
		if _, ok := pts[s]; !ok {
			seats = append(seats, s)
		}
		pts[s] = &points{}
		use[s] = &usage{tokens: int64(0)}
		info[s] = p
	}
	rows, err := o.q("SELECT * FROM scores WHERE day IN ("+m+") "+
		"AND verdict = 'counted'", dayArgs...)
	if err != nil {
		return err
	}
	for _, row := range rows {
		p := pts[row.Str("seat")]
		if p == nil {
			return die("office.db: scores name seat " +
				pyfmt.ReprString(row.Str("seat")) +
				", who is not a player")
		}
		p.points += row.Float("points")
		p.done++
		p.eff += row.Float("eff")
	}
	rows, err = o.q("SELECT * FROM usage WHERE day IN ("+m+")",
		dayArgs...)
	if err != nil {
		return err
	}
	for _, row := range rows {
		u := use[row.Str("seat")]
		if u == nil {
			return die("office.db: usage names seat " +
				pyfmt.ReprString(row.Str("seat")) +
				", who is not a player")
		}
		u.tokens = add(u.tokens, row.Get("tokens"))
		u.mana += row.Float("mana")
	}
	o.heading(day, last)
	type guild struct {
		name string
		avg  float64
		size int
	}
	var guilds []guild
	grows, err := o.q("SELECT name FROM guilds ORDER BY name")
	if err != nil {
		return err
	}
	for _, gr := range grows {
		name := str(gr.Get("name"))
		var members []string
		for _, p := range players {
			if str(p.Get("guild")) == name {
				members = append(members, p.Str("seat"))
			}
		}
		if len(members) == 0 {
			continue
		}
		var each []float64
		for _, s := range members {
			each = append(each, pts[s].points)
		}
		guilds = append(guilds, guild{name, fsum(each) /
			float64(len(members)), len(members)})
	}
	// top = max(avgs + [1]): 1 unless an average is larger.
	top := 1.0
	for _, gd := range guilds {
		if gd.avg > top {
			top = gd.avg
		}
	}
	g.say("GUILDS: points per member (finished, efficient work)")
	sort.SliceStable(guilds, func(i, j int) bool {
		return -guilds[i].avg < -guilds[j].avg
	})
	for _, gd := range guilds {
		bar := strings.Repeat("#", int(pyfmt.RoundInt(30*gd.avg/top)))
		g.say("  %s %d members %s  %s", f(gd.name, "<8"), gd.size,
			f(gd.avg, ">7.1f"), bar)
	}
	g.say("")
	byPts := append([]string{}, seats...)
	sort.SliceStable(byPts, func(i, j int) bool {
		a, b := pts[byPts[i]], pts[byPts[j]]
		if a.points != b.points {
			return -a.points < -b.points
		}
		if a.done != b.done {
			return -a.done < -b.done
		}
		return byPts[i] < byPts[j]
	})
	byTok := append([]string{}, seats...)
	sort.SliceStable(byTok, func(i, j int) bool {
		a, b := toF(use[byTok[i]].tokens), toF(use[byTok[j]].tokens)
		if a != b {
			return -a < -b
		}
		return byTok[i] < byTok[j]
	})
	byMana := append([]string{}, seats...)
	sort.SliceStable(byMana, func(i, j int) bool {
		a, b := use[byMana[i]].mana, use[byMana[j]].mana
		if a != b {
			return -a < -b
		}
		return byMana[i] < byMana[j]
	})
	if !r.Board.Individuals {
		g.say("(Individual boards are off in rules.toml.)")
		return nil
	}
	if len(seats) == 0 {
		return die("office.db: the office has no players")
	}
	name := func(s string) string { return str(info[s].Get("name")) }
	persona := func(s string) string { return str(info[s].Get("persona")) }
	left := []string{"WHO FINISHED WORK (game score)",
		" # seat   persona  done  eff    pts"}
	for i, s := range byPts {
		p := pts[s]
		eff := 0.0
		if p.done != 0 {
			eff = p.eff / float64(p.done)
		}
		left = append(left, f(int64(i+1), ">2")+" "+
			f(name(s), "<6")+" "+f(persona(s), "<8")+" "+
			f(p.done, ">4")+" "+f(eff, ">4.2f")+" "+
			f(p.points, ">6.1f"))
	}
	right := []string{"WHO BURNED TOKENS (naive board)",
		" # seat      tokens   mana  done"}
	for i, s := range byTok {
		right = append(right, f(int64(i+1), ">2")+" "+
			f(name(s), "<6")+" "+f(use[s].tokens, ">9,")+" "+
			f(use[s].mana, ">6,.0f")+" "+f(pts[s].done, ">4"))
	}
	for i := range left {
		g.say("%s| %s", f(left[i], "<36"), right[i])
	}
	g.say("")
	t, w := byTok[0], byPts[0]
	g.say("GOODHART CHECK")
	lines := []struct{ label, s, what string }{
		{"Most tokens:", t, f(use[t].tokens, ",") + " tokens"},
		{"Most mana:", byMana[0], f(use[byMana[0]].mana, ",.0f") +
			" mana"},
		{"Top score:", w, f(use[w].tokens, ",") + " tokens"},
	}
	for _, l := range lines {
		g.say("  %s %s (%s), %s, %d done, %s pts", f(l.label, "<12"),
			name(l.s), persona(l.s), l.what, pts[l.s].done,
			f(pts[l.s].points, ".0f"))
	}
	if t != w && truthy(use[w].tokens) && pts[w].done != 0 {
		g.say("  %s burned %s the tokens of %s, for %s the finished "+
			"quests", name(t),
			f(toF(use[t].tokens)/toF(use[w].tokens), ".1f")+"x",
			name(w), f(float64(pts[t].done)/float64(pts[w].done),
				".2f")+"x")
		g.say("  and %sx the points.", f(pts[t].points/pts[w].points,
			".2f"))
	}
	rho := rankCorr(byTok, byPts)
	g.say("  Rank match, tokens vs score: %s (+1 same order, 0 none, "+
		"-1 reversed)", f(rho, "+.2f"))
	return nil
}

// seatBoard is office.py's seat_board(): one seat, quest by quest, so
// every point can be checked.
func (o *office) seatBoard(name string, day int) error {
	g := o.g
	days, last, err := o.scoredDays(day)
	if err != nil || len(days) == 0 {
		return err
	}
	ps, err := o.q("SELECT * FROM players WHERE name = ? OR seat = ?",
		name, name)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		return die("no seat " + pyfmt.ReprString(name) + ": see `" +
			g.prog + " game board` for the names.")
	}
	p := ps[0]
	m, dayArgs := marks(days)
	rows, err := o.q("SELECT s.*, q.size, q.value, q.par_mana, "+
		"q.calls AS need FROM scores s "+
		"JOIN quests q ON q.id = s.quest "+
		"WHERE s.seat = ? AND s.day IN ("+m+") "+
		"ORDER BY s.day, s.quest", append([]any{p.Get("seat")},
		dayArgs...)...)
	if err != nil {
		return err
	}
	o.heading(day, last)
	g.say("%s (%s, guild %s): points = value x eff x (1 + bonus)",
		str(p.Get("name")), str(p.Get("persona")), str(p.Get("guild")))
	g.say(" day quest  size   calls par   mana  eff bonus value    pts " +
		"verdict")
	var value, mana, par, pts []any
	n := 0
	for _, r := range rows {
		g.say("%s %s %s %s/%s %s %s %s %s %s %s %s", f(r.Get("day"),
			">4"),
			f(r.Get("quest"), "<6"), f(r.Get("size"), "<6"),
			f(r.Get("calls"), ">2"), f(r.Get("need"), "<2"),
			f(r.Get("par_mana"), ">3.0f"), f(r.Get("mana"),
				">6.1f"),
			f(r.Get("eff"), ">4.2f"), f(r.Get("bonus"), ">+5.0%"),
			f(r.Get("value"), ">5.0f"), f(r.Get("points"), ">6.1f"),
			str(r.Get("verdict")))
		if str(r.Get("verdict")) == "counted" {
			n++
			value = append(value, r.Get("value"))
			mana = append(mana, r.Get("mana"))
			par = append(par, r.Get("par_mana"))
			pts = append(pts, r.Get("points"))
		}
	}
	g.say("Counted %d of %d: value %s, mana %s (par %s), points %s.", n,
		len(rows), f(sum(value), ",.0f"), f(sum(mana), ",.1f"),
		f(sum(par), ",.0f"), f(sum(pts), ",.1f"))
	g.say("calls: tagged calls in the spend logs / the fewest the " +
		"quest needs.")
	return nil
}
