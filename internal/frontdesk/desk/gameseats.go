package desk

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
	"github.com/kjFriday/local-verse-proxy/internal/rules"
)

// GameSeats is the office game's seats, found the way `lvp game
// reset` finds them: its prefix plus each roster name in rules.toml,
// and every player in office.db. Each file is reread only when its
// time (in nanoseconds) changes: a running game writes office.db many
// times a minute.
type GameSeats struct {
	mu      sync.Mutex
	rules   string
	db      string
	rulesAt stamp
	dbAt    stamp
	prefix  string
	roster  []string
	players []string
	seats   map[string]bool
}

// stamp is a file's time: "unread" before the first read or after a
// failed one, "missing" when the file is not there.
type stamp struct {
	ns    int64
	state int // 0 unread, 1 missing, 2 there
}

// NewGameSeats reads the game in folder (rules.toml, office.db).
func NewGameSeats(folder string) *GameSeats {
	return &GameSeats{rules: filepath.Join(folder, "rules.toml"),
		db:    filepath.Join(folder, "office.db"),
		seats: map[string]bool{}}
}

func mtime(path string) stamp {
	st, err := os.Stat(path)
	if err != nil {
		return stamp{state: 1}
	}
	return stamp{ns: st.ModTime().UnixNano(), state: 2}
}

// Get rereads what changed and gives the game's seat names.
func (g *GameSeats) Get() map[string]bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if at := mtime(g.rules); at != g.rulesAt {
		g.rulesAt = at
		g.prefix, g.roster = rules.SeatNames(g.rules)
	}
	if at := mtime(g.db); at != g.dbAt {
		g.dbAt = at
		if at.state != 2 {
			g.players = nil
		} else if p, err := gamestate.ReadSeats(g.db); err != nil {
			g.dbAt = stamp{} // busy or half made: next time
		} else {
			g.players = p
		}
	}
	seats := map[string]bool{}
	for _, s := range g.roster {
		seats[s] = true
	}
	for _, s := range g.players {
		seats[s] = true
	}
	g.seats = seats
	return seats
}

// Prefix is the game's seat prefix ("" if none).
func (g *GameSeats) Prefix() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.prefix
}

// Owns is true if a new card by this name would clash with the game:
// its name starts with the prefix, or is one of the seats, ignoring
// case.
func (g *GameSeats) Owns(seat string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	low := pyfmt.Lower(seat)
	if g.prefix != "" && strings.HasPrefix(low, g.prefix) {
		return true
	}
	for s := range g.seats {
		if pyfmt.Lower(s) == low {
			return true
		}
	}
	return false
}
