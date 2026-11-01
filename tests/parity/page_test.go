package parity

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// The page contract (spec 16.4, gap 10): every field gui/static/app.js
// reads from the server's JSON, with its JSON type, checked against
// Go's answers in all four modes, with no browser. And the other way:
// a scan of app.js for property reads on the names that hold server
// JSON, so a field the page starts reading cannot be missed here.

// field is one field the page reads: where it comes from, its JSON
// types ("|" between several), and whether it may be left out.
type field struct {
	where, name, types string
	optional           bool
}

var contract = []field{
	{"session", "csrf", "string|null", false},
	{"session", "seat_url", "string|null", false},
	{"session", "lan", "bool", false},
	{"session", "docker", "bool", false},
	{"session", "need_login", "bool", false},
	{"session", "reset_cmd", "string", false},
	{"login", "csrf", "string", false},
	{"login", "seat_url", "string", false},
	{"state", "cards", "array", false},
	{"state", "defaults", "object", false},
	{"state", "desk", "object", false},
	{"state", "models", "array", false},
	{"state", "tally", "object|null", false},
	{"desk", "up", "bool", false},
	{"desk", "litellm", "string|null", false},
	{"desk", "notes", "array", false},
	{"defaults", "models", "array", false},
	{"defaults", "budget", "string", false},
	{"defaults", "duration", "string", false},
	{"card", "id", "string", false},
	{"card", "seat", "string", false},
	{"card", "ends", "string", false},
	{"card", "budget", "number|null", false},
	{"card", "spent", "number", false},
	{"card", "used", "number|null", false},
	{"card", "state", "string", false},
	{"card", "models", "array", false},
	{"card", "expires", "string|null", false},
	{"card", "resets", "string|null", false},
	{"card", "refills", "string|null", false},
	{"card", "game", "bool", false},
	{"tally", "day", "string", false},
	{"tally", "source", "string", false},
	{"tally", "seats", "array", false},
	{"tally", "models", "array", false},
	{"row", "name", "string", false},
	{"row", "live", "bool", true}, // seat rows only
	{"row", "spend", "number|null", false},
	{"row", "reqs", "number|null", false},
	{"row", "tokens", "number|null", false},
	{"row", "fails", "number|null", false},
	{"row", "t_spend", "number|null", false},
	{"row", "t_reqs", "number|null", false},
	{"row", "t_tokens", "number|null", false},
	{"row", "t_fails", "number|null", false},
	{"tick", "age", "number", false},
	{"mint", "key", "string", false},
	{"mint", "seat", "string", false},
	{"mint", "base_url", "string", false},
	{"mint", "models", "array", false},
	{"mint", "budget", "string", false},
	{"mint", "duration", "string", false},
	{"mint", "refill", "string", false},
	{"revoke", "ok", "bool", false},
	{"revoke", "seat", "string", false},
	{"error", "error", "string", false},
	{"error", "field", "string", true}, // validation errors only
}

// browserNames are properties the page reads on those same variable
// names that are not server JSON: fetch's Response, dialogs and other
// elements.
var browserNames = map[string]bool{"ok": true, "status": true,
	"json": true, "headers": true, "addEventListener": true,
	"close": true, "open": true, "removeAttribute": true,
	"setAttribute": true, "showModal": true, "hidden": true}

// TestPageReadsOnlyContractFields scans app.js.
func TestPageReadsOnlyContractFields(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repo, "gui", "static",
		"app.js"))
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, f := range contract {
		known[f.name] = true
	}
	// state.desk and render(s)'s s.desk read the state's desk.
	known["desk"] = true
	re := regexp.MustCompile(`(?:^|[^A-Za-z0-9_.$])(s|state|d|c|r|t|` +
		`data)\.([A-Za-z_]+)|JSON\.parse\(e\.data\)\.([A-Za-z_]+)`)
	var missing []string
	for n, line := range strings.Split(string(b), "\n") {
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			name := m[2]
			if name == "" {
				name = m[3]
			}
			if !known[name] && !browserNames[name] {
				missing = append(missing, fmt.Sprintf(
					"app.js:%d: %s", n+1,
					strings.TrimSpace(m[0])))
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("app.js reads names the contract does not list "+
			"(add them to page_test.go with their types):\n%s",
			strings.Join(missing, "\n"))
	}
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	}
	return "object"
}

// checkFields checks one object's fields against the contract's
// entries for where.
func checkFields(t *testing.T, mode, where string, obj any) {
	t.Helper()
	m, ok := obj.(map[string]any)
	if !ok {
		t.Errorf("%s: %s is %s, not an object", mode, where,
			jsonType(obj))
		return
	}
	for _, f := range contract {
		if f.where != where {
			continue
		}
		v, in := m[f.name]
		if !in {
			if !f.optional {
				t.Errorf("%s: %s.%s is missing", mode, where,
					f.name)
			}
			continue
		}
		got := jsonType(v)
		if !strings.Contains("|"+f.types+"|", "|"+got+"|") {
			t.Errorf("%s: %s.%s is %s, the page wants %s", mode,
				where, f.name, got, f.types)
		}
	}
}

