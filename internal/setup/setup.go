// Owner: C5

// Package setup is the beginner helpers: lvp init, check, wait, test,
// mint, revoke, env and game practice. Each replaces a few lines of
// shell (cp, chmod, openssl, sed, grep, curl, echo >> .env) that the
// README used to ask for, and works the same on Windows, macOS and
// Linux. None of them prints a secret: the master key, the salt key
// and the passwords stay in seats.env, and only a seat key the user
// asked for is shown.
package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/kit"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// Ready is true: the helpers are built, so lvp help lists them and
// lvp runs them.
const Ready = true

// System is what the helpers need from the computer, so tests can
// fake it.
type System struct {
	Getwd func() (string, error)
	Exe   func() (string, error)
	Env   seats.LookupFunc

	// Docker runs docker with args in dir, its output to stdout and
	// stderr. An error means it could not start, or exited non-zero.
	Docker func(ctx context.Context, dir string,
		stdout, stderr io.Writer, args ...string) error

	Sleep func(ctx context.Context, d time.Duration) error
	Now   func() time.Time
	Rand  io.Reader

	// Prog is how to type lvp here, like "./lvp", for messages.
	Prog string
	GOOS string
}

// OS is the real computer.
func OS() *System {
	return &System{
		Getwd: os.Getwd,
		Exe: func() (string, error) {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			if p, err := filepath.EvalSymlinks(exe); err == nil {
				exe = p
			}
			return exe, nil
		},
		Env:    os.LookupEnv,
		Docker: runDocker,
		Sleep:  sleep,
		Now:    time.Now,
		Rand:   rand.Reader,
		Prog:   args.Prog(),
		GOOS:   runtime.GOOS,
	}
}

func runDocker(ctx context.Context, dir string, stdout, stderr io.Writer,
	argv ...string) error {
	cmd := exec.CommandContext(ctx, "docker", argv...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// interruptible is a context Ctrl+C ends.
func interruptible() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

// Init is `lvp init`.
func Init(argv []string, std args.Std) int {
	ctx, stop := interruptible()
	defer stop()
	return OS().Init(ctx, argv, std)
}

// Check is `lvp check`.
func Check(argv []string, std args.Std) int {
	ctx, stop := interruptible()
	defer stop()
	return OS().Check(ctx, argv, std)
}

// Wait is `lvp wait`.
func Wait(argv []string, std args.Std) int {
	ctx, stop := interruptible()
	defer stop()
	return OS().Wait(ctx, argv, std)
}

// Test is `lvp test`.
func Test(argv []string, std args.Std) int {
	ctx, stop := interruptible()
	defer stop()
	return OS().Test(ctx, argv, std)
}

// Mint is `lvp mint`.
func Mint(argv []string, std args.Std) int {
	ctx, stop := interruptible()
	defer stop()
	return OS().Mint(ctx, argv, std)
}

// Revoke is `lvp revoke`.
func Revoke(argv []string, std args.Std) int {
	ctx, stop := interruptible()
	defer stop()
	return OS().Revoke(ctx, argv, std)
}

// Env is `lvp env`.
func Env(argv []string, std args.Std) int {
	return OS().EnvCmd(context.Background(), argv, std)
}

// Practice is `lvp game practice`.
func Practice(argv []string, std args.Std) int {
	ctx, stop := interruptible()
	defer stop()
	return OS().Practice(ctx, argv, std)
}

// folder is the proxy folder: the one with compose.yaml.
func (s *System) folder() (string, error) {
	wd, _ := s.Getwd()
	exeDir := ""
	if p, err := s.Exe(); err == nil {
		exeDir = filepath.Dir(p)
	}
	return kit.DirFrom(wd, exeDir)
}

// seatsFile finds and reads seats.env as mint-key does: SEATS_ENV,
// else this folder, else the program's folder.
func (s *System) seatsFile() (*seats.File, error) {
	wd, _ := s.Getwd()
	exeDir := ""
	if p, err := s.Exe(); err == nil {
		exeDir = filepath.Dir(p)
	}
	path, err := seats.Find(s.Env, wd, exeDir)
	if err != nil {
		return nil, err
	}
	return seats.Load(path, s.Env)
}

// defaultProxy is where the proxy is when seats.env does not say.
const defaultProxy = "http://localhost:4000"

// proxyURL is PROXY_URL from seats.env, or the usual address; a
// missing or unreadable seats.env gives the usual address too.
func (s *System) proxyURL() string {
	f, err := s.seatsFile()
	if err != nil {
		if v, _ := s.Env("PROXY_URL"); v != "" {
			return v
		}
		return defaultProxy
	}
	return f.Get("PROXY_URL", defaultProxy)
}

// randHex is n random bytes as 2n hex digits.
func (s *System) randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.Rand, b); err != nil {
		return "", errors.New("cannot make random values: " +
			err.Error())
	}
	return hex.EncodeToString(b), nil
}

// dockerError words a docker run that failed: its own first error
// line, or that docker is not there.
func dockerError(err error, stderr string) string {
	if errors.Is(err, exec.ErrNotFound) {
		return "docker is not installed, or not on PATH"
	}
	for _, l := range strings.Split(stderr, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return err.Error()
}

// dockerHint is what to do about a docker error, by its words.
func dockerHint(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "not installed"):
		return "Install Docker (README.md, Step 1)."
	case strings.Contains(m, "permission denied"):
		return "Your user may not use Docker yet (README.md, " +
			"Troubleshooting: setting up)."
	case strings.Contains(m, "cannot connect"),
		strings.Contains(m, "failed to connect"),
		strings.Contains(m, "is the docker daemon running"):
		return "Start Docker, or Docker Desktop (README.md, " +
			"Troubleshooting: setting up)."
	case strings.Contains(m, "not a docker command"),
		strings.Contains(m, "unknown shorthand flag"):
		return "This Docker has no Compose (README.md, " +
			"Troubleshooting: setting up)."
	}
	return "See README.md, Troubleshooting: setting up."
}

// wrap folds text at 80 columns: first after lead, the rest after as
// many spaces as lead is long.
func wrap(lead, text string) string {
	return wrapPad(lead, strings.Repeat(" ", len(lead)), text)
}

// wrapPad is wrap with pad before the second line on.
func wrapPad(lead, pad, text string) string {
	var b strings.Builder
	line, n := lead, 0
	for _, w := range strings.Fields(text) {
		if n > 0 && len(line)+1+len(w) > 80 {
			b.WriteString(line + "\n")
			line, n = pad, 0
		}
		if n > 0 {
			line += " "
		}
		line += w
		n++
	}
	b.WriteString(strings.TrimRight(line, " ") + "\n")
	return b.String()
}

// say prints text folded at 80 columns.
func say(w io.Writer, text string) { io.WriteString(w, wrap("", text)) }

// fail prints text folded, on stderr, and gives exit code 1.
func fail(std args.Std, text string) int {
	io.WriteString(std.Err, wrap("", text))
	return 1
}

// extra is the error for positionals a command takes none of.
func extra(cmd, usage string, pos []string) error {
	return &args.UsageError{Cmd: cmd, Usage: usage,
		Msg: "unrecognized arguments: " + strings.Join(pos, " ")}
}

// usageError is a mistake in how the command was typed: exit 2.
func usageError(std args.Std, cmd, usage, msg string) int {
	fmt.Fprint(std.Err, "usage: "+usage+"\n"+
		wrapPad(cmd+": error: ", "  ", msg))
	return 2
}
