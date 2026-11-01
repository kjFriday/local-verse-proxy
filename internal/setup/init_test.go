package setup

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/kjFriday/local-verse-proxy/internal/fsx"
)

var (
	keyRE = regexp.MustCompile(`^sk-[0-9a-f]{48}$`)
	pwRE  = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// lines is NAME -> value of each setting line.
func settings(text string) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n",
		"\n"), "\n") {
		l = strings.TrimPrefix(l, bom)
		if n := name(l); n != "" {
			out[n] = value(l)
		}
	}
	return out
}

func TestInitFromTemplate(t *testing.T) {
	w := newWorld(t)
	o, e, c := w.run(w.sys.Init)
	if c != 0 || e != "" {
		t.Fatalf("%d %q %q", c, o, e)
	}
	text := w.read("seats.env")
	v := settings(text)
	if !keyRE.MatchString(v["LITELLM_MASTER_KEY"]) ||
		!keyRE.MatchString(v["LITELLM_SALT_KEY"]) ||
		v["LITELLM_MASTER_KEY"] == v["LITELLM_SALT_KEY"] {
		t.Errorf("keys: %q %q", v["LITELLM_MASTER_KEY"],
			v["LITELLM_SALT_KEY"])
	}
	pw := v["POSTGRES_PASSWORD"]
	if !pwRE.MatchString(pw) || v["DATABASE_URL"] !=
		"postgresql://litellm:"+pw+"@db:5432/litellm" {
		t.Errorf("password: %q %q", pw, v["DATABASE_URL"])
	}
	if strings.Contains(text, "change-me") {
		t.Error("a placeholder is left")
	}
	// Everything else as in the template.
	tmpl := w.read("seats.env.example")
	if strings.Count(tmpl, "\n") != strings.Count(text, "\n") ||
		!strings.Contains(text, "OLLAMA_API_BASE="+exampleOllama) {
		t.Errorf("not the template's lines:\n%s", text)
	}
	for _, s := range []string{v["LITELLM_MASTER_KEY"],
		v["LITELLM_SALT_KEY"], pw} {
		if strings.Contains(o+e, s) || strings.Contains(o,
			s[len(s)-6:]) {
			t.Errorf("printed a secret: %q", o)
		}
	}
	if ok, why, err := fsx.IsPrivate(filepath.Join(w.dir,
		"seats.env")); !ok || err != nil {
		t.Errorf("not private: %s %v", why, err)
	}
	for _, want := range []string{"Made seats.env",
		"LITELLM_MASTER_KEY, LITELLM_SALT_KEY, POSTGRES_PASSWORD, " +
			"DATABASE_URL", "Next: ./lvp init --ollama "} {
		if !strings.Contains(strings.Join(strings.Fields(o), " "),
			want) {
			t.Errorf("missing %q in:\n%s", want, o)
		}
	}
	// A volume check ran, as the database password was set.
	if d := w.dockerCalls(); len(d) != 1 || d[0] !=
		"volume ls -q --filter name="+Volume {
		t.Errorf("docker: %q", d)
	}

	// A second run changes nothing, and asks Docker nothing.
	o, e, c = w.run(w.sys.Init)
	if c != 0 || e != "" || w.read("seats.env") != text ||
		!strings.Contains(o, "nothing changed") {
		t.Errorf("second run: %d %q %q", c, o, e)
	}
	if d := w.dockerCalls(); len(d) != 1 {
		t.Errorf("second run asked docker: %q", d)
	}
}

