package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
)

func lvp(argv ...string) (string, string, int) {
	var out, errs bytes.Buffer
	code := run(argv, "v9.9.9", args.Std{In: strings.NewReader(""),
		Out: &out, Err: &errs})
	return out.String(), errs.String(), code
}

func TestHelpAndVersion(t *testing.T) {
	o, e, c := lvp()
	if c != 2 || o != "" || !strings.HasPrefix(e, "lvp: ") {
		t.Errorf("no arguments: %q %q %d", o, e, c)
	}
	for _, a := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		o, e, c := lvp(a...)
		if c != 0 || e != "" || o != usage() {
			t.Errorf("%q: %q %q %d", a, o, e, c)
		}
	}
	for _, line := range strings.Split(usage(), "\n") {
		if len(line) > 80 {
			t.Errorf("help line over 80: %q", line)
		}
	}
	for _, name := range []string{"desk", "game", "mint-key",
		"fake-ollama", "version", "help"} {
		if !strings.Contains(usage(), "\n  "+name+" ") {
			t.Errorf("help does not list %s", name)
		}
	}
	for _, name := range []string{"init", "check", "wait", "test",
		"mint", "revoke", "env"} {
		if !strings.Contains(usage(), "\n  "+name+" ") {
			t.Errorf("help does not list the helper %s", name)
		}
	}
	for _, a := range []string{"version", "--version"} {
		if o, e, c := lvp(a); o != "lvp v9.9.9\n" || e != "" || c != 0 {
			t.Errorf("%s: %q %q %d", a, o, e, c)
		}
	}
	if o, _, c := lvp("mint-key", "--version"); o != "mint-key v9.9.9\n" ||
		c != 0 {
		t.Errorf("mint-key --version: %q %d", o, c)
	}
	o, e, c = lvp("help", "mint-key")
	if c != 0 || e != "" || !strings.HasPrefix(o, "Mint a scoped key") ||
		!strings.Contains(o, "  "+args.Prog()+" mint-key <seat>") ||
		!strings.Contains(o, "  "+args.Prog()+" mint-key --revoke ") ||
		strings.Contains(o, "Type ./mint-key") {
		t.Errorf("help mint-key: %q %q %d", o, e, c)
	}
	for _, name := range []string{"version", "help", "desk", "game",
		"fake-ollama"} {
		o, e, c := lvp("help", name)
		if c != 0 || e != "" || o == "" {
			t.Errorf("help %s: %q %q %d", name, o, e, c)
		}
	}
}

// lvp mint-key's usage in lvp's words, laid out as mint-key's.
func TestMintKeyUsageFor(t *testing.T) {
	for _, prog := range []string{"./lvp", `.\lvp`, "lvp"} {
		u := mintkey.UsageFor(prog + " mint-key")
		lines := strings.Split(u, "\n")
		if strings.Index(lines[4], "[--budget-duration") !=
			strings.Index(lines[3], "[--models") {
			t.Errorf("%s: not aligned:\n%s", prog, u)
		}
		for _, l := range lines {
			if len(l) > 80 {
				t.Errorf("%s: over 80: %q", prog, l)
			}
		}
	}
	long := mintkey.UsageFor(`"C:\Program Files\lvp\lvp.exe" mint-key`)
	for _, l := range strings.Split(long, "\n")[4:] {
		if len(l) > 80 {
			t.Errorf("long path: over 80: %q", l)
		}
	}
}

func TestUnknown(t *testing.T) {
	for _, a := range [][]string{{"bogus"}, {"Init"}, {"help", "nope"},
		{"Desk"}} {
		o, e, c := lvp(a...)
		if c != 2 || o != "" || !strings.Contains(e, "no command") ||
			!strings.Contains(e, " help\n") {
			t.Errorf("%q: %q %q %d", a, o, e, c)
		}
	}
}

func TestCommandHelp(t *testing.T) {
	// desk, game and fake-ollama are built: asked for help, each
	// prints it on stdout and exits 0, and never starts serving.
	for _, a := range [][]string{{"desk", "--help"},
		{"game", "--help"}, {"fake-ollama", "--help"},
		{"init", "--help"}, {"check", "--help"}, {"wait", "--help"},
		{"test", "--help"}, {"mint", "--help"}, {"revoke", "--help"},
		{"env", "--help"}, {"game", "practice", "--help"},
		{"help", "mint"}, {"help", "init"}} {
		o, e, c := lvp(a...)
		if c != 0 || e != "" || o == "" ||
			strings.Contains(o, "not built yet") {
			t.Errorf("%q: %q %q %d", a, o, e, c)
		}
		// The usage names the program as it was run; here that is
		// the test binary's long path, so measure with lvp instead.
		o = strings.ReplaceAll(o, args.Prog(), "lvp")
		for _, line := range strings.Split(o, "\n") {
			if len(line) > 80 {
				t.Errorf("%q: a line over 80: %q", a, line)
			}
		}
	}
}

// TestMintFlagHint: one of mint-key's options typed straight after
// lvp gets the line with mint-key put in.
func TestMintFlagHint(t *testing.T) {
	_, e, c := lvp("--revoke-seat", "test2")
	want := "Did you mean: " + args.Prog() +
		" mint-key --revoke-seat test2\n"
	if c != 2 || !strings.HasSuffix(e, want) {
		t.Errorf("%q %d", e, c)
	}
	_, e, _ = lvp("--models", "a b")
	if !strings.HasSuffix(e, " mint-key --models ...\n") {
		t.Errorf("spaces: %q", e)
	}
	_, e, _ = lvp("bogus")
	if strings.Contains(e, "Did you mean") {
		t.Errorf("bogus: %q", e)
	}
}

// TestMintKeyUsageNamesLvp: after a mistake, lvp mint-key shows its
// usage as lvp types it, not the mint-key program's.
func TestMintKeyUsageNamesLvp(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, "seats.env")
	os.WriteFile(env, []byte("LITELLM_MASTER_KEY=sk-test-usage-0001\n"+
		"PROXY_URL=http://127.0.0.1:9\n"), 0o600)
	t.Setenv("SEATS_ENV", env)
	_, e, c := lvp("mint-key", "test2", "--revoke-seat", "test2")
	if c != 2 || e != mintkey.UsageFor(args.Prog()+" mint-key") ||
		strings.Contains(e, "mint-key.exe") {
		t.Errorf("%d %q", c, e)
	}
}