// TestPageContract runs lvp desk in each mode and checks every
// answer the page reads.
func TestPageContract(t *testing.T) {
	for _, m := range modes {
		m := m
		t.Run(m.name, func(t *testing.T) {
			if m.name == "lan" && runtime.GOOS == "windows" {
				// --lan binds every address: a firewall prompt.
				t.Skip("--lan on Windows asks the firewall")
			}
			pageContract(t, m)
		})
	}
}

func pageContract(t *testing.T, m mode) {
	s := start(t, "go", m)
	call := func(method, path string, body any) (int, map[string]any,
		http.Header) {
		var r io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			r = strings.NewReader(string(b))
		}
		u := fmt.Sprintf("http://127.0.0.1:%d%s", s.port, path)
		req, _ := http.NewRequest(method, u, r)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", fmt.Sprintf("http://127.0.0.1:%d",
			s.port))
		if s.cookie != "" {
			req.Header.Set("Cookie", s.cookie)
		}
		if s.csrf != "" {
			req.Header.Set("X-CSRF-Token", s.csrf)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var j map[string]any
		b, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(b, &j); err != nil {
			t.Fatalf("%s %s: not JSON: %q", method, path, b)
		}
		return resp.StatusCode, j, resp.Header
	}
	_, sess, _ := call("GET", "/api/session", nil)
	checkFields(t, m.name, "session", sess)
	if m.pw {
		st, e, _ := call("POST", "/api/login",
			map[string]string{"password": "wrong"})
		if st != 401 {
			t.Errorf("%s: wrong password: %d", m.name, st)
		}
		checkFields(t, m.name, "error", e)
		time.Sleep(1100 * time.Millisecond)
		st, l, h := call("POST", "/api/login",
			map[string]string{"password": deskPW})
		if st != 200 {
			t.Fatalf("%s: login: %d %v", m.name, st, l)
		}
		checkFields(t, m.name, "login", l)
		s.cookie, _, _ = strings.Cut(h.Get("Set-Cookie"), ";")
		s.csrf, _ = l["csrf"].(string)
		_, sess, _ = call("GET", "/api/session", nil)
		checkFields(t, m.name, "session", sess)
	} else {
		s.csrf, _ = sess["csrf"].(string)
	}
	st, mint, _ := call("POST", "/api/mint", map[string]any{
		"seat": "page-x", "models": []string{"local"}, "budget": "2",
		"duration": "1d", "refill": "1d"})
	if st != 200 {
		t.Fatalf("%s: mint: %d %v", m.name, st, mint)
	}
	checkFields(t, m.name, "mint", mint)
	st, e, _ := call("POST", "/api/mint", map[string]any{"seat": "a b"})
	if st != 400 {
		t.Errorf("%s: bad seat: %d", m.name, st)
	}
	checkFields(t, m.name, "error", e)
	if _, in := e["field"]; !in {
		t.Errorf("%s: a validation error has no field", m.name)
	}
	_, state, _ := call("GET", "/api/state", nil)
	checkState(t, m.name, state)
	st, rv, _ := call("POST", "/api/revoke",
		map[string]string{"seat": "page-x"})
	if st != 200 {
		t.Fatalf("%s: revoke: %d %v", m.name, st, rv)
	}
	checkFields(t, m.name, "revoke", rv)
	// The live stream: the page reads JSON.parse(e.data).age from a
	// tick, which comes 15 s after the last event. Once is enough.
	if m.name == "local" {
		checkTick(t, s)
	}
}

func checkState(t *testing.T, mode string, state map[string]any) {
	checkFields(t, mode, "state", state)
	checkFields(t, mode, "desk", state["desk"])
	checkFields(t, mode, "defaults", state["defaults"])
	cards, _ := state["cards"].([]any)
	if len(cards) == 0 {
		t.Errorf("%s: no cards in the state", mode)
	}
	for _, c := range cards {
		checkFields(t, mode, "card", c)
	}
	if tally, ok := state["tally"].(map[string]any); ok {
		checkFields(t, mode, "tally", tally)
		for _, k := range []string{"seats", "models"} {
			rows, _ := tally[k].([]any)
			for _, r := range rows {
				checkFields(t, mode, "row", r)
			}
		}
	} else if mode != "docker" {
		t.Errorf("%s: no tally", mode)
	}
}

func checkTick(t *testing.T, s *side) {
	c, err := net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "GET /api/events HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n"+
		"Cookie: %s\r\n\r\n", s.port, s.cookie)
	c.SetReadDeadline(time.Now().Add(25 * time.Second))
	br := bufio.NewReader(c)
	event := ""
	var seen []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("no tick in 25 s; events %v", seen)
		}
		line = strings.TrimRight(line, "\r\n")
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
			seen = append(seen, v)
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok &&
			event == "tick" {
			var j any
			if err := json.Unmarshal([]byte(v), &j); err != nil {
				t.Fatalf("tick data: %q", v)
			}
			checkFields(t, "local", "tick", j)
			return
		}
	}
}

// TestContractListsEachFieldOnce keeps the table tidy.
func TestContractListsEachFieldOnce(t *testing.T) {
	seen := map[string]bool{}
	var dup []string
	for _, f := range contract {
		k := f.where + "." + f.name
		if seen[k] {
			dup = append(dup, k)
		}
		seen[k] = true
	}
	sort.Strings(dup)
	if len(dup) > 0 {
		t.Errorf("listed twice: %v", dup)
	}
}
