// Package mintkey is mint-key's logic: it mints and revokes seat
// keys, like mint-key.sh, with nothing else to install. `lvp
// mint-key` and the mint-key program both run Main, so they print the
// same bytes; Front Desk runs Exec, the same rules in its own process.
//
//	mint-key <seat> [--models a,b] [--budget USD] [--duration 30d]
//	                [--budget-duration 1d]
//	mint-key --revoke <key>
//	mint-key --revoke-seat <seat>
//
// It prints only the key, or "revoked" or "revoked seat <seat>".
// Errors go to stderr. Exit code 2 means a usage or value mistake,
// 1 any other failure. The master key stays in seats.env, is never
// printed and never goes on a command line.
package mintkey

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/litellm"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// Version is set by the main package, from its own -X main.version.
var Version = "dev"

const usageText = `
Mint a scoped key for one seat. The master key stays in seats.env and
is never printed.

  mint-key <seat> [--models a,b] [--budget USD] [--duration 30d]
                  [--budget-duration 1d]
  mint-key --revoke <key>
  mint-key --revoke-seat <seat>
  mint-key --version

Type ./mint-key on Linux and macOS, mint-key.exe in Windows cmd, or
.\mint-key.exe in PowerShell. seats.env is read from SEATS_ENV if
that is set, else from this folder, else from the program's folder.
`

// The same checks as mint-key.sh.
var (
	budgetRE   = regexp.MustCompile(`^[0-9]+([.][0-9]+)?$`)
	durationRE = regexp.MustCompile(`^[0-9]+(s|m|h|d|w|mo)$`)
	refillRE   = regexp.MustCompile(`^([1-9][0-9]*[smhdw]|1mo)$`)
)

// System is what Main needs from the computer, so tests can fake it.
type System struct {
	Env     seats.LookupFunc
	Getwd   func() (string, error)
	Exe     func() (string, error)
	Timeout time.Duration
}

// OS is the real computer.
func OS() System {
	return System{Env: os.LookupEnv, Getwd: os.Getwd, Exe: os.Executable}
}

// stop is how a run ends early: a message for stderr and an exit code.
type stop struct {
	code int
	msg  string
}

func usage() *stop { return &stop{2, strings.TrimSpace(usageText)} }

// Usage is the usage text mint-key prints after a mistake, for `lvp
// help mint-key`, which shows it without needing seats.env.
func Usage() string { return strings.TrimSpace(usageText) + "\n" }

// UsageFor is the usage with the command typed as cmd, such as
// "./lvp mint-key", for `lvp help mint-key`. The mint-key program
// keeps Usage's words.
func UsageFor(cmd string) string {
	const opts = "[--models a,b] [--budget USD] [--duration 30d]"
	const refill = "[--budget-duration 1d]"
	// Laid out as Usage, with refill under opts; a long cmd (a path)
	// puts every option on the next line.
	first := "  " + cmd + " <seat> " + opts + "\n" +
		strings.Repeat(" ", len(cmd)+10) + refill + "\n"
	if len(cmd) > 14 {
		first = "  " + cmd + " <seat>\n      " + opts + " " + refill +
			"\n"
	}
	return "Mint a scoped key for one seat. The master key stays in " +
		"seats.env and\nis never printed.\n\n" + first +
		"  " + cmd + " --revoke <key>\n" +
		"  " + cmd + " --revoke-seat <seat>\n" +
		"  " + cmd + " --version\n\n" +
		"seats.env is read from SEATS_ENV if that is set, else from " +
		"this\nfolder, else from the program's folder.\n"
}

func die(msg string) *stop { return &stop{2, msg} }

func fail(err error) *stop { return &stop{1, err.Error()} }

// Main is mint-key exactly: what `lvp mint-key` and the shim run.
func Main(args []string, sys System, stdout, stderr io.Writer) int {
	r := run(context.Background(), args, func() (*seats.File, *stop) {
		return load(sys)
	}, sys.Env, sys.Timeout)
	io.WriteString(stdout, r.Stdout)
	io.WriteString(stderr, r.Stderr)
	return r.Code
}

// Result is what the mint-key program would print, and its code.
type Result struct {
	Stdout, Stderr string
	Code           int
}

// Exec runs the same rules on a seats.env already read, in this
// process. Front Desk and lvp mint use it. A web proxy is used only
// if file gives HTTPS_PROXY or HTTP_PROXY (or their lowercase forms),
// as a mint-key child given only file's settings would see; then
// the proxy settings are this process's, as for Main.
func Exec(ctx context.Context, file *seats.File, args []string,
	timeout time.Duration) Result {
	return run(ctx, args, func() (*seats.File, *stop) {
		return file, nil
	}, file.Lookup, timeout)
}

