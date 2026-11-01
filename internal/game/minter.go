package game

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/fsx"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// The game mints and revokes through a child mint-key (by default
// this program, as "lvp mint-key"), in the proxy folder, with the
// environment passed through, so the master key stays in that child.

// runMint runs mint-key with argv. Text is read as Python's text mode
// does: \r\n and \r become \n.
func (g *game) runMint(argv ...string) (stdout, stderr string,
	code int, err error) {
	full := append(append([]string{}, g.mintArgv[1:]...), argv...)
	cmd := exec.CommandContext(g.ctx, g.mintArgv[0], full...)
	cmd.Dir = g.root
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	if g.ctx.Err() != nil {
		return "", "", 0, errStopped
	}
	var exit *exec.ExitError
	switch {
	case errors.As(runErr, &exit):
		code = exit.ExitCode()
	case runErr != nil:
		return "", "", 0, die("cannot run mint-key: " +
			pyfmt.StrError(runErr))
	}
	return lines(out.String()), lines(errb.String()), code, nil
}

func lines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// mint is office.py's mint(): a key for alias with the rules' models,
// daily budget, life and refill, saved owner-only in game/keys.
// Returns "" or why it failed.
func (g *game) mint(alias string) (string, error) {
	r := g.rules
	usd := float64(r.Mana.Daily.F * r.Mana.USD.F)
	stdout, stderr, code, err := g.runMint(alias,
		"--models", strings.Join(r.Office.Models, ","),
		"--budget", money(usd),
		"--duration", r.Office.KeyLife,
		"--budget-duration", r.Day.Length)
	if err != nil {
		return "", err
	}
	key := pyfmt.Strip(stdout)
	if code != 0 || !strings.HasPrefix(key, "sk-") {
		if why := pyfmt.Strip(stderr); why != "" {
			return why, nil
		}
		return "mint-key gave no key", nil
	}
	if err := os.MkdirAll(g.keys, 0o700); err != nil {
		return pyfmt.OSErrorText(err, g.keys), nil
	}
	if err := fsx.Private(g.keys); err != nil {
		return pyfmt.OSErrorText(err, g.keys), nil
	}
	path := filepath.Join(g.keys, alias+".key")
	if err := fsx.WriteFile(path, []byte(key+"\n"), true); err != nil {
		return pyfmt.OSErrorText(err, path), nil
	}
	return "", nil
}

// revoke is office.py's revoke(): "revoked", "no key", or why it
// failed. The key file goes only once the proxy has no key for the
// seat.
func (g *game) revoke(alias string) (string, error) {
	_, stderr, code, err := g.runMint("--revoke-seat", alias)
	if err != nil {
		return "", err
	}
	why := strings.Join(pyfmt.Fields(stderr), " ")
	result := ""
	switch {
	case code == 0:
		result = "revoked"
	case strings.Contains(why, "No keys found"):
		result = "no key" // LiteLLM's 404 for an unknown seat
	case why != "":
		return why, nil
	default:
		return fmt.Sprintf("mint-key exit %d", code), nil
	}
	path := filepath.Join(g.keys, alias+".key")
	if err := fsx.Remove(path); err != nil {
		return pyfmt.OSErrorText(err, path), nil
	}
	return result, nil
}
