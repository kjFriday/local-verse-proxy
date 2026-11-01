package pgread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
)

// The JSON types: what psycopg reads into Python objects.
const (
	oidJSON  = 114
	oidJSONB = 3802
)

// Direct asks Postgres at url (DATABASE_URL), read only (--docker).
// It is a port of frontdesk.py's Pg: each question in its own
// goroutine, given 8 s; the connection kept between questions; a
// question that failed on a kept connection tried once more on a new
// one; after a failure, a wait of 3 s, then 30 s while failures go on.
//
// Each question is one statement through Postgres's extended protocol
// (as psycopg's prepare=True), so text with two statements is refused;
// one whose answer is not exactly one column (COPY, SET, DO, DDL) is
// refused before it runs. After each question the kept connection is
// reset (RESET ALL, the session's own user, no advisory locks), so
// nothing one question set reaches the next. Read only does not stop
// a superuser's functions (see the package doc): the SQL must be the
// caller's constant text.
//
// A question whose ctx ends is not the database's failure: it drops
// the connection, but sets no back-off.
type Direct struct {
	url   string
	clock clock.Clock

	mu      sync.Mutex
	conn    *dconn // idle between questions
	retryAt float64
	fails   int // failed tries in a row

	timeout time.Duration // per question; tests shorten it
}

type dconn struct {
	pg    *pgconn.PgConn
	stmts map[string]*pgconn.StatementDescription
}

// NewDirect makes a Direct; it connects on the first Query.
func NewDirect(url string, c clock.Clock) *Direct {
	if c == nil {
		c = clock.Real()
	}
	return &Direct{url: url, clock: c, retryAt: -inf,
		timeout: 8 * time.Second}
}

// stale is a question that failed on a connection kept from before.
type stale struct{ msg string }

func (e *stale) Error() string { return e.msg }

// Query asks one question and gives the JSON object it answered.
func (d *Direct) Query(ctx context.Context,
	sql string) (json.RawMessage, error) {
	if err := oneStatement(sql); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.clock.Mono() < d.retryAt {
		return nil, fail("waiting before trying the database again")
	}
	got, err := d.query(ctx, sql)
	var st *stale
	if errors.As(err, &st) && ctx.Err() == nil {
		// The connection kept from last time broke, as when the
		// database restarts. A new one may work now.
		d.drop()
		got, err = d.query(ctx, sql)
	}
	if errors.As(err, &st) {
		err = fail(st.msg)
	}
	if err != nil && ctx.Err() != nil {
		// The asker hung up: nothing is wrong with the database.
		d.drop()
		return nil, ctx.Err()
	}
	if err != nil {
		d.drop()
		// A database that is just restarting is back within
		// seconds: try again soon once, then every 30 s.
		d.fails++
		wait := 30.0
		if d.fails == 1 {
			wait = 3
		}
		d.retryAt = d.clock.Mono() + wait
		return nil, err
	}
	d.fails = 0
	return got, nil
}

// box is where a question's goroutine leaves its answer, unless the
// asker gave up: then the goroutine closes its connection itself.
type box struct {
	mu        sync.Mutex
	full      bool
	abandoned bool
	conn      *dconn
	got       json.RawMessage
	err       error
	ready     chan struct{}
}

func (d *Direct) query(ctx context.Context,
	sql string) (json.RawMessage, error) {
	if d.url == "" {
		return nil, fail("DATABASE_URL is not set in seats.env")
	}
	kept := d.conn != nil
	b := &box{ready: make(chan struct{})}
	go d.work(d.conn, sql, b)
	select {
	case <-b.ready:
	case <-d.clock.After(d.timeout.Seconds()):
	case <-ctx.Done():
	}
	b.mu.Lock()
	if !b.full {
		b.abandoned = true
		b.mu.Unlock()
		d.conn = nil // still busy: its goroutine closes it
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fail("the database did not answer in time")
	}
	b.mu.Unlock()
	d.conn = b.conn
	if b.err != nil {
		msg := firstLine(b.err)
		if kept {
			return nil, &stale{msg}
		}
		return nil, fail(msg)
	}
	if b.got == nil {
		return nil, fail("the database gave no answer")
	}
	return b.got, nil
}

func (d *Direct) work(c *dconn, sql string, b *box) {
	// A question has 8 s; this only makes sure a goroutine the
	// asker left behind ends some day.
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Minute)
	defer cancel()
	var got json.RawMessage
	var err error
	if c == nil {
		c, err = connect(ctx, d.url)
	}
	if err == nil {
		got, err = c.ask(ctx, sql)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.abandoned {
		if c != nil {
			c.close()
		}
		return
	}
	b.full, b.conn, b.got, b.err = true, c, got, err
	close(b.ready)
}

