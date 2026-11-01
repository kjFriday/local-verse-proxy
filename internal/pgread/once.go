package pgread

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os/exec"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

var inf = math.Inf(1)

// OnceArgv is the game's psql: one per question, stopping at the
// first error.
var OnceArgv = []string{"docker", "compose", "exec", "-T", "db",
	"psql", "-U", "litellm", "-d", "litellm", "-X", "-q", "-A",
	"-t", "-v", "ON_ERROR_STOP=1"}

// onceArgv is what Once runs; tests point it at a fake.
var onceArgv = OnceArgv

// Once runs one psql for one SELECT, as office.py's pg(): stdin
// "BEGIN TRANSACTION READ ONLY;\nSELECT coalesce(json_agg(q),
// '[]') FROM (<sql>) q;\nCOMMIT;\n". Error text: "cannot read the
// proxy database: <stderr, stripped>". Rows keep json.Number. sql
// goes inside a subquery, so a ";" anywhere in it is refused before
// psql starts: it could only end the wrapper early (ledger B-16).
func Once(ctx context.Context, dir string,
	sql string) ([]map[string]any, error) {
	if strings.Contains(sql, "\\") {
		return nil, fail("cannot read the proxy database: refused: " +
			"a question for psql must hold no \\")
	}
	if strings.Contains(sql, ";") {
		return nil, fail("cannot read the proxy database: refused: " +
			"a question must be one statement, with no ;")
	}
	script := "BEGIN TRANSACTION READ ONLY;\n" +
		"SELECT coalesce(json_agg(q), '[]') FROM (" + sql + ") q;\n" +
		"COMMIT;\n"
	cmd := exec.CommandContext(ctx, onceArgv[0], onceArgv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewBufferString(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, fail("cannot read the proxy database: " +
				"cannot run docker: " + pyfmt.StrError(err))
		}
		return nil, fail("cannot read the proxy database: " +
			pyfmt.Strip(stderr.String()))
	}
	text := pyfmt.Strip(stdout.String())
	if text == "" {
		text = "[]"
	}
	dec := json.NewDecoder(bytes.NewBufferString(text))
	dec.UseNumber()
	var rows []map[string]any
	if err := dec.Decode(&rows); err != nil || dec.More() {
		return nil, fail("cannot read the proxy database: its " +
			"answer is not the rows expected")
	}
	return rows, nil
}