// The sed rules of README 3a, in order, on lines a person may have
// changed: only exact placeholders; every other change-me gets the
// one password.
func TestInitRules(t *testing.T) {
	w := newWorld(t)
	in := "LITELLM_MASTER_KEY=sk-test-kept-0001\n" +
		"LITELLM_SALT_KEY=sk-change-me\n" +
		"# keep: change-me\n" +
		"FOO=change-me/change-me\n" +
		"LITELLM_MASTER_KEY= \n"
	write(t, filepath.Join(w.dir, "seats.env"), in)
	os.Chmod(filepath.Join(w.dir, "seats.env"), 0o600)
	o, e, c := w.run(w.sys.Init)
	if c != 0 {
		t.Fatalf("%d %q %q", c, o, e)
	}
	got := strings.Split(w.read("seats.env"), "\n")
	salt := strings.TrimPrefix(got[1], "LITELLM_SALT_KEY=")
	pw := strings.TrimPrefix(got[2], "# keep: ")
	if got[0] != "LITELLM_MASTER_KEY=sk-test-kept-0001" ||
		!keyRE.MatchString(salt) || !pwRE.MatchString(pw) ||
		got[3] != "FOO="+pw+"/"+pw ||
		got[4] != "LITELLM_MASTER_KEY= " {
		t.Errorf("got:\n%s", strings.Join(got, "\n"))
	}
	if !strings.Contains(flat(o), "LITELLM_SALT_KEY, line 3, FOO") {
		t.Errorf("names: %q", o)
	}
	// No database line changed: Docker was not asked.
	if d := w.dockerCalls(); len(d) != 0 {
		t.Errorf("docker: %q", d)
	}
}

func TestInitKeepsCRLFAndBOM(t *testing.T) {
	w := newWorld(t)
	tmpl := strings.ReplaceAll(w.read("seats.env.example"), "\n",
		"\r\n")
	write(t, filepath.Join(w.dir, "seats.env"), bom+tmpl)
	if _, _, c := w.run(w.sys.Init, "--desk-password"); c != 0 {
		t.Fatal(c)
	}
	got := w.read("seats.env")
	if !strings.HasPrefix(got, bom+"#") ||
		strings.Count(got, "\r\n") != strings.Count(got, "\n") ||
		strings.Contains(got, "change-me") {
		t.Errorf("got %q", got)
	}
	if !regexp.MustCompile(`\r\nFRONTDESK_PASSWORD=[0-9a-f]{24}\r\n$`).
		MatchString(got) {
		t.Errorf("desk password: %q", got[len(got)-60:])
	}
	if !keyRE.MatchString(settings(got)["LITELLM_MASTER_KEY"]) {
		t.Errorf("master key not filled behind a BOM")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(w.dir, "seats.env"))
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", st.Mode())
		}
	}
}

// Postgres keeps the password of its first start: init will not
// write a new one while the volume is there, unless --force.
func TestInitVolume(t *testing.T) {
	w := newWorld(t)
	w.answer = func(a []string) (string, string, error) {
		return "other_pgdata\n" + Volume + "\n", "", nil
	}
	o, e, c := w.run(w.sys.Init)
	if c != 1 || o != "" || !strings.Contains(e, Volume) ||
		!strings.Contains(e, "nothing was changed") ||
		!strings.Contains(e, "./lvp init --force") {
		t.Errorf("%d %q %q", c, o, e)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "seats.env")); err == nil {
		t.Error("seats.env made anyway")
	}
	o, e, c = w.run(w.sys.Init, "--force")
	if c != 0 || !strings.Contains(o, "--force: if Postgres") ||
		strings.Contains(w.read("seats.env"), "change-me") {
		t.Errorf("--force: %d %q %q", c, o, e)
	}

	// Docker not there: a warning, and init goes on.
	w2 := newWorld(t)
	w2.answer = func(a []string) (string, string, error) {
		return "", "", notFound
	}
	o, e, c = w2.run(w2.sys.Init)
	if c != 0 || !strings.Contains(flat(e), "warning: could not "+
		"ask Docker") || !strings.Contains(flat(e),
		"docker is not installed") {
		t.Errorf("no docker: %d %q %q", c, o, e)
	}
}

