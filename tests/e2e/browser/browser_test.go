//go:build e2e

// Package browser runs the reference Playwright scripts (spec 16.5,
// gap 10) against a running Front Desk: the ones in tests/reference/
// browser, perf and security, as they were written, with node. Each
// must end with exit 0 and no "FAILED" line; where a script reports what got
// into the
// page, nothing may have; xss3_vb.js must print what was recorded for
// Python (tests/reference/security/xss4163.out).
//
//	E2E_URL=http://127.0.0.1:8023 E2E_PWFILE=fd.pass \
//	  E2E_PROXY_URL=http://localhost:4223 E2E_SEATS_ENV=seats.env \
//	  go test -tags e2e -timeout 30m ./tests/e2e/browser
//
// With E2E_PWFILE the desk asks for a password (--lan, --login or
// --docker): the scripts that log in run. Without it the desk is a
// local one: the scripts that open the page straight away run, and
// with E2E_STOP and E2E_START (shell commands that stop and start the
// desk) the restart scripts too. E2E_MODE=docker (a desk with a
// password, as --docker always has) runs them all: the scripts that
// do not log in get login.js preloaded, which logs in through
// /api/login and hands the cookie to Playwright. E2E_PROXY_URL and
// E2E_SEATS_ENV let
// the test make the hostile cards the scripts look for, straight in
// LiteLLM (hostile_mint.py and spend_hostile.py). Node must find
// playwright (NODE_PATH, or a global install).
package browser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

var ref = filepath.Join("..", "..", "reference")

type env struct {
	url, pwfile, proxy, master, shots string
	stop, start                       string
	gameSeat                          string // a game card on the page
	docker                            bool   // E2E_MODE=docker
	preload                           string // login.js, absolute
}

func setup(t *testing.T) *env {
	e := &env{url: os.Getenv("E2E_URL"), pwfile: os.Getenv("E2E_PWFILE"),
		proxy: os.Getenv("E2E_PROXY_URL"), stop: os.Getenv("E2E_STOP"),
		start: os.Getenv("E2E_START")}
	if e.url == "" {
		t.Skip("E2E_URL is not set: no Front Desk to test")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	if err := exec.Command("node", "-e",
		"require('playwright')").Run(); err != nil {
		t.Skip("node cannot find playwright")
	}
	if f := os.Getenv("E2E_SEATS_ENV"); f != "" {
		b, _ := os.ReadFile(f)
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(l,
				"LITELLM_MASTER_KEY="); ok {
				e.master = strings.TrimSpace(v)
			}
		}
	}
	e.shots = t.TempDir()
	e.docker = os.Getenv("E2E_MODE") == "docker"
	if e.docker && e.pwfile == "" {
		t.Fatal("E2E_MODE=docker needs E2E_PWFILE")
	}
	p, err := filepath.Abs("login.js")
	if err != nil {
		t.Fatal(err)
	}
	e.preload = p
	return e
}

// call asks LiteLLM straight, as an admin's other tools could.
func (e *env) call(t *testing.T, path, key string, body any) (int,
	map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.proxy+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	c := http.Client{Timeout: 60 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		t.Logf("%s: %v", path, err)
		return 0, nil
	}
	defer resp.Body.Close()
	var j map[string]any
	json.NewDecoder(resp.Body).Decode(&j)
	return resp.StatusCode, j
}

// hostile makes the cards hostile_mint.py made. A name that is there
// already ("already exists") is fine.
func (e *env) hostile(t *testing.T) {
	if e.proxy == "" || e.master == "" {
		t.Log("no E2E_PROXY_URL or master key: hostile cards not made")
		return
	}
	for _, k := range []struct {
		alias string
		extra map[string]any
	}{
		{"<img src=x onerror=alert(1)>", map[string]any{
			"max_budget": 1, "budget_duration": "1d"}},
		{"game-<svg/onload=alert(2)>", map[string]any{
			"max_budget": 0.015, "budget_duration": "1d"}},
		{`"><script>alert(3)</script>`, map[string]any{
			"max_budget": 2}},
		{"game-dbx", map[string]any{"max_budget": 1,
			"budget_duration": "1d"}},
		{"\u202egnp.exe", map[string]any{"max_budget": 1}},
	} {
		body := map[string]any{"key_alias": k.alias,
			"models": []string{"local"}, "duration": "1d"}
		for x, v := range k.extra {
			body[x] = v
		}
		st, _ := e.call(t, "/key/generate", e.master, body)
		t.Logf("hostile card %q: %d", k.alias, st)
	}
}

