package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPracticeOnOff(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	write(t, filepath.Join(w.dir, ".env"), "LITELLM_TAG=v1\n")
	o, e, c := w.run(w.sys.Practice, "on")
	if c != 0 || e != "" {
		t.Fatalf("%d\n%s%s", c, o, e)
	}
	on := "LITELLM_TAG=v1\n" + practiceMark + "\n" + practiceSep +
		"\n" + practiceFile + "\n"
	if w.read(".env") != on {
		t.Errorf("%q", w.read(".env"))
	}
	if d := w.dockerCalls(); strings.Join(d, ";") !=
		"compose up -d --force-recreate proxy;compose ps" {
		t.Errorf("docker: %q", d)
	}
	if !strings.Contains(o, "The proxy is up.\n") ||
		!strings.Contains(o, "Next: ./lvp game setup") {
		t.Errorf("%q", o)
	}
	// Again: no line twice.
	o, _, c = w.run(w.sys.Practice, "on")
	if c != 0 || w.read(".env") != on ||
		!strings.Contains(o, "on in .env already") {
		t.Errorf("%d %q %q", c, o, w.read(".env"))
	}

	o, e, c = w.run(w.sys.Practice, "off")
	if c != 0 || e != "" || w.read(".env") != "LITELLM_TAG=v1\n" {
		t.Errorf("%d %q %q %q", c, o, e, w.read(".env"))
	}
	if d := w.dockerCalls(); d[len(d)-1] !=
		"compose up -d --force-recreate --remove-orphans proxy" {
		t.Errorf("docker: %q", d)
	}
	// Off when off: nothing to do, Docker left alone.
	n := len(w.dockerCalls())
	o, _, c = w.run(w.sys.Practice, "off")
	if c != 0 || !strings.Contains(o, "nothing to do") ||
		len(w.dockerCalls()) != n {
		t.Errorf("%d %q", c, o)
	}
}

func TestPracticeRefuses(t *testing.T) {
	w := newWorld(t)
	envFile := filepath.Join(w.dir, ".env")
	write(t, envFile, "COMPOSE_FILE=compose.yaml:mine.yaml\n")
	_, e, c := w.run(w.sys.Practice, "on")
	if c != 1 || !strings.Contains(flat(e), "COMPOSE_FILE in .env is "+
		"set already (to compose.yaml:mine.yaml)") ||
		w.read(".env") != "COMPOSE_FILE=compose.yaml:mine.yaml\n" {
		t.Errorf("%d %q", c, e)
	}
	write(t, envFile, "COMPOSE_PATH_SEPARATOR=;\n")
	if _, e, c := w.run(w.sys.Practice, "on"); c != 1 ||
		!strings.Contains(e, "COMPOSE_PATH_SEPARATOR") {
		t.Errorf("%d %q", c, e)
	}
	write(t, envFile, "")
	w.env["COMPOSE_FILE"] = "x.yaml"
	if _, e, c := w.run(w.sys.Practice, "on"); c != 1 ||
		!strings.Contains(flat(e), "set in this terminal") {
		t.Errorf("%d %q", c, e)
	}
	// Turned on by hand, the old README's way: off leaves it.
	delete(w.env, "COMPOSE_FILE")
	write(t, envFile, "COMPOSE_FILE='"+practiceFiles+"'\n")
	if _, e, c := w.run(w.sys.Practice, "off"); c != 1 ||
		!strings.Contains(flat(e), "by hand") {
		t.Errorf("%d %q", c, e)
	}
	if len(w.dockerCalls()) != 0 {
		t.Error("ran docker")
	}
	for _, a := range [][]string{{}, {"maybe"}, {"on", "off"}} {
		if _, e, c := w.run(w.sys.Practice, a...); c != 2 ||
			!strings.HasPrefix(e,
				"usage: ./lvp game practice on | off") {
			t.Errorf("%q: %d %q", a, c, e)
		}
	}
}

func TestPracticeDockerFails(t *testing.T) {
	w := newWorld(t)
	w.answer = func(a []string) (string, string, error) {
		return "", "", notFound
	}
	_, e, c := w.run(w.sys.Practice, "on")
	if c != 1 || !strings.Contains(flat(e), "docker is not installed") ||
		!strings.Contains(e, "Install Docker") {
		t.Errorf("%d %q", c, e)
	}
}

// Through a real docker run: pgread's fake docker on PATH logs each
// run's arguments and folder.
func TestPracticeFakeDocker(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	dir := t.TempDir()
	script := filepath.Join(dir, "script")
	log := filepath.Join(dir, "log")
	write(t, script, "ok {}\n")
	t.Setenv("PATH", fakeDockerDir+string(os.PathListSeparator)+
		os.Getenv("PATH"))
	t.Setenv("FAKEDOCKER_SCRIPT", script)
	t.Setenv("FAKEDOCKER_LOG", log)
	w.sys.Docker = runDocker
	o, e, c := w.run(w.sys.Practice, "on")
	if c != 0 {
		t.Fatalf("%d\n%s%s", c, o, e)
	}
	for n, want := range []string{
		"compose up -d --force-recreate proxy", "compose ps"} {
		b, err := os.ReadFile(filepath.Join(log,
			string(rune('0'+n))+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var run struct {
			Argv []string
			Dir  string
		}
		json.Unmarshal(b, &run)
		if strings.Join(run.Argv, " ") != want ||
			!sameDir(t, run.Dir, w.dir) {
			t.Errorf("run %d: %+v", n, run)
		}
	}
	// A docker that will not start.
	write(t, script, "startfail Cannot connect to the Docker daemon "+
		"at unix:///var/run/docker.sock. Is the docker daemon "+
		"running?\n")
	os.Remove(script + ".n")
	o, _, c = w.run(w.sys.Check)
	if c != 1 || !strings.Contains(flat(o), "FAIL Docker: Cannot "+
		"connect to the Docker daemon") || !strings.Contains(flat(o),
		"Fix: Start Docker, or Docker Desktop") {
		t.Errorf("%d\n%s", c, o)
	}
}

func sameDir(t *testing.T, a, b string) bool {
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}
