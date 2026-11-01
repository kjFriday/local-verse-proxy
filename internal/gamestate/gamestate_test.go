package gamestate

import (
	"bytes"
	"crypto/sha256"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

const refDir = "../../tests/reference/game"

var weeks = []string{"lvg-bal-week1-office.db", "lvg-bal-week2-office.db",
	"lvg-bal-week3-office.db"}

// copyRef copies a reference office.db into a temporary folder, so
// no test ever writes to the recorded file.
func copyRef(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(refDir, name))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "office.db")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func sum(t *testing.T, p string) [32]byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// The Python-made files open read only and read-write, and reading
// them changes no byte.
func TestReferenceFiles(t *testing.T) {
	for _, w := range weeks {
		p := copyRef(t, w)
		before := sum(t, p)
		seats, err := ReadSeats(p)
		if err != nil || len(seats) != 10 {
			t.Fatalf("%s: ReadSeats %v %v", w, seats, err)
		}
		db, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Q("SELECT * FROM players ORDER BY seat")
		if err != nil || len(rows) != 10 {
			t.Fatalf("%s: players %v %v", w, rows, err)
		}
		if rows[0].Str("seat") != "game-bea" ||
			rows[0].Str("PERSONA") != "balanced" {
			t.Errorf("%s: first player %v", w, rows[0])
		}
		days, err := db.Q("SELECT * FROM days ORDER BY day")
		if err != nil || len(days) != 5 {
			t.Fatalf("%s: days %d %v", w, len(days), err)
		}
		d := days[0]
		if _, ok := d.Get("started").(float64); !ok ||
			d.Int("day") != 1 || d.Float("expected") <= 0 ||
			d.Null("nope") != true {
			t.Errorf("%s: day row types: %#v", w, d)
		}
		db.Close()
		if sum(t, p) != before {
			t.Errorf("%s: reading it changed the file", w)
		}
	}
}

// Schema is office.py's SCHEMA byte for byte, and makes the same
// tables as the file Python made.
func TestSchema(t *testing.T) {
	src, err := os.ReadFile("../../game/office.py")
	if err != nil {
		t.Skip("game/office.py is gone (Gate C): nothing to compare")
	}
	m := regexp.MustCompile(`(?s)SCHEMA = """(.*?)"""`).FindSubmatch(src)
	if m == nil || string(m[1]) != Schema {
		t.Fatalf("Schema differs from office.py's SCHEMA")
	}
	p := filepath.Join(t.TempDir(), "office.db")
	db, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, _ := db.Q("SELECT type, name, sql FROM sqlite_master " +
		"ORDER BY name")
	// A file Python's sqlite3 makes from the same SCHEMA. (The
	// reference weeks are older: their daylog has no kind column.)
	// On Windows "python3" may be the Store's stand-in, which only
	// says Python is missing: try it first.
	py, err := exec.LookPath("python3")
	if err == nil {
		err = exec.Command(py, "-I", "-c", "import sqlite3").Run()
	}
	if err != nil {
		t.Skip("no working python3 here")
	}
	pyFile := filepath.Join(t.TempDir(), "py.db")
	mk := exec.Command(py, "-I", "-c", "import re, sqlite3, sys\n"+
		"s = open(sys.argv[1], encoding='utf-8').read()\n"+
		"m = re.search('SCHEMA = \"\"\"(.*?)\"\"\"', s, re.S)\n"+
		"sqlite3.connect(sys.argv[2]).executescript(m.group(1))\n",
		"../../game/office.py", pyFile)
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("python3: %v %s", err, out)
	}
	ref, err := Open(pyFile)
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Close()
	want, _ := ref.Q("SELECT type, name, sql FROM sqlite_master " +
		"ORDER BY name")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tables differ:\n%v\n%v", got, want)
	}
	if _, err := Create(p); err == nil {
		t.Fatal("Create over an existing file")
	}
}

func TestQueryAndClaim(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, id := range []string{"d1q03", "d1q01", "d1q02", "d2q01"} {
		day := int64(1)
		if id == "d2q01" {
			day = 2
		}
		if _, err := db.Q("INSERT INTO quests VALUES "+
			"(?,?,?,?,?,?,NULL,'open')", id, day, "small", 10.0,
			int64(2+i), 12.0); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	row, ok, err := db.Claim("game-fern", 1, func(k int) int {
		n = k
		return 1
	})
	if err != nil || !ok || n != 3 || row.Str("id") != "d1q02" ||
		row.Str("state") != "working" ||
		row.Str("seat") != "game-fern" {
		t.Fatalf("claim: %v %v %v n=%d", row, ok, err, n)
	}
	if v, ok := row.Get("value").(float64); !ok || v != 10 {
		t.Errorf("REAL column: %#v", row.Get("value"))
	}
	if v, ok := row.Get("calls").(int64); !ok || v != 4 {
		t.Errorf("INT column: %#v", row.Get("calls"))
	}
	for i := 0; i < 2; i++ {
		db.Claim("game-bea", 1, func(int) int { return 0 })
	}
	if _, ok, err := db.Claim("x", 1, func(int) int { return 0 }); ok ||
		err != nil {
		t.Fatalf("claim with none open: %v %v", ok, err)
	}
	rows, _ := db.Q("SELECT state, count(*) AS n FROM quests GROUP BY 1 " +
		"ORDER BY 1")
	if len(rows) != 2 || rows[1].Int("n") != 3 ||
		rows[1].At(0) != "working" {
		t.Fatalf("states: %v", rows)
	}
	if rows[0].At(5) != nil || rows[0].Float("n") != 1 {
		t.Fatal("At past the end, or Float of an int")
	}
	if _, err := db.Q("SELECT nope FROM quests"); err == nil {
		t.Fatal("a bad query gave no error")
	}
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"office.db", "office.db-journal",
		"office.dbx", "rules.toml", "my-office.db"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	got, err := Files(dir)
	want := []string{filepath.Join(dir, "office.db"),
		filepath.Join(dir, "office.db-journal"),
		filepath.Join(dir, "office.dbx")}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Files = %v %v", got, err)
	}
	if got, err := Files(filepath.Join(dir, "none")); err != nil ||
		len(got) != 0 {
		t.Fatalf("missing folder: %v %v", got, err)
	}
	if _, err := ReadSeats(filepath.Join(dir, "none.db")); err == nil {
		t.Fatal("ReadSeats of no file")
	}
}

