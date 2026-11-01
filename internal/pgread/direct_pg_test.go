//go:build pg

// Against a real Postgres 16, seeded with testdata/schema.sql (the
// ci.yml pg job): PGREAD_TEST_URL names it. With PGREAD_STACK_DIR set
// to a running stack's folder too, Session asks the real psql there.
package pgread

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
)

func pgURL(t *testing.T) string {
	u := os.Getenv("PGREAD_TEST_URL")
	if u == "" {
		t.Skip("PGREAD_TEST_URL is not set")
	}
	return u
}

// Front Desk's two tally questions answer, with the seeded rows.
func TestDirectTally(t *testing.T) {
	g := loadPsql(t).Session[0]
	d := NewDirect(pgURL(t), nil)
	defer d.Stop()
	for i, sql := range g.SQL {
		got, err := d.Query(context.Background(), sql)
		if err != nil {
			t.Fatalf("question %d: %v", i, err)
		}
		var v struct {
			Day  string
			Rows []map[string]any
		}
		if err := json.Unmarshal(got, &v); err != nil || v.Day == "" ||
			len(v.Rows) == 0 {
			t.Fatalf("question %d: %s (%v)", i, got, err)
		}
	}
}

// writes are ways to change the database's data, or to leave
// read-only mode. Each must fail, and change nothing. (txid_current()
// is answered: it hands out a transaction number, which writes no
// data, as any read-only transaction may.) This is not every way a
// superuser's question can act: see TestDirectKnownExposures.
var writes = []string{
	"insert into pgread_canary values (2)",
	"update pgread_canary set n = 3",
	"delete from pgread_canary",
	"truncate pgread_canary",
	"create table pgread_x (n int)",
	"create temp table pgread_t (n int)",
	"drop table pgread_canary",
	"alter table pgread_canary add column m int",
	"create role pgread_evil",
	"select 1; insert into pgread_canary values (4)",
	"select json_build_object('a', 1); commit; " +
		"insert into pgread_canary values (4)",
	"commit",
	"rollback",
	"copy (select 1) to program 'touch /tmp/pgread_pwned'",
	"copy (select 1) to '/tmp/pgread_pwned2'",
	"copy pgread_canary from program 'echo 5'",
	"select set_config('transaction_read_only', 'off', true)",
	"set session characteristics as transaction read write",
	"set default_transaction_read_only = off",
	"set transaction read write",
	"with x as (insert into pgread_canary values (6) returning n) " +
		"select json_build_object('n', (select n from x))",
	"insert into pgread_canary values (7) " +
		"returning json_build_object('n', n)",
	"select json_build_object('n', n) from pgread_canary for update",
	"select lo_import('/etc/hostname')",
	"select lo_create(0)",
	"do $$ begin insert into pgread_canary values (8); end $$",
	"vacuum pgread_canary",
	"prepare pgread_p as insert into pgread_canary values (9)",
	"notify pgread_c",
	"select pg_notify('pgread_c', 'x')",
	// Leave the session read-write, then write in the next question.
	"select set_config('default_transaction_read_only', 'off', false)",
	"insert into pgread_canary values (10)",
}

// unchanged reads what any write would have changed.
const unchanged = `select json_build_object(
  'rows', (select json_agg(n) from pgread_canary),
  'tables', (select count(*) from pg_class
    where relname like 'pgread_x%' or relname like 'pgread_t%'),
  'roles', (select count(*) from pg_roles
    where rolname = 'pgread_evil'),
  'los', (select count(*) from pg_largeobject_metadata),
  'file', pg_stat_file('/tmp/pgread_pwned', true),
  'file2', pg_stat_file('/tmp/pgread_pwned2', true),
  'ro', current_setting('transaction_read_only'))`

func TestDirectRefusesWrites(t *testing.T) {
	ctx := context.Background()
	fc := clock.NewFake(1000, 50)
	d := NewDirect(pgURL(t), fc)
	defer d.Stop()
	for _, sql := range writes {
		got, err := d.Query(ctx, sql)
		if err == nil {
			t.Errorf("%q was answered: %s", sql, got)
		} else {
			t.Logf("%q: %v", sql, err)
		}
		fc.Advance(31) // past the back-off
		// Reads still work, and nothing changed.
		got, err = d.Query(ctx, unchanged)
		if err != nil {
			t.Fatalf("after %q, a read failed: %v", sql, err)
		}
		want := `{"rows" : [1], "tables" : 0, "roles" : 0, ` +
			`"los" : 0, "file" : null, "file2" : null, "ro" : "on"}`
		if string(got) != want {
			t.Fatalf("after %q:\n got %s\nwant %s", sql, got, want)
		}
	}
}

