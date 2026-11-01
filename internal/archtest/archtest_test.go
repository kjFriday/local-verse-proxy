package archtest

import (
	"bufio"
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

const module = "github.com/kjFriday/local-verse-proxy"

// root is the repo: the folder with go.mod, above this one.
func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			t.Fatal("no go.mod above this folder")
		}
		dir = up
	}
}

// allowed is section 5.3: what each package may import from this
// module. A key covers its folder and everything under it, unless a
// longer key is more exact.
var allowed = map[string][]string{
	"cmd/lvp":      {"internal/cli"},
	"cmd/mint-key": {"internal/mintkey"},
	"internal/cli": {"internal/frontdesk", "internal/game",
		"internal/fakeollama", "internal/mintkey", "internal/setup",
		"internal/args"},
	"internal/frontdesk": {"internal/frontdesk", "internal/mintkey",
		"internal/seats", "internal/litellm", "internal/pgread",
		"internal/rules", "internal/gamestate", "internal/kit",
		"internal/args", "internal/clock", "internal/fsx",
		"internal/pyfmt", "gui"},
	"internal/game": {"internal/game", "internal/rules",
		"internal/gamestate", "internal/pgread", "internal/kit",
		"internal/args", "internal/clock", "internal/fsx",
		"internal/pyfmt", "internal/pyrand"},
	"internal/fakeollama": {"internal/fakeollama", "internal/args",
		"internal/pyfmt"},
	"internal/setup": {"internal/setup", "internal/mintkey",
		"internal/seats", "internal/litellm", "internal/pgread",
		"internal/kit", "internal/args", "internal/clock",
		"internal/fsx", "internal/pyfmt"},
	"internal/mintkey":   {"internal/seats", "internal/litellm"},
	"internal/pgread":    {"internal/clock", "internal/pyfmt"},
	"internal/rules":     {"internal/pyfmt"},
	"internal/gamestate": {"internal/fsx"},
	// Leaves.
	"internal/args":                {},
	"internal/kit":                 {},
	"internal/clock":               {},
	"internal/fsx":                 {},
	"internal/pyfmt":               {},
	"internal/pyrand":              {},
	"internal/seats":               {},
	"internal/litellm":             {},
	"internal/litellm/fakelitellm": {},
	"internal/pgread/fakedocker":   {},
	"internal/archtest":            {},
	"internal/notices":             {},
	"gui":                          {},
}

// testOnly may be imported from _test.go files only.
var testOnly = []string{"internal/litellm/fakelitellm",
	"internal/pgread/fakedocker"}

func rule(dir string) ([]string, bool) {
	best, ok := "", false
	for k := range allowed {
		if (dir == k || strings.HasPrefix(dir, k+"/")) &&
			len(k) >= len(best) {
			best, ok = k, true
		}
	}
	return allowed[best], ok
}

func within(p string, list []string) bool {
	for _, a := range list {
		if p == a || strings.HasPrefix(p, a+"/") {
			return true
		}
	}
	return false
}

