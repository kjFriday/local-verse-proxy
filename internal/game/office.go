package game

import (
	"fmt"
	"os"

	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
	"github.com/kjFriday/local-verse-proxy/internal/rules"
)

// office is office.py's Office: the game's state in office.db.
type office struct {
	g     *game
	db    *gamestate.DB
	rules *rules.Rules
	id    any // meta office: text, or nil (Python None)
}

// openOffice is Office(rules): the office must be set up.
func (g *game) openOffice() (*office, error) {
	if _, err := os.Stat(g.db); err != nil {
		return nil, die("no office yet: run `" + g.prog +
			" game setup`")
	}
	db, err := gamestate.Open(g.db)
	if err != nil {
		return nil, dbError(err)
	}
	o := &office{g: g, db: db, rules: g.rules}
	id, err := o.meta("office")
	if err != nil {
		db.Close()
		return nil, err
	}
	o.id = id
	return o, nil
}

// dbError is an error from office.db, where Python showed sqlite3's
// traceback.
func dbError(err error) error {
	return die(fmt.Sprintf("office.db: %v", err))
}

func (o *office) close() { o.db.Close() }

// q is Office.q: one statement under the lock.
func (o *office) q(sql string, args ...any) ([]gamestate.Row, error) {
	if o.g.ctx.Err() != nil {
		return nil, errStopped // Ctrl+C: nothing more is written
	}
	rows, err := o.db.Q(sql, args...)
	if err != nil {
		return nil, dbError(err)
	}
	return rows, nil
}

// meta is Office.meta(k): the value, or nil.
func (o *office) meta(k string) (any, error) {
	rows, err := o.q("SELECT v FROM meta WHERE k = ?", k)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0].Get("v"), nil
}

// setMeta is Office.meta(k, v): stored as str(v).
func (o *office) setMeta(k string, v any) error {
	_, err := o.q("INSERT OR REPLACE INTO meta VALUES (?, ?)", k, str(v))
	return err
}

// idText is str(office.id), as the f-strings print it.
func (o *office) idText() string { return str(o.id) }

// players is Office.players(): ordered by seat.
func (o *office) players() ([]gamestate.Row, error) {
	return o.q("SELECT * FROM players ORDER BY seat")
}
