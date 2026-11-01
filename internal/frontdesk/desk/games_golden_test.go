package desk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sortedSeats(m map[string]bool) string {
	var out []string
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

func TestGoldenGameSeats(t *testing.T) {
	g := golden(t, "games.json")
	dbs := filepath.Join("testdata", "golden", "games")
	for _, raw := range g["cases"] {
		var c struct {
			Rules     string
			RulesText *string `json:"rules_text"`
			DB        string
			Prefix    string
			Seats     []string
			Owns      map[string]bool
		}
		json.Unmarshal(raw, &c)
		dir := t.TempDir()
		if c.RulesText != nil {
			os.WriteFile(filepath.Join(dir, "rules.toml"),
				[]byte(*c.RulesText), 0o644)
		}
		if c.DB != "none" {
			copyFile(t, filepath.Join(dbs, c.DB+".db"),
				filepath.Join(dir, "office.db"))
		}
		gs := NewGameSeats(dir)
		got := sortedSeats(gs.Get())
		if got != strings.Join(c.Seats, "|") ||
			gs.Prefix() != c.Prefix {
			t.Errorf("%s/%s: seats %q prefix %q, want %q %q",
				c.Rules,
				c.DB, got, gs.Prefix(), c.Seats, c.Prefix)
		}
		for name, want := range c.Owns {
			if gs.Owns(name) != want {
				t.Errorf("%s/%s: owns(%q) = %v", c.Rules, c.DB,
					name,
					!want)
			}
		}
	}
}

func TestGoldenGameSeatsChange(t *testing.T) {
	g := golden(t, "games.json")
	var c struct{ Steps [][]string }
	json.Unmarshal(g["changes"][0], &c)
	cases := map[string]string{}
	for _, raw := range g["cases"] {
		var x struct {
			Rules     string
			RulesText *string `json:"rules_text"`
		}
		json.Unmarshal(raw, &x)
		if x.RulesText != nil {
			cases[x.Rules] = *x.RulesText
		}
	}
	dir := t.TempDir()
	dbs := filepath.Join("testdata", "golden", "games")
	rp, dp := filepath.Join(dir, "rules.toml"), filepath.Join(dir,
		"office.db")
	os.WriteFile(rp, []byte(cases["normal"]), 0o644)
	copyFile(t, filepath.Join(dbs, "players.db"), dp)
	gs := NewGameSeats(dir)
	step := func(i int) {
		if got := sortedSeats(gs.Get()); got !=
			strings.Join(c.Steps[i], "|") {
			t.Errorf("step %d: %q, want %q", i, got, c.Steps[i])
		}
	}
	step(0)
	later := time.Now().Add(time.Minute)
	copyFile(t, filepath.Join(dbs, "players-late.db"), dp)
	os.WriteFile(rp, []byte(cases["no-prefix"]), 0o644)
	os.Chtimes(dp, later, later)
	os.Chtimes(rp, later, later)
	step(1)
	os.Remove(dp)
	step(2)
}
