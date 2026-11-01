// Owner: C3

// Package game is the office game on top of the proxy (lvp game): seats
// are players, AI budget is mana, finished work is the score. It is a
// line-by-line port of game/office.py and plays on the same
// game/office.db, so Python and Go can take turns on one week.
//
// The game never holds the master key: it mints and revokes through a
// child "lvp mint-key", calls the proxy with seat keys, and reads the
// proxy's Postgres in a READ ONLY transaction through psql.
package game

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/kit"
	"github.com/kjFriday/local-verse-proxy/internal/pgread"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
	"github.com/kjFriday/local-verse-proxy/internal/rules"
)

// Options are what Run uses instead of the real world, for tests. The
// zero value is the real world.
type Options struct {
	// Clock is the wall clock and sleeps; nil is the real one.
	Clock clock.Clock
	// Dir is the proxy folder; "" finds it with kit.Dir.
	Dir string
	// MintArgv runs mint-key; nil is this program with "mint-key".
	MintArgv []string
	// PG asks the proxy's database one SELECT; nil is pgread.Once.
	PG func(ctx context.Context, dir, sql string) ([]map[string]any,
		error)
	// Chat sends one chat call; nil is the real client (chat.go).
	Chat ChatFunc
	// TokenHex is secrets.token_hex; nil reads crypto/rand.
	TokenHex func(n int) string
	// Prog is how lvp is typed, for messages; "" is args.Prog().
	Prog string
	// Getenv reads LVG_PROXY_URL; nil is os.Getenv.
	Getenv func(string) string
}

// game is one run of a command: where things are, and the world.
type game struct {
	ctx      context.Context
	out, err io.Writer
	clk      clock.Clock
	root     string // the proxy folder (office.py's ROOT)
	dir      string // <root>/game (GAME)
	db       string // <dir>/office.db (DB)
	keys     string // <dir>/keys (KEYS)
	mintArgv []string
	pgFn     func(ctx context.Context, dir, sql string) (
		[]map[string]any, error)
	chat     ChatFunc
	tokenHex func(n int) string
	prog     string
	getenv   func(string) string
	rules    *rules.Rules
	cmd      string

	rulesFlag *string

	// mint and revoke are minter.go's, unless a test sets them.
	mintFn   func(alias string) (string, error)
	revokeFn func(alias string) (string, error)
}

// errStopped is how a command ends when Ctrl+C came.
var errStopped = errors.New("stopped")

// stopped is office.py's STOPPED, with the new command names.
func (g *game) stopped() string {
	switch g.cmd {
	case "setup":
		return "stopped. Clean up with `" + g.prog + " game reset`."
	case "run":
		return "stopped; this day is not scored. `run` plays it again."
	case "reset":
		return "stopped. Run `" + g.prog + " game reset` again."
	}
	return "stopped."
}

// Main is `lvp game`.
func Main(argv []string, std args.Std) int {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt)
	defer stop()
	return Run(ctx, argv, std, Options{})
}

var commands = []struct{ name, help string }{
	{"setup", "mint game seats, create office.db"},
	{"run", "play practice days with bots"},
	{"board", "show both leaderboards"},
	{"reset", "weekly reset: revoke seats, wipe"},
}

// Run is Main with a context (Ctrl+C) and Options.
func Run(ctx context.Context, argv []string, std args.Std,
	o Options) int {
	prog := o.Prog
	if prog == "" {
		prog = args.Prog()
	}
	top := prog + " game"
	topUsage := top + " [-h] [--rules FILE] {setup,run,board,reset} ..."
	i := findCommand(argv)
	if i < 0 {
		for _, a := range argv {
			if a == "-h" || a == "--help" {
				fmt.Fprint(std.Out, topHelp(topUsage))
				return 0
			}
			if a == "--" {
				break
			}
		}
		fmt.Fprintln(std.Err, &args.UsageError{Cmd: top,
			Usage: topUsage, Msg: "the following arguments are " +
				"required: cmd"})
		return 2
	}
	for _, a := range argv[:i] {
		if a == "-h" || a == "--help" {
			fmt.Fprint(std.Out, topHelp(topUsage))
			return 0
		}
	}
	cmd := argv[i]
	known := false
	for _, c := range commands {
		known = known || c.name == cmd
	}
	if !known {
		fmt.Fprintln(std.Err, &args.UsageError{Cmd: top,
			Usage: topUsage, Msg: "argument cmd: invalid choice: " +
				quote(cmd) + " (choose from 'setup', 'run', " +
				"'board', 'reset')"})
		return 2
	}
	rest := append(append([]string{}, argv[:i]...), argv[i+1:]...)
	g := &game{ctx: ctx, out: std.Out, err: std.Err, prog: prog,
		cmd: cmd}
	fs, run := g.flags(top, cmd)
	pos, err := fs.Parse(rest)
	if err == nil && len(pos) > 0 {
		err = &args.UsageError{Cmd: top, Usage: topUsage,
			Msg: "unrecognized arguments: " +
				strings.Join(pos, " ")}
	}
	if err != nil {
		return args.Exit(std, fs, err)
	}
	if err := g.setUp(o); err != nil {
		fmt.Fprintln(std.Err, err)
		return 1
	}
	path := *g.rulesFlag
	if path == "" {
		path = filepath.Join(g.dir, "rules.toml")
	}
	r, warnings, err := rules.Load(path)
	for _, w := range warnings {
		fmt.Fprintln(std.Err, w)
	}
	if err != nil {
		fmt.Fprintln(std.Err, err)
		return 1
	}
	g.rules = r
	err = run()
	if ctx.Err() != nil {
		fmt.Fprintln(std.Err, "\n"+g.stopped())
		return 1
	}
	if err != nil {
		fmt.Fprintln(std.Err, err)
		return 1
	}
	return 0
}

