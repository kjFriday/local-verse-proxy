package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/fsx"
)

// healthy answers docker as a running setup does.
func healthy(a []string) (string, string, error) {
	switch strings.Join(a, " ") {
	case "version --format {{.Server.Version}}":
		return "27.3.1\n", "", nil
	case "compose ps --all --format json":
		return `{"Service":"db","State":"running","Health":"healthy"}` +
			"\n" + `{"Service":"proxy","State":"running",` +
			`"Health":""}` + "\n", "", nil
	}
	return "", "unknown command", exit1
}

func TestCheckAllGood(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	ol := ollama(t)
	w.seats(p.URL, "OLLAMA_API_BASE="+ol.URL+"\n")
	w.answer = healthy
	o, e, c := w.run(w.sys.Check)
	if c != 0 || e != "" {
		t.Fatalf("%d\n%s%s", c, o, e)
	}
	for _, want := range []string{
		"ok    proxy folder: " + w.dir,
		"ok    seats.env is filled in.",
		"ok    seats.env is yours only.",
		"ok    Docker runs (version 27.3.1).",
		"ok    db (Postgres) runs, and is healthy.",
		"ok    proxy (LiteLLM) runs.",
		"ok    the proxy answers at " + p.URL + ".",
		"ok    the proxy takes the master key in seats.env.",
		"ok    Ollama answers at " + ol.URL + ".",
		"Everything checks out.",
	} {
		if !strings.Contains(o, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, o)
		}
	}
	if strings.Contains(o, masterKey) {
		t.Error("printed the master key")
	}
	// The master key went to the proxy, and only in the header.
	for _, r := range p.Requests() {
		if strings.Contains(r.Path, masterKey) {
			t.Errorf("master key in %s", r.Path)
		}
	}
}

func TestCheckProblems(t *testing.T) {
	w := newWorld(t)
	// The template, with no address that could answer or hang.
	tmpl := strings.NewReplacer("http://localhost:4000",
		"http://127.0.0.1:9", exampleOllama, "http://127.0.0.1:9").
		Replace(w.read("seats.env.example"))
	write(t, filepath.Join(w.dir, "seats.env"), tmpl)
	os.Chmod(filepath.Join(w.dir, "seats.env"), 0o644)
	w.answer = func(a []string) (string, string, error) {
		return "", "", notFound
	}
	o, e, c := w.run(w.sys.Check)
	if c != 1 || e != "" {
		t.Fatalf("%d\n%s%s", c, o, e)
	}
	for _, want := range []string{
		"FAIL seats.env still has placeholders: line 8 " +
			"(LITELLM_MASTER_KEY), line 12 (LITELLM_SALT_KEY), " +
			"line 19 (POSTGRES_PASSWORD), line 20 (DATABASE_URL).",
		"Fix: ./lvp init fills them (README.md, 3a. The three " +
			"secrets).",
		"FAIL Docker: docker is not installed, or not on PATH",
		"Fix: Install Docker (README.md, Step 1).",
		"skip docker compose ps: Docker does not run.",
		"FAIL the proxy at http://127.0.0.1:9 does not answer",
		"skip the master key: the proxy does not answer.",
		"check(s) failed.",
	} {
		if !strings.Contains(flat(o), want) {
			t.Errorf("missing %q in:\n%s", want, o)
		}
	}
	if runtime.GOOS != "windows" && !strings.Contains(flat(o),
		"FAIL others may open seats.env") {
		t.Errorf("privacy not flagged:\n%s", o)
	}
	// A template with the example Ollama: a warning only.
	if !strings.Contains(o, "warn  Ollama does not answer") {
		t.Errorf("no Ollama warning:\n%s", o)
	}
}

func TestCheckNoFolder(t *testing.T) {
	w := newWorld(t)
	os.Remove(filepath.Join(w.dir, "compose.yaml"))
	o, _, c := w.run(w.sys.Check)
	if c != 1 || !strings.Contains(o, "FAIL  cannot find compose.yaml") {
		t.Errorf("%d %q", c, o)
	}
	if len(w.dockerCalls()) != 0 {
		t.Error("went on")
	}
}

