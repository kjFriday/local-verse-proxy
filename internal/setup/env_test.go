package setup

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestEnvSet(t *testing.T) {
	w := newWorld(t)
	o, e, c := w.run(w.sys.EnvCmd, "set", "FRONTDESK_BIND=0.0.0.0")
	if c != 0 || e != "" || w.read(".env") != "FRONTDESK_BIND=0.0.0.0\n" {
		t.Fatalf("%d %q %q", c, o, e)
	}
	if o != ".env now has: FRONTDESK_BIND=0.0.0.0\nTo apply it, run: "+
		"docker compose --profile frontdesk up -d\n" {
		t.Errorf("%q", o)
	}
	// Other lines kept, as they were; the name once, where it was.
	write(t, filepath.Join(w.dir, ".env"), bom+"# mine\r\n"+
		"LITELLM_TAG=v1\r\nFRONTDESK_PORT=1\r\nX=2\r\n"+
		"FRONTDESK_PORT=3")
	o, _, c = w.run(w.sys.EnvCmd, "set", "FRONTDESK_PORT=8041")
	if got := w.read(".env"); c != 0 || got != "# mine\r\n"+
		"LITELLM_TAG=v1\r\nFRONTDESK_PORT=8041\r\nX=2\r\n" {
		t.Errorf("%d %q", c, got)
	}
	o, _, _ = w.run(w.sys.EnvCmd, "set", "LITELLM_TAG=v1.2")
	if !strings.Contains(o, "docker compose pull, then docker "+
		"compose up -d") {
		t.Errorf("%q", o)
	}
	// Values Compose would cut or fill in go in single quotes.
	w.run(w.sys.EnvCmd, "set", "A=a b#c$d")
	if !strings.Contains(w.read(".env"), "\r\nA='a b#c$d'\r\n") {
		t.Errorf("%q", w.read(".env"))
	}
	if _, e, c := w.run(w.sys.EnvCmd, "set", "B=it's a $"); c != 2 ||
		!strings.Contains(e, "cannot be written") {
		t.Errorf("%d %q", c, e)
	}
	// A secret's value is never shown.
	o, _, _ = w.run(w.sys.EnvCmd, "set", "SOME_TOKEN=s3cr3t")
	if strings.Contains(o, "s3cr3t") ||
		!strings.Contains(o, "SOME_TOKEN=(hidden)") {
		t.Errorf("%q", o)
	}
	o, _, _ = w.run(w.sys.EnvCmd)
	if strings.Contains(o, "s3cr3t") || !strings.Contains(o,
		"# mine\nLITELLM_TAG=v1.2\nFRONTDESK_PORT=8041\n") {
		t.Errorf("%q", o)
	}
}

func TestEnvUnset(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.dir, ".env"), "A=1\nB=2\nA=3\n")
	o, _, c := w.run(w.sys.EnvCmd, "unset", "A")
	if c != 0 || w.read(".env") != "B=2\n" ||
		!strings.HasPrefix(o, "Removed A from .env.\n") {
		t.Errorf("%d %q %q", c, o, w.read(".env"))
	}
	o, _, c = w.run(w.sys.EnvCmd, "unset", "A")
	if c != 0 || o != "A is not in .env; nothing changed.\n" {
		t.Errorf("%d %q", c, o)
	}
}

func TestEnvShowEmpty(t *testing.T) {
	w := newWorld(t)
	o, _, c := w.run(w.sys.EnvCmd)
	if c != 0 || !strings.Contains(o, "./lvp env set NAME=value") {
		t.Errorf("%d %q", c, o)
	}
}

// PowerShell 5.1's >> writes UTF-16: read it, write UTF-8 back.
func TestEnvUTF16(t *testing.T) {
	w := newWorld(t)
	u := utf16.Encode([]rune("LITELLM_TAG=v1\r\n"))
	b := []byte{0xff, 0xfe}
	for _, x := range u {
		b = append(b, byte(x), byte(x>>8))
	}
	write(t, filepath.Join(w.dir, ".env"), string(b))
	o, _, c := w.run(w.sys.EnvCmd, "set", "X=1")
	if c != 0 || w.read(".env") != "LITELLM_TAG=v1\r\nX=1\r\n" ||
		!strings.Contains(o, "it is UTF-8 now") {
		t.Errorf("%d %q %q", c, o, w.read(".env"))
	}
}

func TestEnvMistakes(t *testing.T) {
	w := newWorld(t)
	for _, a := range [][]string{{"set"}, {"set", "A"}, {"set", "1A=x"},
		{"unset", "A=B"}, {"put", "A=1"}, {"set", "A=1", "B=2"}} {
		if _, e, c := w.run(w.sys.EnvCmd, a...); c != 2 ||
			!strings.HasPrefix(e, "usage: ./lvp env") {
			t.Errorf("%q: %d %q", a, c, e)
		}
	}
}
