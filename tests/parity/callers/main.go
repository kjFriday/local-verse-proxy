// Command callers is the Go twin of tests/mint-parity.sh's Python
// "callers" block (spec 16.5): on a live proxy, it asks Go's Front Desk
// (lvp desk, which reads mint-key's errors in process) and Python's
// (gui/frontdesk.py, which runs mint-key.sh) the same things, with the
// same settings and the broken variants mint-parity.sh made, and
// compares what each page would show; then it plays the office game's
// minter (a child lvp mint-key, or mint-key.sh for office.py) through
// setup and reset in two throwaway proxy folders, and compares those.
//
//	go run ./tests/parity/callers <lvp> <tmp> <seats.env> <tag>
//
// <tmp> holds mint-parity.sh's settings variants (badkey, down,
// hangup, changeme). Seats are named parity-<tag>-...; all are gone at
// the end. Prints no key; exits 1 if an answer differs without a
// ledger entry that says why (F-3, O-8).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tests/parity/callers "+
			"<lvp> <tmp> <seats.env> <tag>")
		os.Exit(2)
	}
	lvp, tmp, envMain, tag := os.Args[1], os.Args[2], os.Args[3],
		os.Args[4]
	fails := 0
	fails += desk(lvp, tmp, envMain, tag)
	fails += game(lvp, tmp, envMain, tag)
	if fails > 0 {
		os.Exit(1)
	}
}