// findCommand is the index of the command word: the first argument
// that is not a flag or the value of --rules.
func findCommand(argv []string) int {
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			if i+1 < len(argv) {
				return i + 1
			}
			return -1
		case a == "--rules":
			i++
		case len(a) > 1 && a[0] == '-':
		default:
			return i
		}
	}
	return -1
}

func quote(s string) string {
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

func topHelp(usage string) string {
	var b strings.Builder
	b.WriteString("usage: " + usage + "\n\nOffice game on top of the " +
		"proxy (game/README.md).\n\ncommands:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-19s %s\n", c.name, c.help)
	}
	b.WriteString("\noptions:\n" +
		"  -h, --help          show this help and exit\n" +
		"  --rules FILE        rules file (default: game/rules.toml)\n")
	return b.String()
}

// flags builds the command's flags, and what runs it.
func (g *game) flags(top, cmd string) (*args.Set, func() error) {
	name := top + " " + cmd
	var fs *args.Set
	var run func() error
	switch cmd {
	case "setup":
		fs = args.New(name, name+" [-h] [--rules FILE]")
		run = g.cmdSetup
	case "run":
		fs = args.New(name, name+" [-h] [--rules FILE] [-d N] [-q]")
		days := fs.Int("days", "d", "N", 1, positive,
			"days to play (default 1)")
		quiet := fs.Bool("quiet", "q", "day summaries only, no board")
		run = func() error { return g.cmdRun(*days, *quiet) }
	case "board":
		fs = args.New(name, name+
			" [-h] [--rules FILE] [-d N] [-s NAME]")
		day := fs.Int("day", "d", "N", 0, positive, "one day only")
		seat := fs.String("seat", "s", "NAME", "",
			"one seat, quest by quest (e.g. fern)")
		run = func() error { return g.cmdBoard(*day, *seat) }
	default:
		fs = args.New(name, name+" [-h] [--rules FILE]")
		run = g.cmdReset
	}
	g.rulesFlag = fs.String("rules", "", "FILE", "",
		"rules file (default: game/rules.toml)")
	return fs, run
}

// setUp finds the folders and fills in the world from o.
func (g *game) setUp(o Options) error {
	g.clk = o.Clock
	if g.clk == nil {
		g.clk = clock.Real()
	}
	g.root = o.Dir
	if g.root == "" {
		d, err := kit.Dir()
		if err != nil {
			return err
		}
		g.root = d
	}
	g.dir = filepath.Join(g.root, "game")
	g.db = filepath.Join(g.dir, "office.db")
	g.keys = filepath.Join(g.dir, "keys")
	g.mintArgv = o.MintArgv
	if g.mintArgv == nil {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("cannot find this program, to run "+
				"mint-key: %v", err)
		}
		g.mintArgv = []string{exe, "mint-key"}
	}
	g.pgFn = o.PG
	if g.pgFn == nil {
		g.pgFn = pgread.Once
	}
	g.chat = o.Chat
	if g.chat == nil {
		g.chat = Chat
	}
	g.tokenHex = o.TokenHex
	if g.tokenHex == nil {
		g.tokenHex = tokenHex
	}
	g.getenv = o.Getenv
	if g.getenv == nil {
		g.getenv = os.Getenv
	}
	g.mintFn, g.revokeFn = g.mint, g.revoke
	return nil
}

// tokenHex is secrets.token_hex(n).
func tokenHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported systems
	}
	return hex.EncodeToString(b)
}

// positive is office.py's positive(): int(text), 1 or more.
func positive(text string) (int, error) {
	n, ok := pyInt(text)
	if !ok || n < 1 {
		return 0, errors.New(pyfmt.ReprString(text) + ": use 1 or more")
	}
	return n, nil
}
