package game

import (
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
)

// result is office.py's Result: one handed-in (or unfinished) quest,
// as the scorekeeper sees it.
type result struct {
	quest, seat any
	calls       any
	mana        float64
	eff         float64
	verdict     string
	value       any
}

// waitForLogs is office.py's wait_for_logs(): LiteLLM writes spend
// logs in batches; wait until every answered request of the day is
// in, or flush_wait has passed.
func (o *office) waitForLogs(tag string, expected any) (any, float64,
	error) {
	g := o.g
	t0 := g.clk.Wall()
	limit, err := seconds(o.rules.Score.FlushWait)
	if err != nil {
		return nil, 0, err
	}
	for {
		rows, err := g.pg("SELECT count(*) AS n FROM " +
			"\"LiteLLM_SpendLogs\" WHERE " + spendWhere(o.idText(),
			tag))
		if err != nil {
			return nil, 0, err
		}
		var n any
		if len(rows) > 0 {
			n = pyValue(rows[0]["n"])
		}
		if toF(n) >= toF(expected) || g.clk.Wall()-t0 > float64(limit) {
			return n, g.clk.Wall() - t0, nil
		}
		if err := g.sleep(2); err != nil {
			return nil, 0, err
		}
	}
}

type workKey struct{ seat, quest string }

type workDone struct {
	calls any
	mana  float64
}

type use struct {
	calls, tokens any
	mana          float64
}

// scoreDay is office.py's score_day(): the day's work from the spend
// logs, checked against what each player handed in.
func (o *office) scoreDay(day int) error {
	g, r := o.g, o.rules
	usd, sc := r.Mana.USD.F, r.Score
	days, err := o.q("SELECT * FROM days WHERE day = ?", int64(day))
	if err != nil {
		return err
	}
	if len(days) == 0 {
		return die("office.db: no row for day " + str(int64(day)))
	}
	d := days[0]
	logged, waited, err := o.waitForLogs(d.Str("tag"), d.Get("expected"))
	if err != nil {
		return err
	}
	if _, err := o.q("UPDATE days SET logged = ?, flush_s = ? WHERE "+
		"day = ?", logged, waited, int64(day)); err != nil {
		return err
	}
	rows, err := g.pg("SELECT metadata->>'user_api_key_alias' AS alias, " +
		"(SELECT t FROM jsonb_array_elements_text(request_tags) t " +
		" WHERE t LIKE 'seat:%' LIMIT 1) AS seat, " +
		"(SELECT t FROM jsonb_array_elements_text(request_tags) t " +
		" WHERE t LIKE 'quest:%' LIMIT 1) AS quest, " +
		"count(*) AS calls, sum(spend) AS spend, " +
		"sum(total_tokens) AS tokens FROM \"LiteLLM_SpendLogs\" " +
		"WHERE " +
		spendWhere(o.idText(), d.Str("tag")) + " GROUP BY 1, 2, 3")
	if err != nil {
		return err
	}
	ps, err := o.players()
	if err != nil {
		return err
	}
	players := map[string]bool{}
	for _, p := range ps {
		players[p.Str("seat")] = true
	}
	work := map[workKey]workDone{}
	used := map[string]*use{}
	var order []string // used, in Python's dict order
	for _, row := range rows {
		seat, _ := row["alias"].(string)
		mana := toF(row["spend"]) / usd
		if _, ok := row["alias"].(string); !ok || !players[seat] {
			continue
		}
		u := used[seat]
		if u == nil {
			u = &use{calls: int64(0), tokens: int64(0)}
			used[seat] = u
			order = append(order, seat)
		}
		u.calls = add(u.calls, row["calls"])
		u.tokens = add(u.tokens, row["tokens"])
		u.mana += mana
		// Work counts only if the key that paid is the seat it
		// claims.
		tagSeat, _ := row["seat"].(string)
		quest, _ := row["quest"].(string)
		if tagSeat == "seat:"+seat && quest != "" {
			work[workKey{seat, strings.TrimPrefix(quest,
				"quest:")}] = workDone{pyValue(row["calls"]),
				mana}
		}
	}
	for _, seat := range order {
		u := used[seat]
		if _, err := o.q("INSERT INTO usage VALUES (?,?,?,?,?)",
			int64(day), seat, u.calls, u.tokens,
			u.mana); err != nil {
			return err
		}
	}
	qrows, err := o.q("SELECT * FROM quests WHERE day = ?", int64(day))
	if err != nil {
		return err
	}
	quests := map[string]gamestate.Row{}
	var qorder []string
	for _, q := range qrows {
		id := q.Str("id")
		if _, ok := quests[id]; !ok {
			qorder = append(qorder, id)
		}
		quests[id] = q
	}
	comps, err := o.q("SELECT * FROM completions WHERE day = ?",
		int64(day))
	if err != nil {
		return err
	}
	var results []result
	for _, c := range comps {
		q, ok := quests[c.Str("quest")]
		if !ok {
			return die("office.db: completion " + c.Str("quest") +
				" has no quest")
		}
		w, ok := work[workKey{c.Str("seat"), c.Str("quest")}]
		if !ok {
			w = workDone{int64(0), 0.0}
		}
		var verdict string
		switch {
		case !truthy(c.Get("approved")):
			verdict = "rejected"
		case toF(w.calls) < toF(q.Get("calls")):
			verdict = "unbacked" // handed in without the work
		default:
			verdict = "counted"
		}
		eff := sc.EffMax
		if w.mana > 0 {
			eff = q.Float("par_mana") / w.mana
		}
		eff = pyMax(sc.EffMin, pyMin(sc.EffMax, eff))
		results = append(results, result{c.Get("quest"), c.Get("seat"),
			w.calls, w.mana, eff, verdict, q.Get("value")})
	}
	for _, id := range qorder {
		q := quests[id]
		if q.Str("state") != "unfinished" {
			continue
		}
		w, ok := work[workKey{q.Str("seat"), id}]
		if !ok {
			w = workDone{int64(0), 0.0}
		}
		results = append(results, result{q.Get("id"), q.Get("seat"),
			w.calls, w.mana, 0.0, "unfinished", int64(0)})
	}
	for _, res := range results {
		bonus, points := 0.0, 0.0
		if res.verdict == "counted" {
			n, err := o.streak(str(res.seat), day)
			if err != nil {
				return err
			}
			bonus = pyMin(sc.StreakCap, float64(sc.StreakStep*
				float64(n)))
			// float64(...) rounds each product: no fused
			// multiply-add on arm64, as Python.
			points = float64(float64(toF(res.value)*res.eff) *
				(1 + bonus))
		}
		if _, err := o.q("INSERT INTO scores VALUES "+
			"(?,?,?,?,?,?,?,?,?)",
			int64(day), res.quest, res.seat, res.calls, res.mana,
			res.eff, bonus, points, res.verdict); err != nil {
			return err
		}
	}
	return nil
}

// streak is office.py's streak(): days in a row before day with at
// least one counted quest.
func (o *office) streak(seat string, day int) (int, error) {
	n := 0
	for d := day - 1; d > 0; d-- {
		rows, err := o.q("SELECT 1 FROM scores WHERE day = ? AND "+
			"seat = ? AND verdict = 'counted' LIMIT 1", int64(d),
			seat)
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			break
		}
		n++
	}
	return n, nil
}
