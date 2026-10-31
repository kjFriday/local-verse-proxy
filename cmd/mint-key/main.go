// Command mint-key mints and revokes seat keys, like mint-key.sh,
// with nothing else to install: no bash, curl or jq. It runs on
// Windows, macOS and Linux, and takes the same arguments:
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
package main

import (
	"context"
	"errors"
	"fmt"
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

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

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

// system is what run needs from the computer, so tests can fake it.
type system struct {
	env     seats.LookupFunc
	getwd   func() (string, error)
	exe     func() (string, error)
	timeout time.Duration
}

func main() {
	sys := system{env: os.LookupEnv, getwd: os.Getwd, exe: os.Executable}
	os.Exit(run(os.Args[1:], sys, os.Stdout, os.Stderr))
}

// stop is how run ends early: a message for stderr and an exit code.
type stop struct {
	code int
	msg  string
}

func usage() *stop { return &stop{2, strings.TrimSpace(usageText)} }

func die(msg string) *stop { return &stop{2, msg} }

func fail(err error) *stop { return &stop{1, err.Error()} }

func run(args []string, sys system, stdout, stderr io.Writer) int {
	out, ex := handle(args, sys)
	if ex != nil {
		fmt.Fprintln(stderr, ex.msg)
		return ex.code
	}
	fmt.Fprintln(stdout, out)
	return 0
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func handle(args []string, sys system) (string, *stop) {
	if len(args) == 1 && args[0] == "--version" {
		return "mint-key " + fullVersion(), nil
	}
	file, ex := load(sys)
	if ex != nil {
		return "", ex
	}
	master, err := file.MasterKey()
	if err != nil {
		return "", fail(err)
	}
	api := func() (*litellm.Client, *stop) {
		c, err := litellm.New(proxyURL(file), master, sys.timeout)
		if err != nil {
			return nil, &stop{1, "PROXY_URL in " + file.Path +
				" " + err.Error()}
		}
		c.UserAgent = "mint-key/" + fullVersion()
		return c, nil
	}
	ctx := context.Background()

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
func load(sys system) (*seats.File, *stop) {
	wd, _ := sys.getwd()
	exeDir := ""
	if p, err := sys.exe(); err == nil {
		exeDir = filepath.Dir(p)
	}
	path, err := seats.Find(sys.env, wd, exeDir)
	if err != nil {
		return nil, fail(err)
	}
	file, err := seats.Load(path, sys.env)
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
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok &&
		bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}
