package game

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// say prints one line on standard output.
func (g *game) say(format string, a ...any) {
	fmt.Fprintf(g.out, format+"\n", a...)
}

// sleep is time.sleep on the game's clock; Ctrl+C ends it.
func (g *game) sleep(s float64) error {
	if g.clk.Sleep(g.ctx, s) != nil {
		return errStopped
	}
	return nil
}

// waitForDay is office.py's wait_for_day(): start a day right after
// LiteLLM refills every seat's mana. Returns when the day started and
// when the mana next resets.
func (o *office) waitForDay(players []gamestate.Row,
	day int) (started, ends float64, err error) {
	g, r := o.g, o.rules
	seats := make([]string, len(players))
	for i, p := range players {
		seats[i] = p.Str("seat")
	}
	state, err := g.seatState(seats)
	if err != nil {
		return 0, 0, err
	}
	var missing []string
	for _, s := range seats {
		if _, ok := state[s]; !ok {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		return 0, 0, die("no key in the proxy for: " +
			strings.Join(missing, ", ") + ". Run `" + g.prog +
			" game reset`, then `" + g.prog + " game setup`.")
	}
	start := minReset(state)
	// A fresh period began moments ago and nobody has spent yet
	// (say, right after setup): start now instead of waiting a day.
	secs := float64(r.Day.Secs)
	began := start - secs
	unspent := true
	for _, row := range state {
		unspent = unspent && toF(row["spend"]) <= 0
	}
	if g.clk.Wall()-began < secs/4 && unspent {
		g.say("Day %d starts now; mana resets at %s UTC.", day,
			clockText(start))
		return g.clk.Wall(), start, nil
	}
	wait := start - g.clk.Wall()
	soon := "now"
	if wait >= 1 {
		soon = "in " + pyfmt.Format(wait, ".0f") + " s"
	}
	g.say("Day %d starts at %s UTC, when LiteLLM refills the mana (%s).",
		day, clockText(start), soon)
	if wait > 0 {
		if err := g.sleep(wait); err != nil {
			return 0, 0, err
		}
	}
	// The reset job runs every few seconds; wait until it has run for
	// every seat. If it is late, re-mint those seats (a fresh key has
	// a fresh budget), so the day still starts fair.
	giveUp := g.clk.Wall() + 60
	for {
		state, err = g.seatState(seats)
		if err != nil {
			return 0, 0, err
		}
		var late []string
		for _, s := range seats {
			row, ok := state[s]
			if !ok || toF(row["reset_at"]) <= start+1 {
				late = append(late, s)
			}
		}
		if len(late) == 0 {
			break
		}
		if g.clk.Wall() > giveUp {
			g.say("  reset job late; re-minting %d seats",
				len(late))
			for _, s := range late {
				gone, err := g.revokeFn(s)
				if err != nil {
					return 0, 0, err
				}
				if gone != "revoked" && gone != "no key" {
					return 0, 0, die("revoke " + s +
						" failed: " +
						gone)
				}
				why, err := g.mintFn(s)
				if err != nil {
					return 0, 0, err
				}
				if why != "" {
					return 0, 0, die("re-mint " + s +
						" failed: " +
						why)
				}
			}
			break
		}
		if err := g.sleep(1); err != nil {
			return 0, 0, err
		}
	}
	state, err = g.seatState(seats)
	if err != nil {
		return 0, 0, err
	}
	return g.clk.Wall(), minReset(state), nil
}

// minReset is min(r["reset_at"] for r in state.values()).
func minReset(state map[string]map[string]any) float64 {
	first, m := true, 0.0
	for _, row := range state {
		v := toF(row["reset_at"])
		if first || v < m {
			m, first = v, false
		}
	}
	return m
}

// playDay is office.py's play_day(): the day's quests, a bot per
// player until the deadline, then the score.
func (o *office) playDay(day int, url string) (float64, error) {
	g, r := o.g, o.rules
	players, err := o.players()
	if err != nil {
		return 0, err
	}
	for _, p := range players {
		seat := p.Str("seat")
		if _, err := os.Stat(filepath.Join(g.keys,
			seat+".key")); err != nil {
			return 0, die("missing game/keys/" + seat +
				".key: run `" +
				g.prog + " game reset`, then `" + g.prog +
				" game setup`.")
		}
	}
	// A day that never finished is played again.
	for _, t := range []string{"quests", "completions", "daylog",
		"usage", "scores", "days"} {
		if _, err := o.q("DELETE FROM "+t+" WHERE day = ?",
			int64(day)); err != nil {
			return 0, err
		}
	}
	started, ends, err := o.waitForDay(players, day)
	if err != nil {
		return 0, err
	}
	tag := fmt.Sprintf("%d.%s", day, g.tokenHex(2))
	posted, err := o.postQuests(day)
	if err != nil {
		return 0, err
	}
	deadline := ends - float64(r.Day.CloseS)
	g.say("  %d quests posted; bots work until %s UTC, mana resets at "+
		"%s.", posted, clockText(deadline), clockText(ends))
	bots := make([]*bot, 0, len(players))
	for _, p := range players {
		b, err := newBot(g.ctx, o, p, day, tag, deadline, url)
		if err != nil {
			for _, b := range bots {
				b.abandon()
			}
			return 0, err
		}
		bots = append(bots, b)
	}
	for _, b := range bots {
		go b.run()
	}
	stop := func() {
		for _, b := range bots {
			b.abandon()
		}
		for _, b := range bots {
			<-b.done
		}
	}
	for _, b := range bots {
		wait := deadline + 70 - g.clk.Wall()
		if wait < 1 {
			wait = 1
		}
		select {
		case <-b.done:
		case <-g.clk.After(wait):
			b.abandon() // still working: it writes nothing more
		case <-g.ctx.Done():
			stop()
			return 0, errStopped
		}
	}
	if g.ctx.Err() != nil {
		stop()
		return 0, errStopped
	}
	worked := g.clk.Wall() - started
	for _, sql := range []string{
		"UPDATE quests SET state = 'expired' WHERE day = ? AND " +
			"state = 'open'",
		"UPDATE quests SET state = 'unfinished' WHERE day = ? AND " +
			"state = 'working'"} {
		if _, err := o.q(sql, int64(day)); err != nil {
			return 0, err
		}
	}
	rows, err := o.q("SELECT coalesce(sum(ok), 0) AS n FROM daylog "+
		"WHERE day = ?", int64(day))
	if err != nil {
		return 0, err
	}
	if _, err := o.q("INSERT INTO days VALUES (?,?,?,?,?,NULL,NULL)",
		int64(day), tag, started, worked,
		rows[0].Get("n")); err != nil {
		return 0, err
	}
	if err := o.scoreDay(day); err != nil {
		return 0, err
	}
	return started, o.setMeta("day", int64(day))
}
