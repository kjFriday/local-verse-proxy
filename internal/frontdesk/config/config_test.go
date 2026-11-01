package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFromEnviron(t *testing.T) {
	s, err := FromEnviron([]string{
		"LITELLM_MASTER_KEY=sk-fake-env-master",
		"LITELLM_SALT_KEY=sk-change-me",
		"FRONTDESK_PASSWORD=a-fake-password-1",
		"DATABASE_URL=postgresql://u:fakepw@db/x",
		"SHORT_KEY=abc", "HOME=/home/fake-user",
		"PROXY_URL=http://proxy:4000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Docker || s.Path != "" || s.Host != "proxy" || s.Port != 4000 {
		t.Fatalf("%v", s)
	}
	want := []string{"sk-fake-env-master", "sk-change-me",
		"a-fake-password-1", "postgresql://u:fakepw@db/x"}
	if strings.Join(s.Hidden, "|") != strings.Join(want, "|") {
		t.Errorf("hidden: %d values", len(s.Hidden))
	}
	if len(s.Warnings) != 1 || s.Warnings[0] != ChangeMe {
		t.Errorf("warnings %q", s.Warnings)
	}
	if got := s.Scrub("a sk-fake-env-master b"); got != "a [hidden] b" {
		t.Errorf("scrub %q", got)
	}
}

func TestBadProxyURL(t *testing.T) {
	_, err := FromEnviron([]string{"LITELLM_MASTER_KEY=x",
		"PROXY_URL=ftp://x"})
	if err == nil || !strings.HasPrefix(err.Error(), "PROXY_URL in "+
		"the environment is not a web address") {
		t.Fatalf("%v", err)
	}
	p := filepath.Join(t.TempDir(), "seats.env")
	os.WriteFile(p, []byte("LITELLM_MASTER_KEY=x\nPROXY_URL=http://a:"+
		"99999\n"), 0o600)
	if _, err := LoadFile(p, nil); err == nil ||
		!strings.Contains(err.Error(), "has a bad port") {
		t.Fatalf("%v", err)
	}
}

func TestLoadFileErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope", "seats.env")
	if _, err := LoadFile(missing, nil); err == nil ||
		err.Error() != "missing "+missing+": do README Step 3 first" {
		t.Fatalf("%v", err)
	}
	p := filepath.Join(dir, "seats.env")
	os.WriteFile(p, []byte("A=1\nB=$(date)\n"), 0o600)
	if _, err := LoadFile(p, nil); err == nil ||
		!strings.Contains(err.Error(), "line 2") {
		t.Fatalf("%v", err)
	}
}

func TestLog(t *testing.T) {
	var b strings.Builder
	l := NewLog(&b)
	l.SetNow(func() time.Time {
		return time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	})
	l.Line("hello")
	if b.String() != "03:04:05 hello\n" {
		t.Fatalf("%q", b.String())
	}
}
