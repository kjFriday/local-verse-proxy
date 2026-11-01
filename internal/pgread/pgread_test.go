package pgread

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
)

// fakeDir holds the fake docker, built once for the package's tests.
var fakeDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakedocker")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exe := "docker"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, exe),
		"./fakedocker")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building fakedocker:", err)
		os.Exit(1)
	}
	fakeDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fake is one test's fake docker: its script, log and working folder.
type fake struct {
	t      *testing.T
	script string
	log    string
	work   string
}

// newFake puts the fake docker first on PATH, answering with steps.
func newFake(t *testing.T, steps ...string) *fake {
	dir := t.TempDir()
	f := &fake{t: t, script: filepath.Join(dir, "script"),
		log: filepath.Join(dir, "log"), work: filepath.Join(dir,
			"work")}
	os.Mkdir(f.work, 0o755)
	f.steps(steps...)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+
		os.Getenv("PATH"))
	t.Setenv("FAKEDOCKER_SCRIPT", f.script)
	t.Setenv("FAKEDOCKER_LOG", f.log)
	return f
}

func (f *fake) steps(steps ...string) {
	os.WriteFile(f.script, []byte(strings.Join(steps, "\n")+"\n"), 0o644)
	os.Remove(f.script + ".n")
}

// run is the n-th run's arguments, folder and every byte given it.
func (f *fake) run(n int) (argv []string, dir string, env []string,
	stdin string) {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.log, fmt.Sprint(n)+".json"))
	if err != nil {
		f.t.Fatalf("run %d: %v", n, err)
	}
	var meta struct {
		Argv []string
		Dir  string
		Env  []string
	}
	json.Unmarshal(b, &meta)
	in, _ := os.ReadFile(filepath.Join(f.log, fmt.Sprint(n)+".stdin"))
	return meta.Argv, meta.Dir, meta.Env, string(in)
}

func (f *fake) runs() int {
	n := 0
	for {
		if _, err := os.Stat(filepath.Join(f.log, fmt.Sprint(n)+
			".json")); err != nil {
			return n
		}
		n++
	}
}

type psqlGolden struct {
	Session []struct {
		Marks   []string
		SQL     []string
		Answers []json.RawMessage
		Argv    []string
		Stdin   string
	}
	Once []struct {
		SQL   string
		Rows  json.RawMessage
		Argv  []string
		Stdin string
	}
}

func loadPsql(t *testing.T) psqlGolden {
	b, err := os.ReadFile("testdata/golden/psql.json")
	if err != nil {
		t.Fatal(err)
	}
	var g psqlGolden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func sameJSON(t *testing.T, what string, got, want []byte) {
	t.Helper()
	var a, b any
	json.Unmarshal(got, &a)
	json.Unmarshal(want, &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("%s: got %s, Python %s", what, got, want)
	}
}

// Same stdin bytes, arguments and folder as frontdesk.py's Psql.
func TestSessionGolden(t *testing.T) {
	g := loadPsql(t).Session[0]
	f := newFake(t, `ok {"day": "2026-10-31",\n"rows": []}`)
	s := NewSession(SessionConfig{Dir: f.work})
	marks := g.Marks
	s.mark = func() string {
		m := marks[0]
		marks = marks[1:]
		return m
	}
	for i, sql := range g.SQL {
		got, err := s.Query(context.Background(), sql)
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, fmt.Sprint("answer ", i), got, g.Answers[i])
	}
	s.Stop()
	argv, dir, _, stdin := f.run(0)
	if !reflect.DeepEqual(argv, g.Argv) {
		t.Errorf("argv %q\nPython %q", argv, g.Argv)
	}
	if !samePath(dir, f.work) {
		t.Errorf("ran in %s, not %s", dir, f.work)
	}
	if stdin != g.Stdin {
		t.Errorf("stdin:\n%q\nPython:\n%q", stdin, g.Stdin)
	}
	if f.runs() != 1 {
		t.Errorf("%d psql runs for two questions; want 1", f.runs())
	}
}

