// Package gamestate is the office game's state file, game/office.db:
// SQLite, the same schema and the same values as office.py writes, so
// Python and Go can play on one file.
//
// One connection, behind a mutex, as office.py's Office.q. The default
// journal mode (DELETE) stays, so a reader needs no write access.
package gamestate

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // the "sqlite" driver, pure Go

	"github.com/kjFriday/local-verse-proxy/internal/fsx"
)

// Schema is office.py's SCHEMA, byte for byte.
const Schema = `
CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT);
CREATE TABLE guilds (name TEXT PRIMARY KEY);
CREATE TABLE players (seat TEXT PRIMARY KEY, name TEXT UNIQUE,
  persona TEXT, guild TEXT REFERENCES guilds(name));
-- state: open, working, done, unfinished, expired
CREATE TABLE quests (id TEXT PRIMARY KEY, day INT, size TEXT,
  value REAL, calls INT, par_mana REAL, seat TEXT, state TEXT);
-- What a player says it handed in. The scorekeeper checks it.
CREATE TABLE completions (quest TEXT PRIMARY KEY, seat TEXT, day INT,
  attempts INT, approved INT, calls INT);
CREATE TABLE days (day INT PRIMARY KEY, tag TEXT, started REAL,
  worked REAL, expected INT, logged INT, flush_s REAL);
-- code and kind: the proxy's last refusal (kind: error.type).
CREATE TABLE daylog (day INT, seat TEXT, ok INT, refused INT,
  code INT, kind TEXT, out_at REAL, PRIMARY KEY (day, seat));
-- Filled from LiteLLM_SpendLogs at day end.
CREATE TABLE usage (day INT, seat TEXT, calls INT, tokens INT,
  mana REAL, PRIMARY KEY (day, seat));
CREATE TABLE scores (day INT, quest TEXT PRIMARY KEY, seat TEXT,
  calls INT, mana REAL, eff REAL, bonus REAL, points REAL,
  verdict TEXT);
`

// DB is one office.db, open.
type DB struct {
	mu sync.Mutex
	db *sql.DB
}

// NoSync, set only by tests, opens files with synchronous=OFF:
// SQLite then does not wait for the disk after each commit. What the
// file holds is the same; only a power cut could lose the last
// writes. Windows runners take seconds per wait, so the game's tests
// ran past go test's 10 minutes with it on.
var NoSync bool