func TestCheckNoSeats(t *testing.T) {
	w := newWorld(t)
	w.answer = healthy
	o, _, c := w.run(w.sys.Check)
	if c != 1 || !strings.Contains(flat(o), "FAIL cannot read "+
		"seats.env") || !strings.Contains(flat(o), "./lvp init makes "+
		"it (README.md, Step 3).") ||
		!strings.Contains(o, "skip  Ollama: seats.env is not ready.") {
		t.Errorf("%d\n%s", c, o)
	}
}

func TestCheckCompose(t *testing.T) {
	for _, tc := range []struct{ ps, want string }{
		{`[{"Service":"db","State":"running","Health":"starting"},` +
			`{"Service":"proxy","State":"restarting"}]`,
			"FAIL db (Postgres) is still starting. Fix: wait " +
				"a minute, then check again. FAIL proxy " +
				"(LiteLLM) keeps restarting. Fix: docker " +
				"compose logs proxy"},
		{"", "FAIL db (Postgres) is not running. Fix: docker " +
			"compose up -d (README.md, Step 4). FAIL proxy " +
			"(LiteLLM) is not running."},
		{`{"Service":"db","State":"exited","Health":""}` + "\n" +
			`{"Service":"proxy","State":"exited","Health":""}`,
			"FAIL db (Postgres) is not running. Fix: docker " +
				"compose up -d (README.md, Step 4). FAIL " +
				"proxy (LiteLLM) is exited."},
		{"not json", "docker compose ps: its answer is not what " +
			"was expected."},
	} {
		w := newWorld(t)
		p := fake(t)
		w.seats(p.URL)
		w.answer = func(a []string) (string, string, error) {
			if a[0] == "compose" {
				return tc.ps, "", nil
			}
			return healthy(a)
		}
		o, _, c := w.run(w.sys.Check)
		if c != 1 || !strings.Contains(flat(o), tc.want) {
			t.Errorf("%q: %d\n%s", tc.ps, c, o)
		}
	}
	// Compose's own error, with its hint.
	w := newWorld(t)
	w.answer = func(a []string) (string, string, error) {
		if a[0] == "compose" {
			return "", "permission denied while trying to " +
				"connect to the Docker daemon socket\n", exit1
		}
		return healthy(a)
	}
	o, _, _ := w.run(w.sys.Check)
	if !strings.Contains(flat(o), "FAIL docker compose ps: permission "+
		"denied") || !strings.Contains(flat(o), "Your user may not "+
		"use Docker yet") {
		t.Errorf("\n%s", o)
	}
}

func TestCheckMasterKey(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	text := strings.Replace(w.read("seats.env"), masterKey,
		"sk-test-other-key-0002", 1)
	os.WriteFile(filepath.Join(w.dir, "seats.env"), []byte(text), 0o600)
	fsx.Private(filepath.Join(w.dir, "seats.env"))
	w.answer = healthy
	o, _, c := w.run(w.sys.Check)
	if c != 1 || !strings.Contains(flat(o), "FAIL the proxy does not "+
		"take the master key in seats.env") ||
		!strings.Contains(flat(o), "docker compose up -d "+
			"--force-recreate proxy") ||
		strings.Contains(o, "sk-test-other-key-0002") {
		t.Errorf("%d\n%s", c, o)
	}
}

// In practice mode the proxy has its own model server: no Ollama
// check.
func TestCheckPractice(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL, "OLLAMA_API_BASE=http://127.0.0.1:9\n")
	write(t, filepath.Join(w.dir, ".env"), practiceMark+"\n"+
		practiceSep+"\n"+practiceFile+"\n")
	w.answer = healthy
	o, _, c := w.run(w.sys.Check)
	if c != 0 || !strings.Contains(o, "skip  Ollama: practice mode "+
		"is on (./lvp game practice off ends it).") {
		t.Errorf("%d\n%s", c, o)
	}
}

func TestCheckUsage(t *testing.T) {
	w := newWorld(t)
	if _, e, c := w.run(w.sys.Check, "now"); c != 2 ||
		!strings.Contains(e, "unrecognized arguments: now") {
		t.Errorf("%d %q", c, e)
	}
}