func samePath(a, b string) bool {
	ea, _ := filepath.EvalSymlinks(a)
	eb, _ := filepath.EvalSymlinks(b)
	return strings.EqualFold(filepath.Clean(ea), filepath.Clean(eb))
}

// Same stdin bytes, arguments and rows as office.py's pg().
func TestOnceGolden(t *testing.T) {
	g := loadPsql(t).Once[0]
	f := newFake(t, `ok [{"n": 3, "spend": 0.25}]`)
	rows, err := Once(context.Background(), f.work, g.SQL)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := rows[0]["n"].(json.Number); !ok || n != "3" {
		t.Errorf("rows keep json.Number: %#v", rows[0]["n"])
	}
	b, _ := json.Marshal(rows)
	sameJSON(t, "rows", b, g.Rows)
	argv, dir, _, stdin := f.run(0)
	if !reflect.DeepEqual(argv, g.Argv) {
		t.Errorf("argv %q\nPython %q", argv, g.Argv)
	}
	if !samePath(dir, f.work) {
		t.Errorf("ran in %s, not %s", dir, f.work)
	}
	if stdin != g.Stdin {
		t.Errorf("stdin:\n%q\nPython:\n%q", stdin, g.Stdin)
	}
}

func TestSessionFailures(t *testing.T) {
	ctx := context.Background()
	fc := clock.NewFake(1000, 50)
	f := newFake(t,
		"error ERROR:  relation \"x\" does not exist",
		`ok {"a": 1}`,
		"fatal FATAL:  terminating connection due to administrator "+
			"command",
		`ok {"a": 2}`,
		"noanswer",
		`ok {"a": 3}`,
		`die psql: error: connection to server failed\nsecond line`+
			`\nlast words`,
		`ok {"a": 4}`,
		"garbage",
		`ok {"a": 5}`)
	s := NewSession(SessionConfig{Dir: f.work, Clock: fc})
	ask := func(want string) {
		t.Helper()
		got, err := s.Query(ctx, "select 1")
		text := string(got)
		if err != nil {
			text = "error: " + err.Error()
		}
		if text != want {
			t.Errorf("got %q\nwant %q", text, want)
		}
	}
	ask(`error: ERROR:  relation "x" does not exist`)
	ask("error: waiting before trying psql again")
	fc.Advance(29.9)
	ask("error: waiting before trying psql again")
	fc.Advance(0.2)
	ask(`{"a": 1}`)
	ask("error: FATAL:  terminating connection due to administrator " +
		"command")
	fc.Advance(30)
	ask(`{"a": 2}`)
	ask("error: psql gave no answer")
	fc.Advance(30)
	ask(`{"a": 3}`)
	ask("error: second line last words")
	fc.Advance(30)
	ask(`{"a": 4}`)
	// Not JSON: an error, but psql stays and nothing waits (Python's
	// json.loads raising outside the TallyError path).
	f.steps("ok {not json")
	if _, err := s.Query(ctx, "select 1"); err == nil {
		t.Error("bad JSON read as an answer")
	} else if _, ok := err.(*Error); ok {
		t.Errorf("bad JSON is a psql failure: %v", err)
	}
	f.steps(`ok {"a": 5}`)
	ask(`{"a": 5}`)
	s.Stop()
	// Restarts: after each failure psql started again.
	if n := f.runs(); n != 5 {
		t.Errorf("%d psql runs; want 5", n)
	}
}