// errBadURL is DATABASE_URL that pgconn cannot read. Its own error
// quotes the address, and hides the password only where it can tell
// where the password is: with a stray @ / # or %, a part of it can
// show. So nothing of pgconn's text is used.
var errBadURL = fail("DATABASE_URL in seats.env is not a valid " +
	"address; check its password")

func connect(ctx context.Context, url string) (*dconn, error) {
	cfg, err := pgconn.ParseConfig(url)
	if err != nil {
		return nil, errBadURL
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["statement_timeout"] = "5s"
	cfg.RuntimeParams["application_name"] = "frontdesk"
	pg, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &dconn{pg: pg,
		stmts: map[string]*pgconn.StatementDescription{}}, nil
}

// ask runs sql in its own read-only transaction and gives its one
// value if that is a JSON object; nil if it is anything else.
func (c *dconn) ask(ctx context.Context, sql string) (json.RawMessage,
	error) {
	if _, err := c.pg.Exec(ctx, "begin read only").ReadAll(); err != nil {
		return nil, err
	}
	got, err := c.run(ctx, sql)
	if err != nil {
		c.pg.Exec(ctx, "rollback").ReadAll()
		return nil, err
	}
	if _, err := c.pg.Exec(ctx, "commit").ReadAll(); err != nil {
		return nil, err
	}
	// Whatever the question changed for the session (set_config with
	// false, a role, an advisory lock) ends with it. The settings go
	// back to those the connection started with, read only among
	// them; the prepared questions stay.
	if _, err := c.pg.Exec(ctx, resetSQL).ReadAll(); err != nil {
		return nil, err
	}
	return got, nil
}

// resetSQL is run after each question on the connection that is kept.
const resetSQL = "reset all; set session authorization default; " +
	"select pg_advisory_unlock_all()"

func (c *dconn) run(ctx context.Context, sql string) (json.RawMessage,
	error) {
	sd := c.stmts[sql]
	if sd == nil {
		name := "fd_" + strconv.Itoa(len(c.stmts))
		var err error
		sd, err = c.pg.Prepare(ctx, name, sql, nil)
		if err != nil {
			return nil, err
		}
		if len(sd.Fields) != 1 {
			c.pg.Deallocate(ctx, name)
			return nil, errors.New("refused: a question must be " +
				"one statement that gives one column")
		}
		c.stmts[sql] = sd
	}
	res := c.pg.ExecPrepared(ctx, sd.Name, nil, nil, nil).Read()
	if res.Err != nil {
		return nil, res.Err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 ||
		res.Rows[0][0] == nil || len(res.FieldDescriptions) != 1 {
		return nil, nil
	}
	oid := res.FieldDescriptions[0].DataTypeOID
	if oid != oidJSON && oid != oidJSONB {
		return nil, nil
	}
	v := bytes.TrimSpace(res.Rows[0][0])
	if len(v) == 0 || v[0] != '{' || !json.Valid(v) {
		return nil, nil
	}
	return json.RawMessage(append([]byte(nil), v...)), nil
}

func (c *dconn) close() {
	ctx, cancel := context.WithTimeout(context.Background(),
		2*time.Second)
	defer cancel()
	c.pg.Close(ctx)
}

// firstLine is Python's str(e).strip().splitlines()[0], or the type's
// name; a server's error is its primary message, as psycopg words it.
// pgconn's connection error puts its reason on the lines after the
// first ("failed to connect to `user=litellm database=litellm`:"), one
// per address tried: the first of them is kept, so the reason shows.
func firstLine(err error) string {
	var pe *pgconn.PgError
	var ce *pgconn.ConnectError
	text := err.Error()
	switch {
	case errors.As(err, &pe) && pe.Message != "":
		text = pe.Message
	case errors.As(err, &ce):
		text = ce.Error()
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Sprintf("%T", err)
	}
	lines := strings.Split(text, "\n")
	line := strings.TrimSpace(lines[0])
	if strings.HasSuffix(line, ":") {
		for _, more := range lines[1:] {
			if more = strings.TrimSpace(more); more != "" {
				return line + " " + more
			}
		}
	}
	return line
}

func (d *Direct) drop() {
	c := d.conn
	d.conn = nil
	if c != nil {
		c.close()
	}
}

// Stop closes the idle connection. A question still running keeps
// its connection until it ends.
func (d *Direct) Stop() {
	if d.mu.TryLock() {
		defer d.mu.Unlock()
		d.drop()
	}
}
