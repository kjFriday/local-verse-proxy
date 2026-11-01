package args

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func positive(text string) (int, error) {
	n, err := strconv.Atoi(text)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s: use 1 or more", quote(text))
	}
	return n, nil
}

func set() (*Set, *bool, *int, *string, *[]string) {
	s := New("lvp game run", "lvp game run [-d N | --days N] [-q]")
	q := s.Bool("quiet", "q", "day summaries only")
	d := s.Int("days", "d", "N", 1, positive, "days to play (default 1)")
	r := s.String("rules", "", "FILE", "game/rules.toml", "rules file")
	h := s.List("host", "", "NAME", "a name browsers may use")
	return s, q, d, r, h
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		argv  []string
		pos   []string
		quiet bool
		days  int
		rules string
		hosts []string
	}{
		{nil, nil, false, 1, "game/rules.toml", nil},
		{[]string{"-q", "-d", "3"}, nil, true, 3, "game/rules.toml",
			nil},
		{[]string{"--days=4", "x", "--quiet"}, []string{"x"}, true, 4,
			"game/rules.toml", nil},
		{[]string{"-d5", "--rules", "r.toml"}, nil, false, 5, "r.toml",
			nil},
		{[]string{"--host", "a", "--host=b"}, nil, false, 1,
			"game/rules.toml", []string{"a", "b"}},
		{[]string{"a", "--", "-q", "--days"}, []string{"a", "-q",
			"--days"}, false, 1, "game/rules.toml", nil},
		{[]string{"--rules="}, nil, false, 1, "", nil},
		{[]string{"-5"}, []string{"-5"}, false, 1, "game/rules.toml",
			nil},
	} {
		s, q, d, r, h := set()
		pos, err := s.Parse(c.argv)
		if err != nil {
			t.Errorf("%q: %v", c.argv, err)
			continue
		}
		if !reflect.DeepEqual(pos, c.pos) || *q != c.quiet ||
			*d != c.days || *r != c.rules ||
			!reflect.DeepEqual(*h, c.hosts) && len(c.hosts) > 0 {
			t.Errorf("%q: %q %v %d %q %q", c.argv, pos, *q, *d, *r,
				*h)
		}
	}
}

func TestErrors(t *testing.T) {
	head := "usage: lvp game run [-d N | --days N] [-q]\nlvp game run: " +
		"error: "
	for _, c := range []struct {
		argv []string
		msg  string
	}{
		{[]string{"-d", "0"}, "argument -d/--days: '0': use 1 or more"},
		{[]string{"--days=x"},
			"argument -d/--days: 'x': use 1 or more"},
		{[]string{"-d"}, "argument -d/--days: expected one argument"},
		{[]string{"-d", "-q"}, "argument -d/--days: expected one " +
			"argument"},
		{[]string{"--rules"},
			"argument --rules: expected one argument"},
		{[]string{"--quiet=yes"}, "argument -q/--quiet: ignored " +
			"explicit argument 'yes'"},
		{[]string{"--bogus", "-x", "--qui"},
			"unrecognized arguments: " +
				"--bogus -x --qui"},
		{[]string{"-qd"}, "unrecognized arguments: -qd"},
	} {
		s, _, _, _, _ := set()
		_, err := s.Parse(c.argv)
		var ue *UsageError
		if !errors.As(err, &ue) || err.Error() != head+c.msg {
			t.Errorf("%q: %v\nwant %s", c.argv, err, head+c.msg)
		}
	}
	s := New("lvp desk", "lvp desk [--port N]")
	s.Int("port", "", "N", 8040, nil, "port")
	if _, err := s.Parse([]string{"--port", "x"}); err == nil ||
		!strings.HasSuffix(err.Error(), "argument --port: invalid int "+
			"value: 'x'") {
		t.Errorf("int: %v", err)
	}
}

func TestHelpAndExit(t *testing.T) {
	s, _, _, _, _ := set()
	_, err := s.Parse([]string{"x", "--help"})
	if err != ErrHelp {
		t.Fatalf("--help: %v", err)
	}
	var out, errs bytes.Buffer
	std := Std{Out: &out, Err: &errs}
	if Exit(std, s, err) != 0 || !strings.HasPrefix(out.String(),
		"usage: lvp game run") || errs.Len() != 0 {
		t.Fatalf("help: %q %q", out.String(), errs.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if len(line) > 79 {
			t.Errorf("help line too long: %q", line)
		}
	}
	if !strings.Contains(out.String(), "-d N, --days N") {
		t.Errorf("help: %s", out.String())
	}
	out.Reset()
	_, err = s.Parse([]string{"-z"})
	if Exit(std, s, err) != 2 || !strings.HasSuffix(errs.String(),
		"unrecognized arguments: -z\n") {
		t.Fatalf("usage: %q", errs.String())
	}
	errs.Reset()
	if Exit(std, s, errors.New("boom")) != 1 || errs.String() != "boom\n" {
		t.Fatal("plain error")
	}
	if Exit(std, s, nil) != 0 {
		t.Fatal("nil")
	}
}

func TestProgFor(t *testing.T) {
	for _, c := range []struct {
		goos, exe, wd string
		onPath        bool
		want          string
	}{
		{"linux", "/home/u/lvp/lvp", "/home/u/lvp", false, "./lvp"},
		{"linux", "/home/u/lvp/lvp", "/home/u/lvp/", true, "./lvp"},
		{"linux", "/usr/local/bin/lvp", "/home/u", true, "lvp"},
		{"linux", "/opt/lvp/lvp", "/home/u", false, "/opt/lvp/lvp"},
		{"darwin", "/Users/a b/lvp/lvp", "/tmp", false,
			"'/Users/a b/lvp/lvp'"},
		{"darwin", "/Users/it's/lvp", "/tmp", false,
			`'/Users/it'\''s/lvp'`},
		{"windows", `C:\lvp\lvp.exe`, `c:\LVP`, false, `.\lvp`},
		{"windows", `C:\lvp\lvp.EXE`, `D:\`, true, "lvp"},
		{"windows", `C:\Program Files\lvp\lvp.exe`, `D:\`, false,
			`"C:\Program Files\lvp\lvp.exe"`},
		{"windows", `C:\lvp.exe`, `C:\`, false, `.\lvp`},
		{"linux", "/lvp", "/", false, "./lvp"},
	} {
		if got := ProgFor(c.goos, c.exe, c.wd,
			c.onPath); got != c.want {
			t.Errorf("%s %s in %s: %s, want %s", c.goos, c.exe,
				c.wd,
				got, c.want)
		}
	}
	if Prog() == "" {
		t.Fatal("Prog is empty")
	}
}
