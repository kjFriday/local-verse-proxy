package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/fsx"
)

// DotEnv is Docker Compose's own settings file, next to compose.yaml:
// LITELLM_TAG, FRONTDESK_BIND, COMPOSE_FILE and the like. It is not
// seats.env, which is for the containers.
const DotEnv = ".env"

// dotEnv is .env in dir, read; a missing file is an empty one. A
// file saved as UTF-16 (PowerShell's >>) is read, and written back
// as UTF-8, which Compose reads; utf16 says so.
type dotEnv struct {
	path  string
	t     *textFile
	utf16 bool
}

func readDotEnv(dir string) (*dotEnv, error) {
	d := &dotEnv{path: filepath.Join(dir, DotEnv)}
	b, err := os.ReadFile(d.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("cannot read " + d.path + ": " +
			plainErr(err))
	}
	if s, ok := fromUTF16(b); ok {
		b, d.utf16 = []byte(s), true
	}
	d.t = readText(b)
	d.t.bom = false // UTF-8 without a byte order mark
	return d, nil
}

func (d *dotEnv) write() error {
	if err := fsx.WriteFile(d.path, d.t.bytes(), false); err != nil {
		return errors.New("cannot write " + d.path + ": " +
			plainErr(err))
	}
	return nil
}

// get is the value of the last line that sets n, unquoted, and
// whether there is one.
func (d *dotEnv) get(n string) (string, bool) {
	at := d.t.find(n)
	if len(at) == 0 {
		return "", false
	}
	v := strings.TrimSpace(value(d.t.lines[at[len(at)-1]]))
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') &&
		v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	return v, true
}

// set makes line the one line that sets n: in place of the first
// one there, the others dropped; else at the end.
func (d *dotEnv) set(n, line string) {
	at := d.t.find(n)
	if len(at) == 0 {
		d.t.add(line)
		return
	}
	d.t.lines[at[0]] = line
	for k := len(at) - 1; k > 0; k-- {
		d.t.drop(at[k])
	}
}

// unset drops every line that sets n; how many went.
func (d *dotEnv) unset(n string) int {
	at := d.t.find(n)
	for k := len(at) - 1; k >= 0; k-- {
		d.t.drop(at[k])
	}
	return len(at)
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// secretName is a setting whose value is not shown.
func secretName(n string) bool {
	u := strings.ToUpper(n)
	for _, w := range []string{"KEY", "PASSWORD", "SECRET", "TOKEN",
		"PASS", "AUTH", "CREDENTIAL"} {
		if strings.Contains(u, w) {
			return true
		}
	}
	return false
}

// shown is a .env line as it may be printed: a secret's value hidden.
func shown(line string) string {
	if n := name(line); n != "" && secretName(n) {
		return n + "=(hidden)"
	}
	return line
}

// envLine is NAME=value as Compose reads it back: plain if it can be,
// else in single quotes, which Compose takes as they are.
func envLine(n, v string) (string, error) {
	if strings.ContainsAny(v, "\r\n") {
		return "", errors.New("a value must be on one line")
	}
	if !strings.ContainsAny(v, " \t#'\"$\\`") {
		return n + "=" + v, nil
	}
	if strings.ContainsRune(v, '\'') {
		return "", errors.New("a value with a ' and other signs " +
			"cannot be written so that Compose reads it back " +
			"the same")
	}
	return n + "='" + v + "'", nil
}

// upFor is the command that makes Compose read .env again for n.
func upFor(n string) string {
	switch {
	case strings.HasPrefix(n, "FRONTDESK_"):
		return "docker compose --profile frontdesk up -d"
	case n == "LITELLM_TAG":
		return "docker compose pull, then docker compose up -d"
	}
	return "docker compose up -d"
}

// EnvCmd is `lvp env [set NAME=value | unset NAME]`.
func (s *System) EnvCmd(_ context.Context, argv []string,
	std args.Std) int {
	cmd := s.Prog + " env"
	usage := cmd + " [set NAME=value | unset NAME]"
	fl := args.New(cmd, usage)
	pos, err := fl.Parse(argv)
	if err != nil {
		if errors.Is(err, args.ErrHelp) {
			fmt.Fprint(std.Out, fl.Help()+envHelp)
			return 0
		}
		return args.Exit(std, fl, err)
	}
	var verb, arg string
	switch {
	case len(pos) == 0:
	case len(pos) == 2 && (pos[0] == "set" || pos[0] == "unset"):
		verb, arg = pos[0], pos[1]
	case len(pos) == 1 && (pos[0] == "set" || pos[0] == "unset"):
		return usageError(std, cmd, usage, pos[0]+" needs "+
			map[string]string{"set": "NAME=value",
				"unset": "NAME"}[pos[0]])
	default:
		return usageError(std, cmd, usage, "unrecognized arguments: "+
			strings.Join(pos, " "))
	}
	n, v := arg, ""
	if verb == "set" {
		var ok bool
		if n, v, ok = strings.Cut(arg, "="); !ok {
			return usageError(std, cmd, usage, "set needs "+
				"NAME=value, with = between them")
		}
	}
	if verb != "" && !envName.MatchString(n) {
		return usageError(std, cmd, usage, "a name is letters, "+
			"digits and _, and does not start with a digit")
	}
	dir, err := s.folder()
	if err != nil {
		return fail(std, err.Error())
	}
	d, err := readDotEnv(dir)
	if err != nil {
		return fail(std, err.Error())
	}
	switch verb {
	case "":
		return s.showEnv(std, d)
	case "set":
		line, err := envLine(n, v)
		if err != nil {
			return usageError(std, cmd, usage, err.Error())
		}
		d.set(n, line)
		if err := d.write(); err != nil {
			return fail(std, err.Error())
		}
		say(std.Out, ".env now has: "+shown(line))
	case "unset":
		gone := d.unset(n)
		if gone == 0 {
			say(std.Out, n+" is not in .env; nothing changed.")
			return 0
		}
		if err := d.write(); err != nil {
			return fail(std, err.Error())
		}
		say(std.Out, "Removed "+n+" from .env.")
	}
	if d.utf16 {
		say(std.Out, ".env was saved as UTF-16; it is UTF-8 now, "+
			"which Compose reads.")
	}
	say(std.Out, "To apply it, run: "+upFor(n))
	return 0
}

func (s *System) showEnv(std args.Std, d *dotEnv) int {
	some := false
	for _, l := range d.t.lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		fmt.Fprintln(std.Out, shown(l))
		some = true
	}
	if !some {
		say(std.Out, ".env sets nothing yet. To set something: "+
			s.Prog+" env set NAME=value")
	}
	return 0
}

const envHelp = `
Shows, sets or removes a line in .env, next to compose.yaml. Docker
Compose reads .env (LITELLM_TAG, FRONTDESK_BIND and the like); it is
not seats.env. Other lines stay as they are. Values of names with
KEY, PASSWORD, SECRET or TOKEN in them are not shown.
`
