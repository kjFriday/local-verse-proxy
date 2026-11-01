package cli

import (
	"bytes"
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
	if strings.Contains(usage(), "  init ") {
		t.Error("help lists C5's helpers before they are ready")
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
	for _, a := range [][]string{{"bogus"}, {"init"}, {"help", "nope"},
		{"Desk"}} {
		o, e, c := lvp(a...)
		if c != 2 || o != "" || !strings.Contains(e, "no command") ||
			!strings.Contains(e, " help\n") {
			t.Errorf("%q: %q %q %d", a, o, e, c)
		}
	}
}

func TestStubs(t *testing.T) {
	// Until their chunks land, each says so and fails; never panics.
	// Asked for help, each says the same on stdout, and exits 0.
	for _, a := range [][]string{{"desk"}, {"game", "board"},
		{"game", "practice", "on"}, {"fake-ollama"}} {
		o, e, c := lvp(a...)
		if c != 1 || o != "" || !strings.Contains(e, "not built yet") {
			t.Errorf("%q: %q %q %d", a, o, e, c)
		}
		for _, line := range strings.Split(e, "\n") {
			if len(line) > 80 {
				t.Errorf("%q: a line over 80: %q", a, line)
			}
		}
		o, e, c = lvp(a[0], "--help")
		if c != 0 || e != "" || !strings.Contains(o, "not built yet") {
			t.Errorf("%s --help: %q %q %d", a[0], o, e, c)
		}
	}
}