func TestInitOllama(t *testing.T) {
	w := newWorld(t)
	ol := ollama(t)
	o, e, c := w.run(w.sys.Init, "--ollama", ol.URL+"/")
	// The stand-in is on 127.0.0.1, so init warns about that only.
	if c != 0 || strings.Contains(e, "did not answer") ||
		!strings.Contains(e, "warning: inside the proxy's") {
		t.Fatalf("%d %q %q", c, o, e)
	}
	if v := settings(w.read("seats.env"))["OLLAMA_API_BASE"]; v !=
		ol.URL {
		t.Errorf("OLLAMA_API_BASE=%q", v)
	}
	if !strings.Contains(o, "Ollama answers there.") ||
		!strings.Contains(o, "Next: docker compose up -d, then "+
			"./lvp wait") {
		t.Errorf("%q", o)
	}
	// Set again to something else: only that line changes.
	before := w.read("seats.env")
	ol.Close()
	o, e, c = w.run(w.sys.Init, "--ollama=http://127.0.0.1:9")
	after := w.read("seats.env")
	if c != 0 || !strings.Contains(e, "warning: inside the proxy's") ||
		!strings.Contains(e, "warning: Ollama did not answer") ||
		strings.Replace(before, ol.URL, "http://127.0.0.1:9", 1) !=
			after {
		t.Errorf("%d %q %q", c, o, e)
	}
	for _, bad := range []string{"http://x/$(id)", "ftp://x",
		"http://a b", "http://x;y"} {
		_, e, c := w.run(w.sys.Init, "--ollama", bad)
		if c != 2 || !strings.Contains(e, "argument --ollama") {
			t.Errorf("%q: %d %q", bad, c, e)
		}
	}
}

func TestInitDeskPassword(t *testing.T) {
	w := newWorld(t)
	w.run(w.sys.Init, "--desk-password")
	pw := settings(w.read("seats.env"))["FRONTDESK_PASSWORD"]
	if !regexp.MustCompile(`^[0-9a-f]{24}$`).MatchString(pw) {
		t.Fatalf("%q", pw)
	}
	o, _, c := w.run(w.sys.Init, "--desk-password")
	if c != 0 || settings(w.read("seats.env"))["FRONTDESK_PASSWORD"] !=
		pw || !strings.Contains(o, "already in seats.env") ||
		strings.Contains(o, pw) {
		t.Errorf("%d %q", c, o)
	}
}

func TestInitMistakes(t *testing.T) {
	w := newWorld(t)
	if _, e, c := w.run(w.sys.Init, "extra"); c != 2 ||
		!strings.Contains(e, "unrecognized arguments: extra") {
		t.Errorf("%d %q", c, e)
	}
	if o, _, c := w.run(w.sys.Init, "--help"); c != 0 ||
		!strings.HasPrefix(o, "usage: ./lvp init") {
		t.Errorf("%d %q", c, o)
	}
	os.Remove(filepath.Join(w.dir, "compose.yaml"))
	if _, e, c := w.run(w.sys.Init); c != 1 ||
		!strings.Contains(e, "cannot find compose.yaml") {
		t.Errorf("%d %q", c, e)
	}
	w = newWorld(t)
	os.Remove(filepath.Join(w.dir, "seats.env.example"))
	if _, e, c := w.run(w.sys.Init); c != 1 ||
		!strings.Contains(e, "seats.env.example") {
		t.Errorf("%d %q", c, e)
	}
	write(t, filepath.Join(w.dir, "seats.env"), "\xff\xfeL\x00")
	if _, e, c := w.run(w.sys.Init); c != 1 ||
		!strings.Contains(e, "UTF-16") {
		t.Errorf("%d %q", c, e)
	}
}

// init makes an existing seats.env private, without other changes.
func TestInitMakesPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes: Linux and macOS")
	}
	w := newWorld(t)
	w.seats("http://localhost:4000")
	p := filepath.Join(w.dir, "seats.env")
	os.Chmod(p, 0o644)
	o, _, c := w.run(w.sys.Init)
	st, _ := os.Stat(p)
	if c != 0 || st.Mode().Perm() != 0o600 ||
		!strings.Contains(o, "now only you may") {
		t.Errorf("%d %q %v", c, o, st.Mode())
	}
}
