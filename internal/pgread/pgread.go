// Package pgread asks the proxy's Postgres questions, read only, three
// ways: Session keeps one psql running over `docker compose exec`
// (Front Desk on the computer), Once runs one psql per question (the
// office game), and Direct talks to Postgres itself (Front Desk in
// Docker).
//
// Read only, three locks everywhere: the session's default (Session,
// Direct), BEGIN READ ONLY on each question, and SQL that is fixed
// text in the callers. All three refuse a question with a ";" before
// its end (Once: anywhere), and Direct anything but one statement that
// gives one column.
//
// The third lock is the one that matters: pass only constant SQL,
// with nothing in it from a person but names that passed the rules'
// checks. Read-only mode stops writes to tables, not everything. The
// stack's litellm role is a superuser, and a SELECT may call
// functions that act anyway inside a read-only transaction:
// pg_terminate_backend (ends other connections),
// pg_create_physical_replication_slot (a slot that holds WAL
// forever), pg_replication_origin_create, lo_export (writes a file on
// the database's machine), pg_import_system_collations (catalog rows),
// pg_read_file and pg_ls_dir (read any file the database's server
// can, such as /proc/1/environ with POSTGRES_PASSWORD in it), and
// pg_stat_reset and pg_switch_wal (reset statistics, write WAL).
// Direct resets what a question left on its kept connection (settings,
// advisory locks) after each one. A role with only pg_read_all_data
// would close the rest (spec question 23.9).
package pgread

import (
	"context"
	"encoding/json"
	"strings"
)

// Error is a reason in plain words, safe to show after a scrub.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func fail(msg string) error { return &Error{Msg: msg} }

// errSeveral is the refusal of a question with a ";" before its end.
var errSeveral = fail("refused: a question must be one statement, " +
	"with a ; only at its end")

// oneStatement refuses sql with a ";" anywhere but at its end (even
// inside quotes: the callers' SQL has none there). Python sent such
// text as it was; ledger B-16.
func oneStatement(sql string) error {
	body := strings.TrimSuffix(strings.TrimSpace(sql), ";")
	if strings.Contains(body, ";") {
		return errSeveral
	}
	return nil
}

// errBackslash is the refusal of a question holding a backslash.
var errBackslash = fail("refused: a question for psql must hold " +
	"no \\")

// noBackslash refuses sql holding a backslash. Session and Once hand the
// text to psql's stdin, where a backslash starts one of psql's own
// commands: \g sends a statement with no ";", and \! runs a shell
// command. The callers' SQL has none. Python sent such text as it
// was; ledger B-16.
func noBackslash(sql string) error {
	if strings.Contains(sql, "\\") {
		return errBackslash
	}
	return nil
}

// Querier is Front Desk's tally source: one read-only statement
// whose single value is a JSON object; returns that JSON text.
type Querier interface {
	Query(ctx context.Context, sql string) (json.RawMessage, error)
	Stop()
}

var (
	_ Querier = (*Session)(nil)
	_ Querier = (*Direct)(nil)
)