func TestSessionStack(t *testing.T) {
	dir := os.Getenv("PGREAD_STACK_DIR")
	if dir == "" {
		t.Skip("PGREAD_STACK_DIR is not set")
	}
	g := loadPsql(t).Session[0]
	s := NewSession(SessionConfig{Dir: dir})
	defer s.Stop()
	for i, sql := range g.SQL {
		got, err := s.Query(context.Background(), sql)
		if err != nil || !strings.HasPrefix(string(got), "{") {
			t.Fatalf("question %d: %s %v", i, got, err)
		}
	}
	if _, err := s.Query(context.Background(),
		"insert into \"LiteLLM_VerificationToken\" (token) "+
			"values ('pgread-x');"); err == nil ||
		!strings.Contains(err.Error(), "read-only") {
		t.Fatalf("a write through psql: %v", err)
	}
	rows, err := Once(context.Background(), dir, "SELECT count(*) AS n "+
		"FROM \"LiteLLM_VerificationToken\" WHERE token = 'pgread-x'")
	if err != nil || len(rows) != 1 || rows[0]["n"] != json.Number("0") {
		t.Fatalf("Once: %v %v", rows, err)
	}
	if _, err := Once(context.Background(), dir, "INSERT INTO "+
		"\"LiteLLM_VerificationToken\" (token) VALUES ('pgread-y') "+
		"RETURNING 1"); err == nil ||
		!strings.Contains(err.Error(),
			"cannot read the proxy database") {
		t.Fatalf("a write through Once: %v", err)
	}
}

// admin is a plain connection to the test database, to set up and
// clean up around Direct.
func admin(t *testing.T) *pgconn.PgConn {
	t.Helper()
	c, err := pgconn.Connect(context.Background(), pgURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}

func one(t *testing.T, c *pgconn.PgConn, sql string) string {
	t.Helper()
	res := c.ExecParams(context.Background(), sql, nil, nil, nil,
		nil).Read()
	if res.Err != nil {
		t.Fatalf("%s: %v", sql, res.Err)
	}
	if len(res.Rows) == 0 || res.Rows[0][0] == nil {
		return ""
	}
	return string(res.Rows[0][0])
}

// Nothing a question sets for the session reaches the next one: the
// settings go back to the connection's own, advisory locks are let go.
func TestDirectResetsSession(t *testing.T) {
	ctx := context.Background()
	d := NewDirect(pgURL(t), nil)
	defer d.Stop()
	ask := func(sql string) string {
		t.Helper()
		got, err := d.Query(ctx, sql)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return string(got)
	}
	ask("select json_build_object('st', " +
		"set_config('statement_timeout', '0', false), 'ro', " +
		"set_config('default_transaction_read_only', 'off', false))")
	if got := ask("select json_build_object('st', " +
		"current_setting('statement_timeout'), 'ro', " +
		"current_setting('default_transaction_read_only'))"); got !=
		`{"st" : "5s", "ro" : "on"}` {
		t.Errorf("settings kept from the last question: %s", got)
	}
	// Prisma's migrate lock, as a hostile question would take it.
	ask("select json_build_object('l', pg_try_advisory_lock(72707369))")
	a := admin(t)
	if got := one(t, a, "select pg_try_advisory_lock(72707369)"); got !=
		"t" {
		t.Errorf("the advisory lock is still held: %s", got)
	}
	one(t, a, "select pg_advisory_unlock(72707369)")
}

// knownExposures are what read-only mode does not stop for a
// superuser, such as the stack's litellm role: functions that act
// inside a read-only transaction. They are here so nobody reads
// TestDirectRefusesWrites as a full guarantee. The guard is that the
// callers' SQL is fixed text (package doc); a role with only
// pg_read_all_data would close these (spec question 23.9).
func TestDirectKnownExposures(t *testing.T) {
	ctx := context.Background()
	d := NewDirect(pgURL(t), nil)
	defer d.Stop()
	a := admin(t)
	ask := func(sql string) string {
		t.Helper()
		got, err := d.Query(ctx, sql)
		if err != nil {
			t.Fatalf("expected to be answered (a known exposure): "+
				"%s: %v", sql, err)
		}
		return string(got)
	}

	// Ends another connection.
	victim := admin(t)
	pid := one(t, victim, "select pg_backend_pid()")
	if got := ask("select json_build_object('k', " +
		"pg_terminate_backend(" + pid + "))"); got != `{"k" : true}` {
		t.Errorf("pg_terminate_backend: %s", got)
	}

	// A replication origin: a catalog row.
	ask("select json_build_object('o', " +
		"pg_replication_origin_create('pgread_origin'))")
	if one(t, a, "select count(*) from pg_replication_origin "+
		"where roname = 'pgread_origin'") != "1" {
		t.Error("no replication origin was made")
	}
	one(t, a, "select pg_replication_origin_drop('pgread_origin')")

	// A replication slot, which would hold WAL forever.
	ask("select json_build_object('s', (select slot_name from " +
		"pg_create_physical_replication_slot('pgread_slot')))")
	if one(t, a, "select count(*) from pg_replication_slots "+
		"where slot_name = 'pgread_slot'") != "1" {
		t.Error("no replication slot was made")
	}
	one(t, a, "select pg_drop_replication_slot('pgread_slot')")

	// A file written on the database's machine.
	oid := one(t, a, "select lo_from_bytea(0, 'pgread')")
	defer one(t, a, "select lo_unlink("+oid+")")
	ask("select json_build_object('n', lo_export(" + oid +
		", '/tmp/pgread_lo_export'))")
	if one(t, a, "select (pg_stat_file('/tmp/pgread_lo_export', "+
		"true)).size") != "6" {
		t.Error("lo_export wrote no file")
	}
}
