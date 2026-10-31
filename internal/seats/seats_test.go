package seats

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func envOf(m map[string]string) LookupFunc {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

var testEnv = map[string]string{"HOME": "/home/sam", "EMPTY": ""}

// Files bash reads the same way. TestParseLikeBash checks that.
var goodCases = []struct {
	name string
	text string
	want map[string]string
}{
	{"plain", "A=1\nB=two\n", map[string]string{"A": "1", "B": "two"}},
	{"template", "# c\n\n  # indented comment\nLITELLM_MASTER_KEY=\n" +
		"DATABASE_URL=postgresql://litellm:pw@db:5432/litellm\n",
		map[string]string{"LITELLM_MASTER_KEY": "",
			"DATABASE_URL": "postgresql://litellm:pw@db:5432/" +
				"litellm"}},
	{"export", "export A=1\nexport\tB=2\n  export  C=3",
		map[string]string{"A": "1", "B": "2", "C": "3"}},
	{"single quotes", `A='a b # c $HOME "x"'`,
		map[string]string{"A": `a b # c $HOME "x"`}},
	{"double quotes", `A="a b # c 'x' \$ \" \\ \n $HOME"`,
		map[string]string{"A": `a b # c 'x' $ " \ \n /home/sam`}},
	{"mixed word", `A=one"two"'three'four`,
		map[string]string{"A": "onetwothreefour"}},
	{"comment after", "A=1 # note\nB='x'\t# note\nC=  # empty\n",
		map[string]string{"A": "1", "B": "x", "C": ""}},
	{"hash in word", "A=a#b\nB=#x", map[string]string{"A": "a#b",
		"B": "#x"}},
	{"symbols", "A=a=b*c?[d]{e,f}!g%h^i@j+k.l/m,n:o~p",
		map[string]string{"A": "a=b*c?[d]{e,f}!g%h^i@j+k.l/m,n:o~p"}},
	{"backslash", `A=a\ b\$c\\d`, map[string]string{"A": `a b$c\d`}},
	{"expand", "U=litellm\nA=$U:${U}@$HOME/x\nB=\"${U}s\"\nC=$EMPTY.",
		map[string]string{"U": "litellm",
			"A": "litellm:litellm@/home/sam/x", "B": "litellms",
			"C": "."}},
	{"lone dollar", "A=a$\nB=\"$\"\nC=\"$ x\"\nD=$/\nE=\"a$'\"",
		map[string]string{"A": "a$", "B": "$", "C": "$ x",
			"D": "$/", "E": "a$'"}},
	{"later wins", "A=1\nA=2\nB=$A", map[string]string{"A": "2",
		"B": "2"}},
	{"empty quotes", `A=""` + "\nB=''", map[string]string{"A": "",
		"B": ""}},
	{"name chars", "_a1_B=1\nexport=2\nexportX=3",
		map[string]string{"_a1_B": "1", "export": "2",
			"exportX": "3"}},
	// Plain settings are made one by one; export's all at the end.
	{"several", "A=1 B=$A C='x y'\t# c\nD=0\nexport D=2 E=$D",
		map[string]string{"A": "1", "B": "1", "C": "x y", "D": "2",
			"E": "0"}},
	{"bare export", "X=5\nexport X\nexport X NEVER_SET # c\n" +
		"export X Y=$X", map[string]string{"X": "5", "Y": "5"}},
}

func TestParse(t *testing.T) {
	for _, c := range goodCases {
		got, err := Parse(c.text, envOf(testEnv))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestParseWindowsFile(t *testing.T) {
	got, err := Parse("\ufeffA=1\r\nB='two'\r\n# c\r\n\r\nC=\"3\"",
		nil)
	want := map[string]string{"A": "1", "B": "two", "C": "3"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

// TestParseLikeBash sources each good case with bash, under set -u
// as mint-key.sh does, and checks bash got the same values.
func TestParseLikeBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("no bash here")
	}
	dir := t.TempDir()
	for _, c := range goodCases {
		f := filepath.Join(dir, "seats.env")
		if err := os.WriteFile(f, []byte(c.text), 0o600); err != nil {
			t.Fatal(err)
		}
		var names []string
		for k := range c.want {
			names = append(names, k)
		}
		sort.Strings(names)
		script := "set -u; source \"$1\"; shift; " +
			"for n in \"$@\"; do " +
			"printf '%s=%s\\0' \"$n\" \"${!n}\"; done"
		cmd := exec.Command(bash, append([]string{"-c", script, "x", f},
			names...)...)
		cmd.Env = []string{"HOME=/home/sam", "EMPTY=",
			"PATH=/usr/bin:/bin"}
		out, err := cmd.Output()
		if err != nil {
			t.Errorf("%s: bash: %v", c.name, err)
			continue
		}
		got := map[string]string{}
		for _, kv := range strings.Split(strings.TrimSuffix(string(out),
			"\x00"), "\x00") {
			k, v, _ := strings.Cut(kv, "=")
			got[k] = v
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: bash read\n %q\nnot %q", c.name, got,
				c.want)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	cases := []struct {
		text string
		line int
		msg  string
	}{
		{"A=$(id)", 1, "$(...) would run a command"},
		{"A=\"x$(id)\"", 1, "$(...) would run a command"},
		{"# ok\nA=`id`", 2, "backticks"},
		{"A=\"`id`\"", 1, "backticks"},
		{"A=1; B=2", 1, "';' is shell syntax"},
		{"A=x&", 1, "'&' is shell syntax"},
		{"A=x|y", 1, "'|' is shell syntax"},
		{"A=<x", 1, "'<' is shell syntax"},
		{"A=(a b)", 1, "'(' is shell syntax"},
		{"A=two words", 1, "more after the value"},
		{"A=1 B=2 three", 1, "more after the value"},
		{"export A=1 B", 1, "more after the value"},
		{"export A B=1 C", 1, "more after the value"},
		{"A=1 B", 1, "more after the value"},
		{"export A#x", 1, "not a NAME=value line"},
		{"A", 1, "not a NAME=value line"},
		{"A= x", 1, "more after the value"},
		{"A='open", 1, "' quote is not closed"},
		{"A=\"open", 1, "\" quote is not closed"},
		{"A=\"open\\", 1, "\" quote is not closed"},
		{"A=x\\", 1, "joins lines"},
		{"\n\nA=$NOPE", 3, "a $ here starts the name of a setting " +
			"that is not set"},
		{"A=${NOPE}", 1, "not set"},
		{"A=${HOME:-x}", 1, "only ${NAME}"},
		{"A=${HOME", 1, "only ${NAME}"},
		{"A=${}", 1, "only ${NAME}"},
		{"A=$1", 1, "$1 means something special"},
		{"A=\"$$\"", 1, "$$ means something special"},
		{"A=$'x'", 1, "$' means something special"},
		{"A=$[1]", 1, "$[ means something special"},
		{"A=~/x", 1, "a ~ here"},
		{"A=x:~/y", 1, "a ~ here"},
		{"echo hi", 1, "not a NAME=value line"},
		{"export", 1, "not a NAME=value line"},
		{"export -n A=1", 1, "not a NAME=value line"},
		{"A =1", 1, "not a NAME=value line"},
		{"1A=1", 1, "not a NAME=value line"},
		{"A+=1", 1, "not a NAME=value line"},
		{"A[0]=1", 1, "not a NAME=value line"},
		{"source other.env", 1, "not a NAME=value line"},
	}
	for _, c := range cases {
		_, err := Parse(c.text, envOf(testEnv))
		se, ok := err.(*SyntaxError)
		if !ok {
			t.Errorf("%q: got %v, want a SyntaxError", c.text, err)
			continue
		}
		if se.Line != c.line || !strings.Contains(se.Msg, c.msg) {
			t.Errorf("%q: got line %d %q; want line %d %q", c.text,
				se.Line, se.Msg, c.line, c.msg)
		}
	}
}

func TestParseErrorHidesValues(t *testing.T) {
	for _, text := range []string{
		"LITELLM_MASTER_KEY=sk-secret$(id)",
		"FRONTDESK_PASSWORD=Kq7$secret2wLr4v",
		"FRONTDESK_PASSWORD=Kq7${secret}",
		"FRONTDESK_PASSWORD=plum secret 8x kite",
	} {
		_, err := Parse(text, nil)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("%q: got %v", text, err)
		}
	}
}

func TestParseUTF16(t *testing.T) {
	utf16 := func(bom bool, s string) string {
		var b []byte
		if bom {
			b = append(b, 0xff, 0xfe)
		}
		for i := 0; i < len(s); i++ {
			b = append(b, s[i], 0)
		}
		return string(b)
	}
	for _, text := range []string{
		utf16(true, "A=1\r\n"), utf16(false, "A=1\r\n"),
		"\xfe\xff\x00A\x00=\x001", "A=1\x00",
	} {
		if _, err := Parse(text, nil); err != ErrUTF16 {
			t.Errorf("%q: got %v, want ErrUTF16", text, err)
		}
	}
	dir := t.TempDir()
	p := write(t, dir, utf16(true, "LITELLM_MASTER_KEY=sk-1\r\n"))
	_, err := Load(p, nil)
	if err == nil || err.Error() != p+" seems to be saved as UTF-16 "+
		"(Notepad calls it Unicode). Save it as UTF-8 instead: in "+
		"Notepad, File, Save as, then Encoding: UTF-8" {
		t.Fatalf("got %v", err)
	}
}

func write(t *testing.T, dir, text string) string {
	t.Helper()
	p := filepath.Join(dir, Name)
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFind(t *testing.T) {
	wd, exe, other := t.TempDir(), t.TempDir(), t.TempDir()
	none := envOf(nil)

	_, err := Find(none, wd, exe)
	want := "missing " + filepath.Join(wd, Name) + "\n(also looked for " +
		filepath.Join(exe, Name) + ")\nMake it as README.md Step 3 " +
		"shows, or set SEATS_ENV to its path."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	_, err = Find(none, wd, wd)
	if err == nil || strings.Contains(err.Error(), "also looked") {
		t.Fatalf("same folder twice: %v", err)
	}

	pe := write(t, exe, "A=1")
	if p, err := Find(none, wd, exe); p != pe || err != nil {
		t.Fatalf("program folder: got %q, %v", p, err)
	}
	pw := write(t, wd, "A=1")
	if p, err := Find(none, wd, exe); p != pw || err != nil {
		t.Fatalf("current folder first: got %q, %v", p, err)
	}
	po := write(t, other, "A=1")
	env := envOf(map[string]string{"SEATS_ENV": po})
	if p, err := Find(env, wd, exe); p != po || err != nil {
		t.Fatalf("SEATS_ENV first: got %q, %v", p, err)
	}
	gone := filepath.Join(other, "gone.env")
	env = envOf(map[string]string{"SEATS_ENV": gone})
	if _, err := Find(env, wd, exe); err == nil ||
		err.Error() != "missing "+gone {
		t.Fatalf("SEATS_ENV missing: got %v", err)
	}
	env = envOf(map[string]string{"SEATS_ENV": other}) // a folder
	if _, err := Find(env, wd, exe); err == nil ||
		err.Error() != "missing "+other {
		t.Fatalf("SEATS_ENV a folder: got %v", err)
	}
	env = envOf(map[string]string{"SEATS_ENV": ""}) // empty: unset
	if p, err := Find(env, wd, exe); p != pw || err != nil {
		t.Fatalf("SEATS_ENV empty: got %q, %v", p, err)
	}
}

func TestLoadAndMasterKey(t *testing.T) {
	dir := t.TempDir()
	env := envOf(map[string]string{"DEFAULT_BUDGET": "9",
		"PROXY_URL": "http://env:1", "LITELLM_MASTER_KEY": "sk-env"})

	p := write(t, dir, "LITELLM_MASTER_KEY=sk-file\nPROXY_URL=\n")
	f, err := Load(p, env)
	if err != nil {
		t.Fatal(err)
	}
	if mk, err := f.MasterKey(); mk != "sk-file" || err != nil {
		t.Fatalf("got %q, %v", mk, err)
	}
	// The file's empty PROXY_URL wins over the environment's, then
	// counts as unset.
	if v := f.Get("PROXY_URL", "def"); v != "def" {
		t.Fatalf("PROXY_URL: got %q", v)
	}
	if v := f.Get("DEFAULT_BUDGET", "5"); v != "9" {
		t.Fatalf("from the environment: got %q", v)
	}

	p = write(t, dir, "PROXY_URL=x\n")
	f, _ = Load(p, env)
	if mk, err := f.MasterKey(); mk != "sk-env" || err != nil {
		t.Fatalf("master key from the environment: %q, %v", mk, err)
	}

	unset := "LITELLM_MASTER_KEY: not set in " + p
	changeMe := "replace the change-me values in " + p + " first"
	for text, want := range map[string]string{
		"LITELLM_MASTER_KEY=\n": unset,
		"# nothing\n":           unset,
		"LITELLM_MASTER_KEY=a\nLITELLM_SALT_KEY=sk-change-me": changeMe,
		"LITELLM_MASTER_KEY=sk-change-me-x":                   changeMe,
	} {
		write(t, dir, text)
		f, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.MasterKey(); err == nil || err.Error() != want {
			t.Errorf("%q: got %v, want %s", text, err, want)
		}
	}

	write(t, dir, "A=1\nB=$(id)\n")
	if _, err := Load(p, nil); err == nil ||
		err.Error() != p+" line 2: $(...) would run a command; "+
			"to keep it as is, put the value in single quotes" {
		t.Fatalf("got %v", err)
	}
	if _, err := Load(dir, nil); err == nil ||
		!strings.HasPrefix(err.Error(), "cannot read "+dir+": ") {
		t.Fatalf("a folder: got %v", err)
	}
}