func TestSessionStall(t *testing.T) {
	fc := clock.NewFake(1000, 50)
	f := newFake(t, "stall")
	s := NewSession(SessionConfig{Dir: f.work, Clock: fc})
	done := make(chan error)
	go func() {
		_, err := s.Query(context.Background(), "select 1")
		done <- err
	}()
	for fc.Waiting() == 0 {
		time.Sleep(time.Millisecond)
	}
	fc.Advance(8)
	select {
	case err := <-done:
		if err == nil || err.Error() != "psql did not answer in time" {
			t.Fatalf("got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no answer after the timeout")
	}
	// It killed the stalled psql and waits 30 s.
	if _, err := s.Query(context.Background(), "x"); err == nil ||
		err.Error() != "waiting before trying psql again" {
		t.Fatalf("got %v", err)
	}
}

func TestSessionStartFailures(t *testing.T) {
	f := newFake(t, "startfail Cannot connect to the Docker daemon at "+
		"unix:///var/run/docker.sock. Is the docker daemon running?")
	s := NewSession(SessionConfig{Dir: f.work})
	_, err := s.Query(context.Background(), "select 1")
	if err == nil || err.Error() != "Cannot connect to the Docker "+
		"daemon at unix:///var/run/docker.sock. Is the docker daemon "+
		"running?" {
		t.Errorf("docker not running: %v", err)
	}
	s = NewSession(SessionConfig{Dir: f.work,
		Argv: []string{filepath.Join(t.TempDir(), "no-docker")}})
	_, err = s.Query(context.Background(), "select 1")
	want := "cannot run docker: No such file or directory"
	if runtime.GOOS == "windows" {
		want = "cannot run docker: The system cannot find the file " +
			"specified"
	}
	if err == nil || err.Error() != want {
		t.Errorf("no docker: %v, want %q", err, want)
	}
}

func TestSessionEnv(t *testing.T) {
	f := newFake(t, `ok {"a": 1}`)
	env := append(os.Environ(), "PGREAD_TEST_MARK=1")
	s := NewSession(SessionConfig{Dir: f.work, Env: env, App: "o'k"})
	if _, err := s.Query(context.Background(), "select 1"); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	_, _, names, stdin := f.run(0)
	found := false
	for _, n := range names {
		found = found || n == "PGREAD_TEST_MARK"
	}
	if !found {
		t.Error("psql did not get the environment given")
	}
	if !strings.Contains(stdin, "set application_name = 'o''k';\n") {
		t.Errorf("application name not quoted: %q", stdin)
	}
}

func TestOnceFailures(t *testing.T) {
	ctx := context.Background()
	f := newFake(t, `error ERROR:  relation "x" does not exist`)
	_, err := Once(ctx, f.work, "select 1")
	if err == nil || err.Error() != "cannot read the proxy database: "+
		`psql:<stdin>:2: ERROR:  relation "x" does not exist` {
		t.Errorf("error: %v", err)
	}
	f.steps("noanswer")
	rows, err := Once(ctx, f.work, "select 1")
	if err != nil || rows == nil || len(rows) != 0 {
		t.Errorf("no output reads as []: %v %v", rows, err)
	}
	f.steps("die docker: 'compose' is not a docker command.")
	_, err = Once(ctx, f.work, "select 1")
	if err == nil || err.Error() != "cannot read the proxy database: "+
		"docker: 'compose' is not a docker command." {
		t.Errorf("die: %v", err)
	}
	f.steps("ok not json")
	if _, err = Once(ctx, f.work, "select 1"); err == nil {
		t.Error("bad JSON read as rows")
	}
	f.steps("stall")
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Once(cctx, f.work, "select 1")
	if err != context.DeadlineExceeded ||
		time.Since(start) > 5*time.Second {
		t.Errorf("cancel: %v after %v", err, time.Since(start))
	}

	old := onceArgv
	defer func() { onceArgv = old }()
	onceArgv = append([]string{filepath.Join(t.TempDir(), "no-docker")},
		OnceArgv[1:]...)
	_, err = Once(ctx, f.work, "select 1")
	want := "cannot read the proxy database: cannot run docker: No " +
		"such file or directory"
	if runtime.GOOS == "windows" {
		want = "cannot read the proxy database: cannot run docker: " +
			"The system cannot find the file specified"
	}
	if err == nil || err.Error() != want {
		t.Errorf("no docker: %v, want %q", err, want)
	}
}

func TestDirectNoDatabase(t *testing.T) {
	ctx := context.Background()
	fc := clock.NewFake(1000, 50)
	d := NewDirect("", fc)
	if _, err := d.Query(ctx, "select 1"); err == nil ||
		err.Error() != "DATABASE_URL is not set in seats.env" {
		t.Fatalf("got %v", err)
	}
	// Refused at once: 3 s, then 30 s while it goes on.
	d = NewDirect("postgresql://u:p@127.0.0.1:1/db", fc)
	_, err := d.Query(ctx, "select 1")
	if err == nil || strings.Contains(err.Error(), ":p@") {
		t.Fatalf("first: %v", err)
	}
	if _, err := d.Query(ctx, "x"); err == nil ||
		err.Error() != "waiting before trying the database again" {
		t.Fatalf("got %v", err)
	}
	fc.Advance(3)
	if _, err := d.Query(ctx, "x"); err == nil || strings.HasPrefix(
		err.Error(), "waiting") {
		t.Fatalf("after 3 s: %v", err)
	}
	fc.Advance(29)
	if _, err := d.Query(ctx, "x"); err == nil ||
		err.Error() != "waiting before trying the database again" {
		t.Fatalf("29 s into the 30: %v", err)
	}
	d.Stop()
}

func TestDirectTimeout(t *testing.T) {
	// Something that takes the connection and never speaks.
	ln, err := listenSilent(t)
	if err != nil {
		t.Skip(err)
	}
	fc := clock.NewFake(1000, 50)
	d := NewDirect("postgresql://u:p@"+ln+"/db?sslmode=disable", fc)
	done := make(chan error)
	go func() {
		_, err := d.Query(context.Background(), "select 1")
		done <- err
	}()
	for fc.Waiting() == 0 {
		time.Sleep(time.Millisecond)
	}
	fc.Advance(8)
	select {
	case err := <-done:
		if err == nil || err.Error() !=
			"the database did not answer in time" {
			t.Fatalf("got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no answer after the timeout")
	}
}

func TestDirectErrorWords(t *testing.T) {
	if got := firstLine(fmt.Errorf("  \n first\nsecond")); got != "first" {
		t.Errorf("got %q", got)
	}
	if got := firstLine(&emptyErr{}); got != "*pgread.emptyErr" {
		t.Errorf("got %q", got)
	}
	var buf bytes.Buffer
	buf.WriteString("x")
	_ = buf
}

type emptyErr struct{}

func (*emptyErr) Error() string { return " " }

// A question whose context ends stops psql, but is not psql's
// failure: the next question starts a new psql at once.
func TestSessionCancelNoBackoff(t *testing.T) {
	fc := clock.NewFake(1000, 50)
	f := newFake(t, "stall", "ok {\"a\" : 1}")
	s := NewSession(SessionConfig{Dir: f.work, Clock: fc})
	defer s.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		_, err := s.Query(ctx, "select 1")
		done <- err
	}()
	// Wait until the fake psql has read the question (and so taken
	// its "stall" step), then hang up.
	for {
		if b, _ := os.ReadFile(f.script + ".n"); string(b) == "1" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("cancelled: %v", err)
	}
	got, err := s.Query(context.Background(), "select 1")
	if err != nil || string(got) != `{"a" : 1}` {
		t.Fatalf("after a cancel: %s %v", got, err)
	}
	if n := f.runs(); n != 2 {
		t.Errorf("%d psql runs; want 2 (the stalled one was stopped)",
			n)
	}
}

func TestDirectCancelNoBackoff(t *testing.T) {
	ln, err := listenSilent(t)
	if err != nil {
		t.Skip(err)
	}
	fc := clock.NewFake(1000, 50)
	d := NewDirect("postgresql://u:p@"+ln+"/db?sslmode=disable", fc)
	defer d.Stop()
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(),
			50*time.Millisecond)
		_, err := d.Query(ctx, "select 1")
		cancel()
		if err != context.DeadlineExceeded {
			t.Fatalf("question %d: %v (no back-off after a "+
				"hang-up)", i, err)
		}
	}
}