var hostileNames = []string{"<img src=x onerror=alert(1)>",
	"game-<svg/onload=alert(2)>", `"><script>alert(3)</script>`,
	"game-dbx", "\u202egnp.exe",
	"<b>tally-b</b><img src=x onerror=alert(9)>",
	"t-<svg/onload=alert(8)>"}

// clean deletes the hostile cards a run before left, so each run
// starts as the reference rounds did.
func (e *env) clean(t *testing.T) {
	if e.proxy == "" || e.master == "" {
		return
	}
	st, _ := e.call(t, "/key/delete", e.master,
		map[string]any{"key_aliases": hostileNames})
	t.Logf("hostile cards of an earlier run deleted: %d", st)
}

// spending makes spend_hostile.py's cards, which spend so the tally
// names them. Made after xss_vb.js, as in the reference rounds: its
// "<img src=x" card must be the only one with that text.
func (e *env) spending(t *testing.T) {
	hi := map[string]any{"model": "local", "messages": []any{
		map[string]string{"role": "user", "content": "hi"}}}
	if e.proxy == "" || e.master == "" {
		return
	}
	for _, alias := range []string{
		"<b>tally-b</b><img src=x onerror=alert(9)>",
		"t-<svg/onload=alert(8)>"} {
		st, j := e.call(t, "/key/generate", e.master, map[string]any{
			"key_alias": alias, "models": []string{"local"},
			"duration": "1d", "max_budget": 1})
		key, _ := j["key"].(string)
		t.Logf("spending card %q: %d", alias, st)
		for i := 0; i < 2 && key != ""; i++ {
			st, _ := e.call(t, "/v1/chat/completions", key,
				hi)
			t.Logf("   chat %d", st)
		}
	}
}

