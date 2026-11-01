// Owner: C2

// Package frontdesk is Front Desk: the web page for the proxy's key
// cards and tally (lvp desk). It reads the master key from seats.env,
// as mint-key does, and never sends it to a browser.
//
//	lvp desk          # this computer only
//	lvp desk --lan    # phones and laptops on your network
//
// In Docker the image runs `lvp desk --docker`: settings come from
// the environment (compose passes seats.env in), the tally straight
// from Postgres, and the password is always asked for.
package frontdesk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/config"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/desk"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/gate"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/web"
	"github.com/kjFriday/local-verse-proxy/internal/kit"
	"github.com/kjFriday/local-verse-proxy/internal/pgread"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// Usage is lvp desk's usage line.
const Usage = "lvp desk [--lan] [--login] [--port N] [--host NAME]...\n" +
	"                [--seat-url URL] [--env FILE] [--docker]"

const description = "Front Desk: a web page for the proxy's key cards."

// Again is how a --docker message ends.
const Again = "then run: docker compose --profile frontdesk up -d"

// MintEnv is all of mint-key's environment with --docker. Not the
// database password or the model keys, which it never needs.
var MintEnv = []string{"PATH", "HOME", "LANG", "TZ", "SEATS_ENV",
	"PROXY_URL", "LITELLM_MASTER_KEY", "LITELLM_SALT_KEY",
	"DEFAULT_MODELS", "DEFAULT_BUDGET", "DEFAULT_DURATION"}

// System is what Front Desk needs from the computer, so tests can
// fake it.
type System struct {
	Environ   func() []string
	LookupEnv func(string) (string, bool)
	Unsetenv  func(string) error
	Getwd     func() (string, error)
	Exe       func() (string, error)
	Hostname  func() (string, error)
	LANIP     func() string
	Prog      func() string
	Clock     clock.Clock
	// Stop gives a channel that is sent to on Ctrl+C or SIGTERM, and
	// a function that stops listening.
	Stop func() (<-chan os.Signal, func())
	// Ready, if set, is told the address Front Desk listens on.
	Ready func(net.Addr)
	// Gate, if set, makes the gate (tests pin its random bytes).
	Gate func(password string, has bool) *gate.Gate
	// Tally, if set, makes the tally source instead of psql or
	// Postgres (tests).
	Tally func(docker bool, dir, dbURL string) pgread.Querier
}

// OS is the real computer.
func OS() System {
	return System{Environ: os.Environ, LookupEnv: os.LookupEnv,
		Unsetenv: os.Unsetenv, Getwd: os.Getwd, Exe: os.Executable,
		Hostname: os.Hostname, LANIP: web.LANIP, Prog: args.Prog,
		Clock: clock.Real(),
		Stop: func() (<-chan os.Signal, func()) {
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
			return ch, func() { signal.Stop(ch) }
		}}
}

// Main is `lvp desk`.
func Main(argv []string, std args.Std) int { return Run(argv, std, OS()) }

type flags struct {
	lan, login, docker bool
	port               int
	hosts              []string
	seatURL, env       string
	envGiven           bool
}

func parse(argv []string, std args.Std) (*flags, int, bool) {
	s := args.New("lvp desk", Usage)
	f := &flags{}
	lan := s.Bool("lan", "", "let phones and laptops on your network "+
		"in (asks for a password)")
	port := s.Int("port", "", "N", 8040, checkPort,
		"port for the page (default 8040)")
	login := s.Bool("login", "", "ask for a password on this computer "+
		"too")
	hosts := s.List("host", "", "NAME", "with --lan, a name for this "+
		"machine that browsers may use, besides its IP address")
	seatURL := s.String("seat-url", "", "URL", "", "base URL shown to "+
		"seats, if the guess is wrong")
	env := s.String("env", "", "FILE", "\x00", "secrets file "+
		"(default: seats.env)")
	docker := s.Bool("docker", "", "how the Docker image runs it: "+
		"settings from the environment, on 0.0.0.0, with a password")
	rest, err := s.Parse(argv)
	if errors.Is(err, args.ErrHelp) {
		help := s.Help()
		if i := strings.Index(help, "\n\n"); i >= 0 {
			help = help[:i] + "\n\n" + description + help[i:]
		}
		fmt.Fprint(std.Out, help)
		return nil, 0, false
	}
	if err == nil && len(rest) > 0 {
		err = &args.UsageError{Cmd: "lvp desk", Usage: Usage,
			Msg: "unrecognized arguments: " + strings.Join(rest,
				" ")}
	}
	if err != nil {
		return nil, args.Exit(std, s, err), false
	}
	f.lan, f.login, f.docker, f.port = *lan, *login, *docker, *port
	f.hosts = append([]string(nil), *hosts...)
	f.seatURL = *seatURL
	f.env, f.envGiven = *env, *env != "\x00"
	return f, 0, true
}