func run(ctx context.Context, args []string,
	load func() (*seats.File, *stop), env seats.LookupFunc,
	timeout time.Duration) Result {
	out, ex := handle(ctx, args, load, env, timeout)
	if ex != nil {
		return Result{Stderr: ex.msg + "\n", Code: ex.code}
	}
	return Result{Stdout: out + "\n"}
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// envProxy says whether env names a web proxy.
func envProxy(env seats.LookupFunc) bool {
	if env == nil {
		return false
	}
	for _, n := range []string{"HTTPS_PROXY", "https_proxy",
		"HTTP_PROXY", "http_proxy"} {
		if v, _ := env(n); v != "" {
			return true
		}
	}
	return false
}

func handle(ctx context.Context, args []string,
	load func() (*seats.File, *stop), env seats.LookupFunc,
	timeout time.Duration) (string, *stop) {
	if len(args) == 1 && args[0] == "--version" {
		return "mint-key " + fullVersion(), nil
	}
	file, ex := load()
	if ex != nil {
		return "", ex
	}
	master, err := file.MasterKey()
	if err != nil {
		return "", fail(err)
	}
	api := func() (*litellm.Client, *stop) {
		c, err := litellm.NewWith(litellm.Options{
			BaseURL: proxyURL(file), MasterKey: master,
			UserAgent: "mint-key/" + fullVersion(),
			Timeout:   timeout, EnvProxy: envProxy(env)})
		if err != nil {
			return nil, &stop{1, "PROXY_URL in " + file.Path +
				" " + err.Error()}
		}
		return c, nil
	}

	switch arg(args, 0) {
	case "--revoke":
		key := arg(args, 1)
		if key == "" {
			return "", usage()
		}
		c, ex := api()
		if ex != nil {
			return "", ex
		}
		if err := c.DeleteKeys(ctx, key); err != nil {
			return "", apiFail(err, master, lateRevoke)
		}
		return "revoked", nil
	case "--revoke-seat":
		seat := arg(args, 1)
		if seat == "" {
			return "", usage()
		}
		c, ex := api()
		if ex != nil {
			return "", ex
		}
		if err := c.DeleteSeats(ctx, seat); err != nil {
			return "", apiFail(err, master, lateRevoke)
		}
		return "revoked seat " + seat, nil
	}

	seat := arg(args, 0)
	if seat == "" || strings.HasPrefix(seat, "-") {
		return "", usage()
	}
	models := file.Get("DEFAULT_MODELS", "local")
	budget := file.Get("DEFAULT_BUDGET", "5")
	duration := file.Get("DEFAULT_DURATION", "30d")
	refill := "" // empty: the budget is a lifetime total
	for rest := args[1:]; len(rest) > 0; rest = rest[2:] {
		if len(rest) < 2 {
			return "", usage()
		}
		switch rest[0] {
		case "--models":
			models = rest[1]
		case "--budget":
			budget = rest[1]
		case "--duration":
			duration = rest[1]
		case "--budget-duration":
			refill = rest[1]
		default:
			return "", usage()
		}
	}

	usd, err := strconv.ParseFloat(budget, 64)
	if !budgetRE.MatchString(budget) || err != nil {
		return "", die("--budget must be a number of US dollars, " +
			"like 5 or 2.50")
	}
	// LiteLLM units; note "m" is minutes and "mo" is months.
	if !durationRE.MatchString(duration) {
		return "", die("--duration must be a number and a unit, " +
			"like 30d, 12h or 2w")
	}
	// What LiteLLM accepts: a whole number and s, m, h, d or w; or
	// 1mo (LiteLLM refuses 2mo and up).
	if refill != "" && !refillRE.MatchString(refill) {
		return "", die("--budget-duration must look like 30m, 12h, " +
			"1d, 1w or 1mo")
	}
	var names []string
	for _, m := range strings.Split(models, ",") {
		if m = strings.TrimSpace(m); m != "" {
			names = append(names, m)
		}
	}
	// An empty list means "all models" to LiteLLM. Never mint that.
	if len(names) == 0 {
		return "", die("--models must name at least one model")
	}

	c, ex := api()
	if ex != nil {
		return "", ex
	}
	key, err := c.GenerateKey(ctx, litellm.GenerateRequest{
		KeyAlias:       seat,
		Models:         names,
		MaxBudget:      usd,
		Duration:       duration,
		Metadata:       litellm.Metadata{Seat: seat},
		BudgetDuration: refill,
	})
	if err != nil {
		// Its key, if it made one, was never shown: only a revoke
		// frees the seat name for a key that can be.
		return "", apiFail(err, master, "It got the request but "+
			"did not answer in time, so it may have made the key "+
			"anyway. To be sure, run this program again with "+
			"--revoke-seat "+seat+", then mint again.")
	}
	return key, nil
}

// proxyURL is where the proxy is: PROXY_URL, or the usual address.
func proxyURL(file *seats.File) string {
	return file.Get("PROXY_URL", "http://localhost:4000")
}

// load finds and reads seats.env.
func load(sys System) (*seats.File, *stop) {
	wd, _ := sys.Getwd()
	exeDir := ""
	if p, err := sys.Exe(); err == nil {
		exeDir = filepath.Dir(p)
	}
	path, err := seats.Find(sys.Env, wd, exeDir)
	if err != nil {
		return nil, fail(err)
	}
	file, err := seats.Load(path, sys.Env)
	if err != nil {
		return nil, fail(err)
	}
	return file, nil
}

// lateRevoke is what to do when a revoke got no answer in time.
const lateRevoke = "It got the request but did not answer in time, " +
	"so it may have revoked it anyway. Run the same line again to check."

// apiFail words an error from the proxy: what happened, what to do,
// then LiteLLM's own message last, as mint-key.sh prints it. late is
// what to do if the proxy got the request but answered too late.
func apiFail(err error, master, late string) *stop {
	var (
		se *litellm.StatusError
		ce *litellm.ConnError
	)
	var lines []string
	switch {
	case errors.As(err, &se):
		first := "the proxy returned " + se.Status()
		if h := se.Hint(); h != "" {
			first += ". " + h
		}
		lines = append(lines, first)
		if se.Message != "" {
			lines = append(lines, se.Message)
		}
	case errors.As(err, &ce):
		hint := ce.Hint
		if ce.Late {
			hint = late
		}
		lines = append(lines, ce.Error()+".", hint)
	default:
		lines = append(lines, err.Error())
	}
	msg := strings.Join(lines, "\n")
	// The client already hides the master key; this is a second lock.
	msg = strings.ReplaceAll(msg, master, litellm.Mask(master))
	return &stop{1, msg}
}

func fullVersion() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok &&
		bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}
