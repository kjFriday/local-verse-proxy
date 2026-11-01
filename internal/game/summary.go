package game

import (
	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// counts is dict(office.q("SELECT x, count(*) ... GROUP BY 1")).
func counts(rows []gamestate.Row) (map[string]any, []string) {
	m := map[string]any{}
	var order []string
	for _, r := range rows {
		k := str(r.At(0))
		if _, ok := m[k]; !ok {
			order = append(order, k)
		}
		m[k] = r.At(1)
	}
	return m, order
}

func get(m map[string]any, k string) any {
	if v, ok := m[k]; ok {
		return v
	}
	return int64(0)
}

// daySummary is office.py's day_summary().
func (o *office) daySummary(day int) error {
	g := o.g
	days, err := o.q("SELECT * FROM days WHERE day = ?", int64(day))
	if err != nil {
		return err
	}
	if len(days) == 0 {
		return die("office.db: no row for day " + str(int64(day)))
	}
	d := days[0]
	stRows, err := o.q("SELECT verdict, count(*) FROM scores "+
		"WHERE day = ? GROUP BY 1", int64(day))
	if err != nil {
		return err
	}
	qsRows, err := o.q("SELECT state, count(*) FROM quests "+
		"WHERE day = ? GROUP BY 1", int64(day))
	if err != nil {
		return err
	}
	st, _ := counts(stRows)
	qs, qorder := counts(qsRows)
	g.say("Day %d done: bots worked %s s, %s calls answered.", day,
		pyfmt.Format(d.Get("worked"), ".0f"), str(d.Get("expected")))
	note := ""
	if toF(d.Get("logged")) < toF(d.Get("expected")) {
		note = "  (INCOMPLETE)"
	}
	g.say("  spend logs: %s of %s rows after %s s%s", str(d.Get("logged")),
		str(d.Get("expected")), pyfmt.Format(d.Get("flush_s"), ".0f"),
		note)
	var posted []any
	for _, k := range qorder {
		posted = append(posted, qs[k])
	}
	g.say("  quests: %s posted, %s counted, %s thrown out (too few "+
		"calls), %s given up,", str(sum(posted)),
		str(get(st, "counted")), str(get(st, "unbacked")),
		str(get(st, "rejected")))
	g.say("          %s unfinished, %s never picked up",
		str(get(st, "unfinished")), str(get(qs, "expired")))
	outs, err := o.q("SELECT l.*, p.name FROM daylog l JOIN players p "+
		"USING (seat) WHERE day = ? AND refused > 0 "+
		"ORDER BY out_at", int64(day))
	if err != nil {
		return err
	}
	for _, out := range outs {
		at := toF(out.Get("out_at")) - toF(d.Get("started"))
		kind := out.Get("kind")
		var why string
		switch {
		case str(kind) == "budget_exceeded":
			why = "out of mana"
		case truthy(kind):
			why = "error " + str(kind)
		default:
			why = "error"
		}
		g.say("  %s: %s at %s:%s into the day (HTTP %s), idle after "+
			"that", str(out.Get("name")), why,
			pyfmt.Format(pyfmt.FloorDiv(at, 60), ".0f"),
			pyfmt.Format(pyfmt.Mod(at, 60), "02.0f"),
			str(out.Get("code")))
	}
	g.say("")
	return nil
}