func checkPort(text string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, fmt.Errorf("invalid int value: %s",
			pyfmt.ReprString(text))
	}
	if n < 0 || n > 65535 {
		return 0, fmt.Errorf("%s: use 0 to 65535",
			pyfmt.ReprString(text))
	}
	return n, nil
}

// fail is sys.exit(msg): the message on stderr, exit 1.
func fail(std args.Std, msg string) int {
	fmt.Fprintln(std.Err, msg)
	return 1
}

// Run is Main on a given computer.
func Run(argv []string, std args.Std, sys System) int {
	f, code, ok := parse(argv, std)
	if !ok {
		return code
	}
	log := config.NewLog(std.Err)
	lookup := sys.LookupEnv
	var settings *config.Settings
	var dir string
	publicPort := f.port
	if f.docker {
		var err error
		settings, err = config.FromEnviron(sys.Environ())
		if err != nil {
			return fail(std, err.Error())
		}
		if settings.Master == "" {
			return fail(std, "LITELLM_MASTER_KEY is not set. "+
				"Put it in seats.env, next to compose.yaml "+
				"(README Step 3a), "+Again)
		}
		if p, _ := lookup("FRONTDESK_PORT"); digits(p) {
			if n, err := strconv.Atoi(p); err == nil && n > 0 &&
				n < 65536 {
				publicPort = n
			}
		}
		hosts, _ := lookup("FRONTDESK_HOSTS")
		for _, n := range splitHosts.Split(hosts, -1) {
			if n != "" {
				f.hosts = append(f.hosts, n)
			}
		}
		if f.seatURL == "" {
			f.seatURL, _ = lookup("FRONTDESK_SEAT_URL")
		}
		wd, err := sys.Getwd()
		if err != nil {
			wd = "."
		}
		dir = wd
	} else {
		wd, _ := sys.Getwd()
		exeDir := ""
		if exe, err := sys.Exe(); err == nil {
			exeDir = filepath.Dir(exe)
		}
		var err error
		dir, err = kit.DirFrom(wd, exeDir)
		if err != nil {
			return fail(std, err.Error())
		}
		path := f.env
		if !f.envGiven {
			if v, ok := lookup("SEATS_ENV"); ok {
				path = v
			} else {
				path = filepath.Join(dir, seats.Name)
			}
		}
		settings, err = config.LoadFile(path, lookup)
		if err != nil {
			return fail(std, err.Error())
		}
		if settings.Master == "" {
			return fail(std, "LITELLM_MASTER_KEY: not set in "+
				settings.Path)
		}
	}

	// Read once, then gone from the environment: no child gets it.
	password, _ := lookup("FRONTDESK_PASSWORD")
	sys.Unsetenv("FRONTDESK_PASSWORD")
	// In Docker, everyone may come through one port mapping: the
	// proxy machine can't be told from a phone, so all log in.
	lan := f.lan || f.docker
	has, shown := false, false
	if lan || f.login {
		tail := "."
		if f.docker {
			tail = ". Fix it in seats.env, " + Again
		}
		if password != "" && utf8.RuneCountInString(password) < 12 {
			return fail(std, "FRONTDESK_PASSWORD must be 12 "+
				"characters or more"+tail)
		}
		if password != "" && (!utf8.ValidString(password) ||
			strings.ContainsRune(password, utf8.RuneError)) {
			msg := "FRONTDESK_PASSWORD has a character that " +
				"is not plain text. Pick another password."
			if f.docker {
				msg += " Fix it in seats.env, " + Again
			}
			return fail(std, msg)
		}
		shown = password == ""
		if shown {
			password = gate.MakePassword()
		}
		has = true
	} else {
		password = ""
	}

	var tally pgread.Querier
	game := filepath.Join(dir, "game")
	switch {
	case sys.Tally != nil:
		tally = sys.Tally(f.docker, dir, settings.DBURL)
	case f.docker:
		tally = pgread.NewDirect(settings.DBURL, sys.Clock)
	default:
		tally = pgread.NewSession(pgread.SessionConfig{Dir: dir,
			Clock: sys.Clock})
	}
	live := config.NewLive(settings, log, lookup)
	d := desk.New(desk.Options{Live: live, Log: log, Clock: sys.Clock,
		Tally: tally, LAN: lan, Game: game})
	var g *gate.Gate
	if sys.Gate != nil {
		g = sys.Gate(password, has)
	} else {
		g = gate.New(password, has, sys.Clock, nil)
	}
	hostname, _ := sys.Hostname()
	reset := sys.Prog() + " game reset"
	if f.docker {
		reset = "lvp game reset"
	}
	app, err := web.New(web.Options{Docker: f.docker, LAN: f.lan,
		Port: f.port, PublicPort: publicPort, Hosts: f.hosts,
		SeatURL: f.seatURL, Hostname: hostname, LANIP: sys.LANIP,
		ResetCmd: reset, Live: live, Log: log, Desk: d, Gate: g,
		Clock:    sys.Clock,
		MintFile: mintFile(f.docker, live, sys)})
	if err != nil {
		return fail(std, "cannot read the page's files: "+err.Error())
	}
	bind := "127.0.0.1"
	per := 0
	if lan {
		bind, per = "0.0.0.0", web.MaxPerDevice
	}
	l, err := web.Listen(bind, f.port, per)
	if err != nil {
		tally.Stop()
		return fail(std, fmt.Sprintf("cannot use port %d: %s. Try "+
			"--port %d.", f.port, pyfmt.StrError(err), f.port+1))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	d.Kick() // one check now: psql's start is paid here

	if f.docker {
		dockerBanner(std.Out, lookup, publicPort, password, shown)
	} else {
		hostBanner(std.Out, f, sys, password, has, shown)
	}
	if sys.Ready != nil {
		sys.Ready(l.Addr())
	}
	return serve(std, sys, app.Server(), l, d)
}

var splitHosts = regexp.MustCompile(`[` + pyfmt.SpaceClass() + `,]+`)

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// mintFile is the seats.env minting reads. Host mode: the file, read
// fresh for each call (as mint-key.sh did). --docker: the environment
// cut to MintEnv, behind the image's stand-in file SEATS_ENV names,
// so mint-key's messages name it (ledger base.md, point 6).
func mintFile(docker bool, live *config.Live,
	sys System) func() (*seats.File, error) {
	if !docker {
		return func() (*seats.File, error) {
			return seats.Load(live.Get().Path, sys.LookupEnv)
		}
	}
	return func() (*seats.File, error) {
		var cut []string
		vals := map[string]string{}
		for _, kv := range sys.Environ() {
			k, v, _ := strings.Cut(kv, "=")
			for _, n := range MintEnv {
				if k == n {
					if _, dup := vals[k]; !dup {
						vals[k] = v
						cut = append(cut, kv)
					}
				}
			}
		}
		look := func(n string) (string, bool) {
			v, ok := vals[n]
			return v, ok
		}
		if p := vals["SEATS_ENV"]; p != "" {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return seats.Load(p, look)
			}
		}
		return seats.FromEnviron(cut), nil
	}
}