func freePort() int {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// running is one Front Desk, Python's or Go's.
type running struct {
	cmd  *exec.Cmd
	base string
	csrf string
}

func startDesk(argv []string, envFile string) (*running, error) {
	p := freePort()
	argv = append(argv, "--port", strconv.Itoa(p), "--env", envFile)
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := &running{cmd: cmd, base: fmt.Sprintf("http://127.0.0.1:%d", p)}
	for i := 0; i < 200; i++ {
		resp, err := http.Get(r.base + "/api/session")
		if err == nil {
			var s map[string]any
			json.NewDecoder(resp.Body).Decode(&s)
			resp.Body.Close()
			r.csrf, _ = s["csrf"].(string)
			// The first look at the proxy, so the cards are known.
			time.Sleep(1500 * time.Millisecond)
			return r, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.stop()
	return nil, fmt.Errorf("%s did not start", argv[0])
}

func (r *running) stop() {
	// Python's Front Desk ignores SIGINT when started from a script
	// (ledger F-20); both stop on SIGTERM.
	r.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { r.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		r.cmd.Process.Kill()
		<-done
	}
}

// post gives the answer's status and JSON.
func (r *running) post(path string, body any) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", r.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", r.csrf)
	req.Header.Set("Origin", r.base)
	c := http.Client{Timeout: 60 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()
	var j map[string]any
	json.NewDecoder(resp.Body).Decode(&j)
	return resp.StatusCode, j
}

var keyRE = regexp.MustCompile(`^sk-[A-Za-z0-9_-]{8,200}$`)

// said is one answer as the page shows it: "key" for a key, "ok", or
// the plain words of the error.
func said(st int, j map[string]any) string {
	if k, _ := j["key"].(string); st == 200 && keyRE.MatchString(k) {
		return "key"
	}
	if st == 200 {
		return "ok"
	}
	e, _ := j["error"].(string)
	return fmt.Sprintf("%d %s", st, e)
}

// steps is what both desks are asked, on one settings file: a mint, the
// same seat again, a cancel, the cancel again; on the broken settings
// files, a mint (each fails inside mint-key, so its words are the
// desk's reading of mint-key's error).
func steps(r *running, seat string, main bool) []string {
	mint := map[string]any{"seat": seat, "models": []string{"local"},
		"budget": "1", "duration": "1h"}
	if !main {
		return []string{said(r.post("/api/mint", mint))}
	}
	var out []string
	out = append(out, said(r.post("/api/mint", mint)))
	out = append(out, said(r.post("/api/mint", mint)))
	time.Sleep(500 * time.Millisecond)
	cancel := map[string]any{"seat": seat}
	out = append(out, said(r.post("/api/revoke", cancel)))
	out = append(out, said(r.post("/api/revoke", cancel)))
	return out
}

// desk runs Python's Front Desk (gui/frontdesk.py, which mints with
// mint-key.sh) and Go's (lvp desk, which mints in process) on the same
// settings and compares what the page would show.
func desk(lvp, tmp, envMain, tag string) int {
	impls := map[string][]string{
		"python": {"python3", "-B", "gui/frontdesk.py"},
		"go":     {lvp, "desk"},
	}
	type run struct{ name, env string }
	var runs []run
	runs = append(runs, run{"main", envMain})
	for _, v := range []string{"badkey", "down", "hangup", "changeme"} {
		runs = append(runs, run{v, filepath.Join(tmp, v+".env")})
	}
	got := map[string][]string{}
	for _, impl := range []string{"python", "go"} {
		for _, rn := range runs {
			// One seat name for both: the words name it. Python
			// goes first and leaves no card behind.
			seat := fmt.Sprintf("parity-%s-desk2", tag)
			r, err := startDesk(impls[impl], rn.env)
			if err != nil {
				got[impl] = append(got[impl],
					"start: "+err.Error())
				continue
			}
			for _, a := range steps(r, seat, rn.name == "main") {
				got[impl] = append(got[impl], rn.name+": "+a)
			}
			r.stop()
		}
	}
	bad := false
	fmt.Println("         Front Desk: lvp desk vs gui/frontdesk.py, " +
		"same settings")
	for i, g := range got["go"] {
		w := ""
		if i < len(got["python"]) {
			w = got["python"][i]
		}
		mark := "  "
		if w != g {
			mark = "!="
			if f3(w, g) {
				mark = "~~"
			} else {
				bad = true
			}
		}
		fmt.Printf("    %s %.74s\n", mark, g)
		if mark != "  " {
			fmt.Printf("       Python: %.66s\n", w)
		}
	}
	if bad || len(got["go"]) != len(got["python"]) {
		fmt.Println("FAIL     Front Desk (Go)")
		return 1
	}
	fmt.Println("same     Front Desk (Go)")
	return 0
}

// f3: ledger F-3. Where neither has words for mint-key's error, Go's
// page says "mint-key said:" and the Go mint-key's line, Python's
// "mint-key.sh said:" and curl's.
func f3(py, gs string) bool {
	return strings.Contains(py, "mint-key.sh said: ") &&
		strings.Contains(gs, "mint-key said: ")
}

// game plays the minter through both games' commands in two throwaway
// proxy folders: setup (mints one seat), setup again, reset (revokes
// it), reset again. The outputs must be equal but for the office id
// and the rename table (ledger O-8: Go names lvp where Python named
// `python3 game/office.py`).
func game(lvp, tmp, envMain, tag string) int {
	repo, _ := os.Getwd()
	rules := "[office]\nseat_prefix = \"parity-" + tag + "-g-\"\n" +
		"models = [\"local\"]\nkey_life = \"1h\"\n"
	src, err := os.ReadFile(filepath.Join(repo, "game", "rules.toml"))
	if err != nil {
		fmt.Println("FAIL     office game (Go):", err)
		return 1
	}
	// rules.toml with this office section and one seat.
	body := string(src)
	body = regexp.MustCompile(`(?s)\[office\].*?\n\n`).ReplaceAllString(
		body, rules+"\n")
	body = regexp.MustCompile(`(?s)\[roster\].*`).ReplaceAllString(body,
		"[roster]\nseats = [{ name = \"g1\", persona = \"frugal\", "+
			"guild = \"owls\" }]\n")
	envb, err := os.ReadFile(envMain)
	if err != nil {
		fmt.Println("FAIL     office game (Go):", err)
		return 1
	}
	folder := func(name string) string {
		d := filepath.Join(tmp, name)
		os.MkdirAll(filepath.Join(d, "game"), 0o700)
		copyFile(filepath.Join(repo, "compose.yaml"),
			filepath.Join(d, "compose.yaml"), 0o644)
		copyFile(filepath.Join(repo, "mint-key.sh"),
			filepath.Join(d, "mint-key.sh"), 0o755)
		copyFile(filepath.Join(repo, "game", "office.py"),
			filepath.Join(d, "game", "office.py"), 0o644)
		os.WriteFile(filepath.Join(d, "seats.env"), envb, 0o600)
		os.WriteFile(filepath.Join(d, "game", "rules.toml"),
			[]byte(body), 0o644)
		return d
	}
	goDir, pyDir := folder("game-go"), folder("game-py")
	cmds := [][]string{{"setup"}, {"setup"}, {"reset"}, {"reset"}}
	idRE := regexp.MustCompile(`Office [0-9a-f]{6} is open`)
	bad := false
	fmt.Println("         office game: lvp game vs office.py, setup " +
		"twice, reset twice")
	// One after the other: both mint the same seat name.
	var gos, pys []string
	for _, c := range cmds {
		gos = append(gos, runIn(goDir, lvp, append([]string{"game"},
			c...)...))
	}
	for _, c := range cmds {
		pys = append(pys, runIn(pyDir, "python3", append([]string{
			"-B", "game/office.py"}, c...)...))
	}
	for i, s := range cmds {
		a, b := gos[i], pys[i]
		norm := func(x string) string {
			x = idRE.ReplaceAllString(x, "Office <id> is open")
			x = strings.ReplaceAll(x, "python3 game/office.py",
				"<game>")
			x = strings.ReplaceAll(x, lvp+" game", "<game>")
			return x
		}
		mark := "  "
		if norm(a) != norm(b) {
			mark, bad = "!=", true
		}
		fmt.Printf("    %s %s: %.200q\n", mark, s[0], norm(a))
		if mark != "  " {
			fmt.Printf("       Python: %.200q\n", norm(b))
		}
	}
	os.RemoveAll(goDir)
	os.RemoveAll(pyDir)
	if bad {
		fmt.Println("FAIL     office game (Go)")
		return 1
	}
	fmt.Println("same     office game (Go)")
	return 0
}

func runIn(dir, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if e, ok := err.(*exec.ExitError); ok {
		code = e.ExitCode()
	}
	return fmt.Sprintf("%s[exit %d]", out.String(), code)
}

func copyFile(from, to string, mode os.FileMode) {
	in, err := os.Open(from)
	if err != nil {
		return
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return
	}
	defer out.Close()
	io.Copy(out, in)
}
