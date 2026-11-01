package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/litellm/fakelitellm"
	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
)

const master = "sk-shim-test-master-0123456789"

// build makes the shim and lvp, with one version stamped in both.
func build(t *testing.T) (shim, lvp string) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds two programs")
	}
	dir := t.TempDir()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	shim = filepath.Join(dir, "mint-key"+ext)
	lvp = filepath.Join(dir, "lvp"+ext)
	for out, pkg := range map[string]string{shim: ".", lvp: "../lvp"} {
		cmd := exec.Command("go", "build", "-ldflags",
			"-X main.version=v9.9.9", "-o", out, pkg)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", pkg, err, b)
		}
	}
	return shim, lvp
}

type result struct {
	stdout, stderr string
	code           int
}

func run(t *testing.T, env []string, prog string, args ...string) result {
	t.Helper()
	cmd := exec.Command(prog, args...)
	cmd.Env = env
	cmd.Dir = t.TempDir()
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{out.String(), errs.String(), code}
}

// The shim prints exactly what `lvp mint-key` prints.
func TestShimEqualsLvp(t *testing.T) {
	shim, lvp := build(t)
	fake := fakelitellm.Start(fakelitellm.Options{MasterKey: master,
		Models: []string{"local"}})
	defer fake.Close()
	env := filepath.Join(t.TempDir(), "seats.env")
	os.WriteFile(env, []byte("LITELLM_MASTER_KEY="+master+
		"\nLITELLM_SALT_KEY=sk-salt-1234\nPROXY_URL="+fake.URL+"\n"),
		0o600)
	environ := append(os.Environ(), "SEATS_ENV="+env)
	for _, args := range [][]string{{}, {"--version"}, {"-x"},
		{"--revoke-seat", "nobody"}, {"seat", "--budget", "x"},
		{"seat", "--models", ""}} {
		a := run(t, environ, shim, args...)
		b := run(t, environ, lvp, append([]string{"mint-key"},
			args...)...)
		// After a mistake the shim shows its own usage; lvp shows
		// the same, naming the command as lvp mint-key.
		if a.stderr == mintkey.Usage() {
			named := strings.Contains(b.stderr,
				" mint-key --revoke-seat <seat>\n")
			if b.code != a.code || b.stdout != a.stdout ||
				!named || strings.Contains(b.stderr, ".exe") {
				t.Errorf("%q usage:\n shim %+v\n lvp  %+v",
					args, a, b)
			}
			continue
		}
		if a != b {
			t.Errorf("%q:\n shim %+v\n lvp  %+v", args, a, b)
		}
	}
	if a := run(t, environ, shim, "--version"); a.stdout !=
		"mint-key v9.9.9\n" {
		t.Errorf("version: %+v", a)
	}
	// A mint each: the same request, a key printed the same way.
	key := regexp.MustCompile(`^sk-[A-Za-z0-9_-]{8,200}\n$`)
	a := run(t, environ, shim, "seat-a", "--budget", "2.50")
	b := run(t, environ, lvp, "mint-key", "seat-b", "--budget", "2.50")
	if !key.MatchString(a.stdout) || !key.MatchString(b.stdout) ||
		a.stderr != "" || b.stderr != "" || a.code != 0 || b.code != 0 {
		t.Fatalf("mint:\n shim %+v\n lvp  %+v", a, b)
	}
	reqs := fake.Requests()
	var bodies []string
	for _, r := range reqs {
		if r.Path != "/key/generate" {
			continue
		}
		b := strings.Replace(string(r.Body), "seat-b", "seat-a", 2)
		bodies = append(bodies, b+" "+r.Header.Get("User-Agent"))
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatalf("requests differ: %q", bodies)
	}
	var body map[string]any
	json.Unmarshal(reqs[len(reqs)-1].Body, &body)
	if body["max_budget"] != 2.5 {
		t.Fatalf("body %v", body)
	}
}