func hostBanner(w io.Writer, f *flags, sys System, password string,
	has, shown bool) {
	var b strings.Builder
	b.WriteString("Front Desk is open.\n")
	fmt.Fprintf(&b, "  On this computer: http://127.0.0.1:%d\n", f.port)
	if f.lan {
		ip := sys.LANIP()
		if ip == "" {
			ip = "<proxy-ip>"
		}
		fmt.Fprintf(&b, "  On your network:  http://%s:%d\n", ip,
			f.port)
		b.WriteString("  Plain http: use it only on a network you " +
			"trust.\n")
	}
	switch {
	case has && shown:
		b.WriteString("  Password, shown once: " + password + "\n")
	case has:
		b.WriteString("  Password: the one in FRONTDESK_PASSWORD\n")
	default:
		b.WriteString("  Anyone logged in to this computer can use " +
			"it; on a shared one, add --login.\n")
	}
	b.WriteString("Stop it with Ctrl+C.\n")
	io.WriteString(w, b.String())
}

func dockerBanner(w io.Writer, lookup func(string) (string, bool),
	port int, password string, shown bool) {
	bind, ok := lookup("FRONTDESK_BIND")
	if !ok {
		bind = "127.0.0.1"
	}
	local := bind == "localhost"
	if pyfmt.IsIP(bind) {
		local = pyfmt.IsLoopback(bind)
	}
	var b strings.Builder
	b.WriteString("Front Desk is open, in Docker.\n")
	fmt.Fprintf(&b, "  On this computer: http://127.0.0.1:%d\n", port)
	if local {
		b.WriteString("  Other devices: put FRONTDESK_BIND=0.0.0.0 " +
			"in .env (gui/README.md)\n")
	} else {
		fmt.Fprintf(&b, "  On your network:  http://<proxy-ip>:%d\n",
			port)
		b.WriteString("  Plain http: use it only on a network you " +
			"trust.\n")
	}
	if shown {
		b.WriteString("  Password, new at each start: " + password +
			"\n")
		b.WriteString("  To keep one, set FRONTDESK_PASSWORD in " +
			"seats.env (12 characters or more), " + Again + "\n")
	} else {
		b.WriteString("  Password: the one in FRONTDESK_PASSWORD in " +
			"seats.env\n")
	}
	b.WriteString("Stop it with: docker compose stop frontdesk\n")
	io.WriteString(w, b.String())
}

// serve answers until Ctrl+C or SIGTERM (ledger F-20): then an empty
// line, psql stopped, exit 0.
func serve(std args.Std, sys System, srv *http.Server, l net.Listener,
	d *desk.Desk) int {
	sig, stop := sys.Stop()
	defer stop()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()
	select {
	case <-sig:
		fmt.Fprintln(std.Out)
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(std.Err,
				"Front Desk stopped: "+err.Error())
			srv.Close()
			d.Stop()
			return 1
		}
	}
	srv.Close()
	d.Stop()
	return 0
}