// Text with a backslash never reaches psql: \g and \! are psql's
// own commands and need no ";".
func TestBackslashRefused(t *testing.T) {
	f := newFake(t, "ok {\"a\" : 1}")
	s := NewSession(SessionConfig{Dir: f.work})
	defer s.Stop()
	for _, sql := range []string{
		"select 1 \\g",
		"select 1\n\\! id",
		"select '\\'",
	} {
		_, err := s.Query(context.Background(), sql)
		if err == nil || err.Error() != "refused: a question for "+
			"psql must hold no \\" {
			t.Errorf("Session %q: %v", sql, err)
		}
		if _, err := Once(context.Background(), f.work, sql); err ==
			nil || !strings.Contains(err.Error(), "hold no \\") {
			t.Errorf("Once %q: %v", sql, err)
		}
	}
	if f.runs() != 0 {
		t.Errorf("psql ran %d times", f.runs())
	}
}

// Text with a ";" before its end never reaches psql or Postgres.
func TestSeveralStatementsRefused(t *testing.T) {
	f := newFake(t, "ok {\"a\" : 1}")
	s := NewSession(SessionConfig{Dir: f.work})
	defer s.Stop()
	d := NewDirect("postgresql://u:p@127.0.0.1:1/db", nil)
	for _, sql := range []string{
		"select 1; commit; set default_transaction_read_only = off; " +
			"insert into t values (1); select 2",
		"select 1;;",
		"select ';'",
	} {
		for name, q := range map[string]Querier{"Session": s,
			"Direct": d} {
			_, err := q.Query(context.Background(), sql)
			if err == nil || err.Error() != "refused: a "+
				"question must be one statement, with a ; "+
				"only at its end" {
				t.Errorf("%s %q: %v", name, sql, err)
			}
		}
		if _, err := Once(context.Background(), f.work, sql); err ==
			nil || !strings.Contains(err.Error(), "with no ;") {
			t.Errorf("Once %q: %v", sql, err)
		}
	}
	if f.runs() != 0 {
		t.Errorf("psql ran %d times", f.runs())
	}
	if _, err := Once(context.Background(), f.work,
		"select 1 as n) q; commit; select 1 from (select 1"); err ==
		nil || !strings.Contains(err.Error(), "with no ;") {
		t.Errorf("Once's wrapper broken out of: %v", err)
	}
	// One ; at the end is a statement's own end, as Front Desk's are.
	if _, err := s.Query(context.Background(), "select 1;\n"); err != nil {
		t.Errorf("a ; at the end: %v", err)
	}
}