func TestImports(t *testing.T) {
	top := root(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(top, func(path string, d fs.DirEntry,
		err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(top, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		isTest := strings.HasSuffix(path, "_test.go")
		ok, known := rule(dir)
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !strings.HasPrefix(p, module+"/") {
				continue
			}
			p = strings.TrimPrefix(p, module+"/")
			switch {
			case within(p, testOnly) && !isTest:
				t.Errorf("%s imports %s, which only tests may "+
					"import", rel, p)
			case within(p, testOnly):
			case isTest:
				// Tests may use what they test with.
			case !known:
				t.Errorf("%s: package %s is not in the import "+
					"rules (internal/archtest)", rel, dir)
			case !within(p, ok):
				t.Errorf("%s imports %s: section 5.3 does not "+
					"allow it", rel, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// skipDir: version control, recorded material, and test data.
func skipDir(rel string) bool {
	base := filepath.Base(rel)
	return base == ".git" || base == "testdata" ||
		base == "node_modules" || rel == "tests/reference" ||
		base == "__pycache__" || base == "dist"
}

// textFiles is every file under the repo that is text, but those skip
// names.
func textFiles(t *testing.T, top string,
	skip func(rel string) bool) map[string][]byte {
	out := map[string][]byte{}
	err := filepath.WalkDir(top, func(path string, d fs.DirEntry,
		err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(top, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && skip(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || skip(rel) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
			return nil // not text
		}
		out[rel] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ignored are files git ignores that may sit in a working copy.
func ignored(rel string) bool {
	base := filepath.Base(rel)
	return strings.HasPrefix(base, "seats.env") &&
		base != "seats.env.example" || base == ".env" ||
		strings.HasSuffix(base, ".key") ||
		strings.HasPrefix(rel, "game/office.db") ||
		rel == "lvp" || rel == "lvp.exe" || rel == "mint-key" ||
		rel == "mint-key.exe"
}

// Width is the 80-column rule, tabs to the next multiple of 8.
func width(line string) int {
	n := 0
	for _, r := range line {
		if r == '\t' {
			n += 8 - n%8
		} else {
			n++
		}
	}
	return n
}

// generated are files a program writes, not a person: the goldens
// that tests/golden/*.py capture, and LiteLLM's answers as recorded.
// Their lines may be long. Every other file of the port is held to 80
// columns, testdata too.
func generated(rel string) bool {
	return strings.Contains(rel, "/testdata/golden/") ||
		rel == "internal/litellm/fakelitellm/testdata/captures.json"
}

func TestWidths(t *testing.T) {
	top := root(t)
	// go.mod and go.sum are the go command's: it writes their lines.
	// The recorded material is kept as it was; its README is ours.
	files := textFiles(t, top, func(rel string) bool {
		base := filepath.Base(rel)
		return base == ".git" || base == "node_modules" ||
			base == "__pycache__" || base == "dist" ||
			ignored(rel) || rel == "go.mod" || rel == "go.sum" ||
			strings.HasPrefix(rel+"/", "tests/reference/") &&
				rel != "tests/reference/README.md" &&
				rel != "tests/reference" ||
			generated(rel)
	})
	names := sortedNames(files)
	for _, rel := range names {
		sc := bufio.NewScanner(bytes.NewReader(files[rel]))
		sc.Buffer(nil, 1<<20)
		for n := 1; sc.Scan(); n++ {
			line := strings.TrimRight(sc.Text(), "\r")
			if w := width(line); w > 80 {
				t.Errorf("%s:%d: %d columns (80 at most)",
					rel, n, w)
			}
		}
	}
}

// invisible is a character a reader cannot see, or can take for a
// plain space: white space but the space, line and paragraph
// separators, format and control characters but tab and line ends.
func invisible(r rune) bool {
	switch r {
	case '\t', '\n', '\r', ' ':
		return false
	}
	return unicode.In(r, unicode.Zs, unicode.Zl, unicode.Zp, unicode.Cf,
		unicode.Cc, unicode.Co) || r == utf8.RuneError
}

// Go source and the golden scripts write such characters as escapes
// (\u00a0), so an editor or a copy cannot turn them into spaces.
func TestNoInvisibleCharacters(t *testing.T) {
	top := root(t)
	files := textFiles(t, top, func(rel string) bool {
		base := filepath.Base(rel)
		return base == ".git" || base == "testdata" || ignored(rel) ||
			rel == "tests/reference"
	})
	for _, rel := range sortedNames(files) {
		if !strings.HasSuffix(rel, ".go") &&
			!strings.HasPrefix(rel, "tests/golden/") {
			continue
		}
		lines := strings.Split(string(files[rel]), "\n")
		for n, line := range lines {
			for _, r := range strings.TrimSuffix(line, "\r") {
				if !invisible(r) {
					continue
				}
				t.Errorf("%s:%d: the invisible character "+
					"U+%04X; write it as an escape", rel,
					n+1, r)
			}
		}
	}
}

func sortedNames(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// modelRE finds names of AI models: none may ship in a file, but
// config.yaml, whose models are the user's choice (spec 23.4). Each
// name is split by a one-letter class, so this file names none.
var modelRE = regexp.MustCompile(`(?i)\b(` +
	`c[l]aude-[0-9a-z]|(op[u]s|son[n]et|hai[k]u)-[0-9]|` +
	`g[p]t-[0-9]|chatg[p]t|o[1-4]-(m[i]ni|pre[v]iew)|` +
	`gem[i]ni-[0-9]|gem[m]a[0-9]|l[l]ama[-_]?[0-9]|code[l]lama|` +
	`mis[t]ral-|mix[t]ral|q[w]en[0-9]|ph[i][0-9]|deep[s]eek-|` +
	`comm[a]nd-r)`)

var modelAllowed = map[string]bool{"config.yaml": true}

func TestModelNames(t *testing.T) {
	top := root(t)
	files := textFiles(t, top, func(rel string) bool {
		base := filepath.Base(rel)
		return base == ".git" || ignored(rel)
	})
	for _, rel := range sortedNames(files) {
		if modelAllowed[rel] {
			continue
		}
		for n, line := range strings.Split(string(files[rel]), "\n") {
			if m := modelRE.FindString(line); m != "" {
				t.Errorf("%s:%d: names a model (%s)", rel, n+1,
					m)
			}
		}
	}
}

// secret is one of the forms section 16.0 forbids: a key, a Front
// Desk password (make_password's letters: no i, l or o; ledger B-17),
// a long hex run (a key, hash or token).
type secret struct {
	kind string
	re   *regexp.Regexp
}

var secretKinds = []secret{
	{"an sk- key", regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)},
	{"a Front Desk password", regexp.MustCompile(
		`[a-hjkmnp-z2-9]{4}(-[a-hjkmnp-z2-9]{4}){3}`)},
	{"a long hex run (a key, hash or token)",
		regexp.MustCompile(`[0-9A-Fa-f]{32,}`)},
}

// madeUp marks a key the tests made up, by a word no random key has.
var madeUp = regexp.MustCompile(`(?i)fake|test|example|dummy|master|shim`)

// notSecret is a match that is not a secret: a key a test made up; a
// run of decimal digits (a number: a golden's 1e300 as f); a pinned
// hash, after "sha256:" (an image or a package), "DIGEST=" (an
// image's ARG) or "@" (an action's commit).
func notSecret(b []byte, at []int, kind int) bool {
	m := b[at[0]:at[1]]
	switch kind {
	case 0:
		return madeUp.Match(m)
	case 2:
		before := string(b[max(0, at[0]-7):at[0]])
		return allDigits(m) || strings.HasSuffix(before, "sha256:") ||
			strings.HasSuffix(before, "DIGEST=") ||
			strings.HasSuffix(before, "@")
	}
	return false
}

// secretHits are the secrets in one file, as messages that say where
// and what, and never show any of the secret.
func secretHits(rel string, b []byte) []string {
	var out []string
	for i, k := range secretKinds {
		for _, at := range k.re.FindAllIndex(b, -1) {
			if notSecret(b, at, i) {
				continue
			}
			line := 1 + bytes.Count(b[:at[0]], []byte("\n"))
			out = append(out, fmt.Sprintf("%s:%d: looks like %s "+
				"(%d characters)", rel, line, k.kind,
				at[1]-at[0]))
		}
	}
	return out
}

func allDigits(b []byte) bool {
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Every file in the repo, text or not (an office.db can hold a key),
// but those git ignores (seats.env, keys, builds, Python's caches)
// and the version control's own.
func TestSecretScan(t *testing.T) {
	top := root(t)
	n := 0
	err := filepath.WalkDir(top, func(path string, d fs.DirEntry,
		err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(top, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch filepath.Base(rel) {
			case ".git", "__pycache__", "dist":
				return filepath.SkipDir // never committed
			}
			return nil
		}
		if !d.Type().IsRegular() || ignored(rel) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		for _, msg := range secretHits(rel, b) {
			t.Error(msg)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 100 {
		t.Fatalf("scanned %d files; the walk is wrong", n)
	}
}

// The scan finds what it must, and its messages show at most 4
// characters of what it found.
func TestSecretScanMessages(t *testing.T) {
	// Made up here, so no file holds them.
	key := "sk-" + strings.Repeat("Zq8Wv3", 8)
	pw := strings.Join([]string{"abcd", "efgh", "jkmn", "pqrs"}, "-")
	hex := strings.Repeat("9f3a", 8)
	for _, c := range []struct{ text, secret string }{
		{"MASTER=" + key + "\n", key},
		{"x\npassword pw_" + pw + " here", pw},
		{"pass 2" + pw, pw},
		{`"\\n` + pw + `"`, pw},
		{"postgresql://u:" + hex + "@db/x", hex},
	} {
		msgs := secretHits("f.txt", []byte(c.text))
		if len(msgs) == 0 {
			t.Errorf("not found: %q", c.text)
		}
		for _, m := range msgs {
			for i := 0; i+5 <= len(c.secret); i++ {
				if strings.Contains(m, c.secret[i:i+5]) {
					t.Errorf("the message shows it: %s", m)
					break
				}
			}
		}
	}
	for _, ok := range []string{
		"uses: actions/checkout@" + strings.Repeat("ab12", 10),
		"FROM golang@sha256:" + strings.Repeat("cd34", 16),
		"x = " + strings.Repeat("1234567890", 4),
		"MASTER=sk-test-master-" + strings.Repeat("0", 20),
		"seat x390x844-dark-copy-refused",
	} {
		if msgs := secretHits("f.txt", []byte(ok)); len(msgs) > 0 {
			t.Errorf("flagged: %q: %v", ok, msgs)
		}
	}
}
