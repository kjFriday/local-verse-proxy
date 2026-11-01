package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The docs a beginner follows (Gate B): every command they show
// names a real lvp command, and every --flag on it is one that
// command takes.
var docs = []string{"README.md", "gui/README.md", "game/README.md",
	"docs/lvp-handbook.md"}

// docLine is one lvp command from a fenced block.
type docLine struct {
	file string
	line int
	args []string
}

// fenced is every line in a fenced block of the Markdown at path
// that runs lvp, as its arguments after the program's name.
func fenced(t *testing.T, path string) []docLine {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []docLine
	in := false
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "```") {
			in = !in
			continue
		}
		if !in {
			continue
		}
		l = strings.TrimPrefix(l, "$ ")
		if i := strings.Index(l, " #"); i >= 0 {
			l = l[:i] // a comment
		}
		words := strings.Fields(l)
		if len(words) > 0 && words[0] == "wsl" {
			// wsl -d Ubuntu --cd <folder> ./lvp ...
			for i, w := range words {
				if w == "./lvp" {
					words = words[i:]
					break
				}
			}
		}
		if len(words) == 0 || words[0] != "./lvp" &&
			words[0] != `.\lvp` && words[0] != "lvp" {
			continue
		}
		var a []string
		for _, w := range words[1:] {
			a = append(a, strings.Trim(w, "[]|"))
		}
		out = append(out, docLine{path, n, a})
	}
	return out
}

func TestDocsCommands(t *testing.T) {
	top := filepath.Join("..", "..")
	known := map[string]bool{"version": true, "help": true}
	for _, c := range commands {
		known[c.name] = true
	}
	helps := map[string]string{}
	help := func(argv ...string) string {
		k := strings.Join(argv, " ")
		if h, ok := helps[k]; ok {
			return h
		}
		o, _, _ := lvp(append(argv, "--help")...)
		if argv[0] == "mint-key" {
			o, _, _ = lvp("help", "mint-key")
		}
		helps[k] = o
		return o
	}
	count := 0
	for _, d := range docs {
		for _, l := range fenced(t, filepath.Join(top, d)) {
			count++
			where := d + ":" + itoa(l.line)
			if len(l.args) == 0 {
				continue // lvp alone: the short help
			}
			cmd := l.args[0]
			if !known[cmd] {
				t.Errorf("%s: no command %q", where, cmd)
				continue
			}
			texts := []string{help(cmd)}
			if sub := gameSub(cmd, l.args); sub != "" {
				texts = append(texts, help(cmd, sub))
			}
			checkFlags(t, where, l.args, strings.Join(texts, "\n"))
		}
	}
	t.Logf("%d lvp lines checked", count)
	if count < 40 {
		t.Errorf("found only %d lvp lines in the docs", count)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// gameSub is the game command a line runs (setup, run, ...), or "".
func gameSub(cmd string, a []string) string {
	if cmd != "game" {
		return ""
	}
	for _, w := range a[1:] {
		switch w {
		case "setup", "run", "board", "reset", "practice":
			return w
		}
	}
	return ""
}

// checkFlags: every --flag in a is in help.
func checkFlags(t *testing.T, where string, a []string, help string) {
	t.Helper()
	for _, w := range a[1:] {
		if !strings.HasPrefix(w, "--") || w == "--" {
			continue
		}
		name, _, _ := strings.Cut(w, "=")
		re := regexp.MustCompile(`(^|[^\w-])` +
			regexp.QuoteMeta(name) + `([^\w-]|$)`)
		if !re.MatchString(help) {
			t.Errorf("%s: lvp %s takes no %s", where,
				strings.Join(a, " "), name)
		}
	}
}