// dsn is the driver's name for path: a file: URI, so the mode and
// pragmas ride along whatever the path holds.
func dsn(path, mode string, busyMS int) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{}
	q.Set("mode", mode)
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyMS))
	if NoSync {
		q.Add("_pragma", "synchronous(OFF)")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func open(path, mode string, busyMS int) (*sql.DB, error) {
	name, err := dsn(path, mode, busyMS)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Create makes a new file at path and runs Schema in it. A file
// already there is an error.
func Create(path string) (*DB, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := open(path, "rw", 5000)
	if err != nil {
		fsx.Remove(path)
		return nil, err
	}
	if _, err := db.Exec(Schema); err != nil {
		db.Close()
		fsx.Remove(path)
		return nil, err
	}
	return &DB{db: db}, nil
}

// Open opens an existing file read-write; a busy file is waited for
// up to 5 s, as Python's sqlite3 default.
func Open(path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := open(path, "rw", 5000)
	if err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

// Close closes the file.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.db.Close()
}

// Q runs one statement under the lock, like Office.q: rows of
// int64, float64, string or nil, as SQLite stores them.
func (db *DB) Q(sql string, args ...any) ([]Row, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return query(db.db, sql, args...)
}

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func query(q querier, sql string, args ...any) ([]Row, error) {
	rows, err := q.Query(sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, Row{cols: cols, vals: vals})
	}
	return out, rows.Err()
}

// Claim is Office.claim: under the lock, the open quests of day by
// id, pick(n) chooses one, it becomes "working" for seat.
func (db *DB) Claim(seat string, day int,
	pick func(n int) int) (Row, bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	ids, err := query(db.db, "SELECT id FROM quests WHERE day = ? AND "+
		"state = 'open' ORDER BY id", int64(day))
	if err != nil || len(ids) == 0 {
		return Row{}, false, err
	}
	qid := ids[pick(len(ids))].At(0)
	if _, err := db.db.Exec("UPDATE quests SET state = 'working', "+
		"seat = ? WHERE id = ?", seat, qid); err != nil {
		return Row{}, false, err
	}
	rows, err := query(db.db, "SELECT * FROM quests WHERE id = ?", qid)
	if err != nil || len(rows) == 0 {
		return Row{}, false, err
	}
	return rows[0], true, nil
}

// Row is one row: its columns' names and values.
type Row struct {
	cols []string
	vals []any
}

// index finds a column the way sqlite3.Row does: the first whose
// name equals col, ignoring the case of ASCII letters only (a name
// with any other letter must match exactly).
func (r Row) index(col string) int {
	for i, c := range r.cols {
		if c == col || asciiFold(c, col) {
			return i
		}
	}
	return -1
}

// asciiFold: a and b are ASCII and equal but for letter case.
func asciiFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 0x80 || y >= 0x80 {
			return false
		}
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// Get is the value of col, or nil if there is no such column.
func (r Row) Get(col string) any {
	if i := r.index(col); i >= 0 {
		return r.vals[i]
	}
	return nil
}

// Int is col as a whole number: a float is cut, text and NULL are 0.
func (r Row) Int(col string) int64 {
	switch v := r.Get(col).(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}

// Float is col as a float: an int64 converts.
func (r Row) Float(col string) float64 { // an int64 converts
	switch v := r.Get(col).(type) {
	case int64:
		return float64(v)
	case float64:
		return v
	}
	return 0
}

// Str is col as Python's str() prints the value sqlite3 gives: text
// as it is, an int64 in digits, a float64 as repr() (123456.0, 1e+16,
// 1e-05). NULL, or no such column, gives "" (Python: "None", or an
// IndexError); Null tells them apart.
func (r Row) Str(col string) string {
	switch v := r.Get(col).(type) {
	case string:
		return v
	case nil:
		return ""
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return pyFloat(v)
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

// pyFloat is Python's repr(float), as pyfmt.Repr (gamestate may not
// import pyfmt; the test checks the two agree): the shortest digits
// that read back, with Python's layout.
func pyFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	sign := ""
	if math.Signbit(f) {
		sign, f = "-", -f
	}
	if f == 0 {
		return sign + "0.0"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	k := strings.IndexByte(e, 'e')
	exp, _ := strconv.Atoi(e[k+1:])
	digits := strings.Replace(e[:k], ".", "", 1)
	decpt := exp + 1 // digits before the point
	if decpt > 16 || decpt < -3 {
		m := digits[:1]
		if len(digits) > 1 {
			m += "." + digits[1:]
		}
		es := "+"
		if exp < 0 {
			es, exp = "-", -exp
		}
		return fmt.Sprintf("%s%se%s%02d", sign, m, es, exp)
	}
	switch {
	case decpt <= 0:
		return sign + "0." + strings.Repeat("0", -decpt) + digits
	case decpt >= len(digits):
		return sign + digits + strings.Repeat("0", decpt-len(digits)) +
			".0"
	}
	return sign + digits[:decpt] + "." + digits[decpt:]
}

// Null says whether col is NULL (or not there).
func (r Row) Null(col string) bool { return r.Get(col) == nil }

// At is the i-th value.
func (r Row) At(i int) any {
	if i < 0 || i >= len(r.vals) {
		return nil
	}
	return r.vals[i]
}

// Columns are the row's column names.
func (r Row) Columns() []string { return append([]string(nil), r.cols...) }

// ReadSeats is Front Desk's read: SELECT seat FROM players, read
// only (mode=ro), busy 200 ms; the file closed before it returns.
func ReadSeats(path string) ([]string, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := open(path, "ro", 200)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := query(db, "SELECT seat FROM players")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		if s, ok := r.At(0).(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// Files is office.db* in dir, as Python's glob, sorted.
func Files(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, "office.db") ||
			runtime.GOOS == "windows" && strings.HasPrefix(
				strings.ToLower(n), "office.db") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out)
	return out, nil
}
