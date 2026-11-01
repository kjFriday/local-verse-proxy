package game

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/fsx"
	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// cmdSetup is office.py's cmd_setup(): mint the game seats, make
// office.db.
func (g *game) cmdSetup() error {
	r := g.rules
	if _, err := os.Stat(g.db); err == nil {
		return die("the office is already set up. For a new week, " +
			"run\n`" + g.prog + " game reset` first.")
	}
	prefix := r.Office.SeatPrefix
	usd := float64(r.Mana.Daily.F * r.Mana.USD.F)
	g.say("Minting %d game seats: %s mana ($%s) each, refilled every %s.",
		len(r.Roster.Seats), r.Mana.Daily.Text, money(usd),
		r.Day.Length)
	for _, s := range r.Roster.Seats {
		why, err := g.mintFn(prefix + s.Name)
		if err != nil {
			return err
		}
		if why != "" {
			return die("mint " + prefix + s.Name +
				" failed: " + why +
				"\nClean up with `" + g.prog + " game reset`.")
		}
		g.say("  %s %s guild %s", pyfmt.Format(prefix+s.Name, "<14"),
			pyfmt.Format(s.Persona, "<9"), s.Guild)
	}
	db, err := gamestate.Create(g.db)
	if err != nil {
		return dbError(err)
	}
	defer db.Close()
	id := g.tokenHex(3)
	created := time.Unix(int64(g.clk.Wall()), 0).UTC().
		Format("2006-01-02 15:04 UTC")
	stmts := [][]any{
		{"INSERT INTO meta VALUES (?, ?)", "office", id},
		{"INSERT INTO meta VALUES (?, ?)", "day", "0"},
		{"INSERT INTO meta VALUES (?, ?)", "created", created},
	}
	seen := map[string]bool{}
	var guilds []string
	for _, s := range r.Roster.Seats {
		if !seen[s.Guild] {
			seen[s.Guild] = true
			guilds = append(guilds, s.Guild)
		}
	}
	sort.Strings(guilds)
	for _, gd := range guilds {
		stmts = append(stmts, []any{"INSERT INTO guilds VALUES (?)",
			gd})
	}
	for _, s := range r.Roster.Seats {
		stmts = append(stmts, []any{"INSERT INTO players VALUES " +
			"(?,?,?,?)", prefix + s.Name, s.Name, s.Persona,
			s.Guild})
	}
	for _, st := range stmts {
		if _, err := db.Q(st[0].(string), st[1:]...); err != nil {
			return dbError(err)
		}
	}
	g.say("Office %s is open. Keys are in game/keys/ (git ignores "+
		"*.key).", id)
	g.say("Next: %s game run --days 2", g.prog)
	return nil
}

// cmdRun is office.py's cmd_run(): play days, each with its summary
// and, unless quiet, the board.
func (g *game) cmdRun(days int, quiet bool) error {
	o, err := g.openOffice()
	if err != nil {
		return err
	}
	defer o.close()
	url := g.proxyURL()
	perWeek := g.rules.Day.PerWeek
	for i := 0; i < days; i++ {
		v, err := o.meta("day")
		if err != nil {
			return err
		}
		last, ok := pyInt(str(v))
		if !ok {
			return die("office.db: meta day " +
				pyfmt.ReprString(str(v)) + " is not a number")
		}
		day := last + 1
		if float64(day) > perWeek.F {
			g.say("The week is over (%s days). Start a new one "+
				"with\n`%s game reset`, then `%s game setup`.",
				perWeek.Text, g.prog, g.prog)
			return nil
		}
		if _, err := o.playDay(day, url); err != nil {
			return err
		}
		if err := o.daySummary(day); err != nil {
			return err
		}
		if !quiet {
			if err := o.board(0); err != nil {
				return err
			}
			g.say("")
		}
	}
	return nil
}

// cmdBoard is office.py's cmd_board().
func (g *game) cmdBoard(day int, seat string) error {
	o, err := g.openOffice()
	if err != nil {
		return err
	}
	defer o.close()
	if seat != "" {
		return o.seatBoard(seat, day)
	}
	return o.board(day)
}

// cmdReset is office.py's cmd_reset(): revoke every game seat, then
// wipe the game's state.
func (g *game) cmdReset() error {
	r := g.rules
	var seats []string
	for _, s := range r.Roster.Seats {
		seats = append(seats, r.Office.SeatPrefix+s.Name)
	}
	if _, err := os.Stat(g.db); err == nil {
		db, err := gamestate.Open(g.db)
		if err != nil {
			return dbError(err)
		}
		rows, err := db.Q("SELECT seat FROM players")
		db.Close()
		if err != nil {
			return dbError(err)
		}
		set := map[string]bool{}
		for _, s := range seats {
			set[s] = true
		}
		for _, row := range rows {
			set[str(row.At(0))] = true
		}
		seats = seats[:0]
		for s := range set {
			seats = append(seats, s)
		}
		sort.Strings(seats)
	}
	done := map[string]string{}
	var order []string
	for _, s := range seats {
		why, err := g.revokeFn(s)
		if err != nil {
			return err
		}
		if _, ok := done[s]; !ok {
			order = append(order, s)
		}
		done[s] = why
	}
	var failed []string
	for _, s := range order {
		if done[s] != "revoked" && done[s] != "no key" {
			failed = append(failed, s)
		}
	}
	if len(failed) > 0 {
		return die(fmt.Sprintf("could not revoke %d of %d game seats. "+
			"%s:\n  %s\nThe game was not wiped. Is the proxy "+
			"up, with this seats.env? (docker compose ps)\n"+
			"Then run reset again.",
			len(failed), len(seats), failed[0], done[failed[0]]))
	}
	n := 0
	for _, s := range order {
		if done[s] == "revoked" {
			n++
		}
	}
	plural := "s"
	if n == 1 {
		plural = ""
	}
	tail := "."
	if n < len(seats) {
		tail = fmt.Sprintf("; %d had no key.", len(seats)-n)
	}
	g.say("Revoked %d game seat%s%s", n, plural, tail)
	files, err := gamestate.Files(g.dir)
	if err != nil {
		return die(pyfmt.OSErrorText(err, g.dir))
	}
	for _, p := range files {
		if err := fsx.Remove(p); err != nil {
			if fsx.InUse(err) {
				return die("game/office.db is in use by " +
					"another program, likely a running " +
					g.prog + " game run or Front Desk. " +
					"Stop it, then run " + g.prog +
					" game reset again.")
			}
			return die(pyfmt.OSErrorText(err, p))
		}
	}
	g.say("Game state wiped. New week: %s game setup", g.prog)
	return nil
}