// A DATABASE_URL pgconn cannot read gives fixed words: pgconn's own
// text can show part of the password.
func TestDirectBadURLHidesPassword(t *testing.T) {
	const pw = "Zq9wXv"
	for _, url := range []string{
		"postgresql://litellm:" + pw + "@B@db:54x2/litellm",
		"postgresql://litellm:" + pw +
			"@B@db:5432/litellm?sslmode=bogus",
		"postgresql://litellm:" + pw + "/B@db:5432/lit%zz",
		"host=db password = " + pw + " port=x",
		"postgresql://litellm:" + pw + "@db:5432/litellm?sslmode=bogus",
	} {
		d := NewDirect(url, clock.NewFake(1000, 50))
		_, err := d.Query(context.Background(), "select 1")
		if err == nil || strings.Contains(err.Error(), "Zq9") ||
			strings.Contains(err.Error(), "wXv") ||
			err.Error() != "DATABASE_URL in seats.env is not a "+
				"valid address; check its password" {
			t.Errorf("%s: %v", strings.ReplaceAll(url, pw, "<pw>"),
				err)
		}
	}
}

// A refused connection keeps its reason, not only pgconn's first line.
func TestDirectConnectionWords(t *testing.T) {
	d := NewDirect("postgresql://litellm:p@127.0.0.1:1/litellm",
		clock.NewFake(1000, 50))
	_, err := d.Query(context.Background(), "select 1")
	if err == nil || !strings.HasPrefix(err.Error(), "failed to connect "+
		"to `user=litellm database=litellm`: ") ||
		!strings.Contains(err.Error(), "127.0.0.1:1") ||
		!strings.Contains(err.Error(), "refused") &&
			!strings.Contains(err.Error(), "actively") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("more than one line: %q", err)
	}
}