// copyAll writes every row of src into a new file Go makes.
func copyAll(t *testing.T, src, dst string) {
	t.Helper()
	from, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	to, err := Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer to.Close()
	for _, table := range []string{"meta", "guilds", "players", "quests",
		"completions", "days", "daylog", "usage", "scores"} {
		rows, err := from.Q("SELECT * FROM " + table +
			" ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			marks := strings.TrimSuffix(strings.Repeat("?,",
				len(r.vals)), ",")
			// By name: the reference weeks' daylog is an older one.
			if _, err := to.Q("INSERT INTO "+table+" ("+
				strings.Join(r.cols,
					", ")+") VALUES ("+marks+")",
				r.vals...); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Python's `office.py board` reads a file Go made exactly as it reads
// its own: every board, byte for byte.
func TestPythonReadsGoFile(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 here")
	}
	src, err := os.ReadFile("../../game/office.py")
	if err != nil {
		t.Skip("game/office.py is gone (Gate C)")
	}
	rules, _ := os.ReadFile("../../game/rules.toml")
	if exec.Command(py, "-c", "import tomllib").Run() != nil {
		t.Skip("python3 is older than 3.11")
	}
	board := func(db string, args ...string) string {
		dir := t.TempDir()
		game := filepath.Join(dir, "game")
		os.Mkdir(game, 0o755)
		os.WriteFile(filepath.Join(game, "office.py"), src, 0o644)
		os.WriteFile(filepath.Join(game, "rules.toml"), rules, 0o644)
		b, _ := os.ReadFile(db)
		os.WriteFile(filepath.Join(game, "office.db"), b, 0o644)
		cmd := exec.Command(py, append([]string{"-I", filepath.Join(
			game, "office.py"), "board"}, args...)...)
		var out, errs bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errs
		if err := cmd.Run(); err != nil {
			t.Fatalf("office.py board %v: %v\n%s", args, err,
				errs.String())
		}
		return out.String()
	}
	for _, w := range weeks {
		ref := copyRef(t, w)
		mine := filepath.Join(t.TempDir(), "office.db")
		copyAll(t, ref, mine)
		for _, args := range [][]string{{}, {"-d", "1"}, {"-d", "5"},
			{"-s", "fern"}, {"-s", "game-spike", "-d", "3"}} {
			want, got := board(ref, args...), board(mine, args...)
			if got != want || !strings.Contains(got, "Office ") {
				t.Errorf("%s board %v: Go's file reads "+
					"differently:\n"+
					"%s\nPython's file:\n%s", w, args, got,
					want)
			}
		}
	}
}

// Row finds columns as sqlite3.Row does and prints values as Python's
// str() of what sqlite3 gives (recorded with Python 3.13's sqlite3).
func TestRowLikePython(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Q(`SELECT 1 AS Seat, 2 AS seat, 3 AS "K",
		123456.0 AS f, 1e6 AS g, 1e16 AS h, 0.00001 AS i, -0.0 AS j,
		7 AS n, 'x' AS s, NULL AS z, 4 AS "é"`)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	r := rows[0]
	// Python: r["seat"] and r["SEAT"] are 1 (the first, by ASCII
	// case); r["K"] is an IndexError (the Kelvin sign is not K); a
	// name with a non-ASCII letter matches only exactly.
	if r.Get("seat") != int64(1) || r.Get("SEAT") != int64(1) ||
		r.Get("K") != nil || r.Get("k") != nil ||
		r.Get("K") != int64(3) || r.Get("É") != nil ||
		r.Get("é") != int64(4) {
		t.Errorf("index: seat %v SEAT %v K %v kelvin %v É %v é %v",
			r.Get("seat"), r.Get("SEAT"), r.Get("K"),
			r.Get("K"), r.Get("É"), r.Get("é"))
	}
	want := map[string]string{"f": "123456.0", "g": "1000000.0",
		"h": "1e+16", "i": "1e-05", "j": "-0.0", "n": "7", "s": "x",
		"z": ""} // Python: str(None) is "None"; Str gives "" on purpose
	for col, w := range want {
		if got := r.Str(col); got != w {
			t.Errorf("Str(%s) = %q, Python %q", col, got, w)
		}
	}
}

// pyFloat is pyfmt.Repr, which gamestate may not import.
func TestPyFloatIsRepr(t *testing.T) {
	vals := []float64{0, math.Copysign(0, -1), 1, 0.1, 1e16, 1e15,
		123456789012345678, 1e-4, 1e-5, 5e-324, math.MaxFloat64,
		math.Inf(1), math.Inf(-1), math.NaN(), 2.675, 1500}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 5000 {
		vals = append(vals, math.Float64frombits(rng.Uint64()))
	}
	for _, v := range vals {
		if got, want := pyFloat(v), pyfmt.Repr(v); got != want {
			t.Fatalf("pyFloat(%x) = %q, Repr %q",
				math.Float64bits(v), got, want)
		}
	}
}