// evilSite serves the other site evil_vb.js and evil2_vb.js open: it
// asks Front Desk for the state and to mint, frames it, and has a
// form that posts a mint to it. (The page the scripts were run with
// was not kept; this one does what they look for.)
func evilSite(t *testing.T, fd string) string {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><title>evil</title>
<iframe src="` + fd + `/"></iframe>
<form id="fm" method="POST" action="` + fd + `/api/mint"
 enctype="text/plain"><input name='{"seat":"evil-form","x":"'
 value='"}'></form>
<script>
window.results = {};
(async () => {
  for (const [name, path, opts] of [
    ["state", "/api/state", {credentials: "include"}],
    ["mint", "/api/mint", {method: "POST", credentials: "include",
      headers: {"Content-Type": "text/plain"},
      body: JSON.stringify({seat: "evil-fetch"})}],
  ]) {
    try {
      const r = await fetch("` + fd + `" + path, opts);
      window.results[name] = "read " + r.status;
    } catch (e) { window.results[name] = "blocked"; }
  }
  window.results.done = true;
})();
</script>`
	srv := &http.Server{Handler: http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

type script struct {
	file string
	args func(e *env) []string
	pw   bool   // logs in: needs E2E_PWFILE; else needs none
	stop bool   // stops and starts the desk
	seat string // a card it needs, made first through Front Desk
	game bool   // needs a game card ("game-" seat) too
}

var scripts = []script{
	{file: "security/xss_vb.js", pw: true, args: func(e *env) []string {
		return []string{e.url, e.pwfile, e.shots}
	}},
	{file: "security/xss3_vb.js", pw: true, args: func(e *env) []string {
		return []string{e.url, e.pwfile, e.shots}
	}},
	{file: "security/xss4_vb.js", pw: true, args: func(e *env) []string {
		return []string{e.url, e.pwfile, e.shots}
	}},
	{file: "security/tally_xss.js", pw: true, args: func(e *env) []string {
		return []string{e.url, e.pwfile, e.shots}
	}},
	{file: "security/text_vb.js", pw: true, args: func(e *env) []string {
		return []string{e.url, e.pwfile}
	}},
	{file: "security/evil_vb.js", pw: true, args: func(e *env) []string {
		return []string{e.url, "EVIL", e.pwfile, e.shots}
	}},
	{file: "security/evil2_vb.js", args: func(e *env) []string {
		return []string{e.url, "EVIL", e.shots}
	}},
	{file: "browser/race2.js", seat: "e2e-race", args: func(
		e *env) []string {
		return []string{e.url, "e2e-race", e.shots}
	}},
	{file: "browser/race3.js", seat: "e2e-race", game: true,
		args: func(e *env) []string {
			return []string{e.url, e.gameSeat, "e2e-race", e.shots}
		}},
	{file: "browser/fallback.js", args: func(e *env) []string {
		return []string{e.url, filepath.Join(e.shots, "fallback")}
	}},
	{file: "browser/keepit.js", seat: "e2e-race", args: func(
		e *env) []string {
		return []string{e.url, "e2e-race"}
	}},
	{file: "browser/keepit2.js", seat: "e2e-race", args: func(
		e *env) []string {
		return []string{e.url, "e2e-race"}
	}},
	// race.js was written for the perf round's seats: seat150b.
	{file: "perf/race.js", seat: "seat150b", args: func(e *env) []string {
		return []string{e.url, filepath.Join(e.shots, "race.png")}
	}},
	{file: "perf/closerace.js", args: func(e *env) []string {
		return []string{e.url}
	}},
	{file: "perf/restart_fix.js", stop: true, args: func(e *env) []string {
		return []string{e.url, e.shots, e.stop, e.start}
	}},
	{file: "perf/down_mint.js", stop: true, args: func(e *env) []string {
		return []string{e.url, e.stop, e.start}
	}},
	{file: "perf/lan_restart.js", pw: true, stop: true,
		args: func(e *env) []string {
			return []string{e.url, e.pwfile, e.stop, e.start}
		}},
}

// injectedRE finds the "injected" lists the scripts print.
var injectedRE = regexp.MustCompile(`"injected":(\[[^\]]*\]|\d+)`)

func TestScripts(t *testing.T) {
	e := setup(t)
	e.clean(t)
	e.hostile(t)
	evil := evilSite(t, e.url)
	for _, s := range scripts {
		s := s
		if s.file == "security/xss3_vb.js" {
			// xss_vb.js cancelled the <img> card: make it again,
			// and the spending ones.
			e.hostile(t)
			e.spending(t)
		}
		t.Run(filepath.Base(s.file), func(t *testing.T) {
			// With a password, a script that does not log in
			// runs only in docker mode, with login.js preloaded.
			cookie := !s.pw && e.pwfile != ""
			if s.pw != (e.pwfile != "") && !(cookie && e.docker) {
				t.Skip("for a desk with a password: " +
					fmt.Sprint(s.pw))
			}
			if s.stop && (e.stop == "" || e.start == "") {
				t.Skip("needs E2E_STOP and E2E_START")
			}
			if s.seat != "" {
				mintVia(t, e, s.seat)
			}
			if s.game {
				// Front Desk mints no game seats: a card the
				// game
				// made (lvp game setup) must be there.
				e.gameSeat = gameCard(t, e)
				if e.gameSeat == "" {
					t.Skip("no game card on the page " +
						"(lvp game setup makes them)")
				}
			}
			args := s.args(e)
			for i, a := range args {
				if a == "EVIL" {
					args[i] = evil
				}
			}
			cmd := exec.Command("node", append([]string{
				filepath.Join(ref, s.file)}, args...)...)
			if cookie {
				start := ""
				if s.stop {
					start = e.start
				}
				cmd.Env = append(os.Environ(),
					"NODE_OPTIONS=--require "+e.preload,
					"E2E_URL="+e.url,
					"E2E_PWFILE="+e.pwfile,
					"E2E_START="+start)
			}
			out, err := cmd.CombinedOutput()
			text := string(out)
			t.Logf("%s:\n%s", s.file, text)
			// A script's catch prints "FAILED <error>" (race.js's
			// "cancel FAILED during..." is the faked failure it
			// checks, not one).
			if err != nil || strings.HasPrefix(text, "FAILED") ||
				strings.Contains(text, "\nFAILED") {
				t.Fatalf("%s failed: %v", s.file, err)
			}
			for _, m := range injectedRE.FindAllStringSubmatch(text,
				-1) {
				if m[1] != "[]" && m[1] != "0" &&
					!pageOwn(m[1]) &&
					!strings.Contains(text, `"els":`+own) {
					t.Errorf("%s: injected %s", s.file,
						m[1])
				}
			}
			if strings.Contains(text, "dialogs:") &&
				!strings.Contains(text,
					"dialogs: [] page errors: []") {
				t.Errorf("%s: dialogs or page errors", s.file)
			}
			if strings.Contains(text, "evil cards made:") &&
				!strings.Contains(text, "evil cards made: []") {
				t.Errorf("%s: the other site made cards",
					s.file)
			}
			if s.file == "security/xss3_vb.js" {
				sameAsRecorded(t, text)
			}
		})
	}
}

// own is the tally's own bold words, as recorded for Python.
const own = `["<b>tally</b>","<b>Tokens</b>","<b>Refused</b>"]`

func pageOwn(list string) bool { return list == own }

// sameAsRecorded compares xss3_vb.js's lines with Python's recorded
// ones, but for the card count (this stack's cards are its own).
func sameAsRecorded(t *testing.T, text string) {
	b, err := os.ReadFile(filepath.Join(ref, "security",
		"xss4163.out"))
	if err != nil {
		t.Fatal(err)
	}
	cards := regexp.MustCompile(`"cards":\d+,`)
	norm := func(s string) []string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			l = cards.ReplaceAllString(strings.TrimSpace(l), "")
			// The tally lists seats by spend: which of the two
			// spent more is chance. Their names, sorted.
			if j, ok := strings.CutPrefix(l, "tally: "); ok {
				var v struct {
					Injected []string `json:"injected"`
					Hostile  []string `json:"hostile"`
				}
				if json.Unmarshal([]byte(j), &v) == nil {
					sort.Strings(v.Hostile)
					b, _ := json.Marshal(v)
					l = "tally: " + string(b)
				}
			}
			if l != "" && !strings.HasPrefix(l, "hostile card") {
				out = append(out, l)
			}
		}
		return out
	}
	want, got := norm(string(b)), norm(text)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Errorf("xss3_vb.js differs from Python's recorded output:\n"+
			"got:\n%s\nwant:\n%s", strings.Join(got, "\n"),
			strings.Join(want, "\n"))
	}
}

// gameCard is the seat of a game card on the page, or "".
func gameCard(t *testing.T, e *env) string {
	ck, _ := e.session(t)
	req, _ := http.NewRequest("GET", e.url+"/api/state", nil)
	if ck != "" {
		req.Header.Set("Cookie", ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var s struct {
		Cards []struct {
			Seat string
			Game bool
		}
	}
	json.NewDecoder(resp.Body).Decode(&s)
	for _, c := range s.Cards {
		if c.Game && !strings.ContainsAny(c.Seat, "<>") {
			return c.Seat
		}
	}
	return ""
}

// session is the Cookie header and CSRF token of a new session (a
// desk with a password: logged in through /api/login), or "" and the
// local desk's token.
func (e *env) session(t *testing.T) (cookie, csrf string) {
	var s map[string]any
	if e.pwfile == "" {
		resp, err := http.Get(e.url + "/api/session")
		if err != nil {
			t.Fatal(err)
		}
		json.NewDecoder(resp.Body).Decode(&s)
		resp.Body.Close()
		csrf, _ = s["csrf"].(string)
		return "", csrf
	}
	b, err := os.ReadFile(e.pwfile)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{
		"password": strings.TrimSpace(string(b))})
	resp, err := http.Post(e.url+"/api/login", "application/json",
		bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&s)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	cookie, _, _ = strings.Cut(resp.Header.Get("Set-Cookie"), ";")
	csrf, _ = s["csrf"].(string)
	return cookie, csrf
}

// mintVia makes a card through Front Desk's own API (logged in if
// the desk has a password), so the scripts that need one find it.
func mintVia(t *testing.T, e *env, seat string) {
	u := e.url
	ck, csrf := e.session(t)
	b, _ := json.Marshal(map[string]any{"seat": seat,
		"models": []string{"local"}, "budget": "1", "duration": "1d"})
	req, _ := http.NewRequest("POST", u+"/api/mint", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Origin", u)
	if ck != "" {
		req.Header.Set("Cookie", ck)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	t.Logf("card %s through Front Desk: %d", seat, r.StatusCode)
}
