package rules

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shapes Python took at load and then crashed on mid-game (an
// empty [quests.sizes], weights that random.choices refused, chars
// that randint refused) are refused at load with the line, as the
// other shapes of ledger O-1 are. A count past the 32-bit range says
// so.
func TestUnplayableShapes(t *testing.T) {
	b, err := os.ReadFile("../../game/rules.toml")
	if err != nil {
		t.Fatal(err)
	}
	real := string(b)
	sizes := regexp.MustCompile(`(?s)\[quests\.sizes\.small\].*` +
		`par_mana = 45\n`)
	weight := regexp.MustCompile(`(?m)^weight = \d+$`)
	cases := []struct {
		name, text, want string
	}{
		{"empty sizes", sizes.ReplaceAllString(real,
			"[quests.sizes]\n"), "rules.toml line 34: " +
			"quests.sizes must have at least one size, " +
			"like [quests.sizes.small]"},
		{"zero weights", weight.ReplaceAllString(real, "weight = 0"),
			"quests.sizes weights must add up to more than 0"},
		{"inf weight", strings.Replace(real, "weight = 5",
			"weight = inf", 1),
			"quests.sizes weights must add up to a finite number"},
		{"nan weight", strings.Replace(real, "weight = 5",
			"weight = nan", 1),
			"quests.sizes weights must add up to a finite number"},
		{"negative weight", strings.Replace(real, "weight = 5",
			"weight = -1", 1), ""},
		{"chars reversed", strings.Replace(real,
			"chars = [300, 700]", "chars = [700, 300]", 1),
			"rules.toml line 82: personas.frugal.chars must " +
				"be [low, high], like [300, 700]"},
		{"chars equal", strings.Replace(real,
			"chars = [300, 700]", "chars = [500, 500]", 1), ""},
		{"reply big", strings.Replace(real, "reply = 80",
			"reply = 9223372036854775807", 1),
			"rules.toml line 83: personas.frugal.reply must " +
				"be a whole number from -2147483648 to " +
				"2147483647"},
		{"reply float", strings.Replace(real, "reply = 80",
			"reply = 80.5", 1), "rules.toml line 83: " +
			"personas.frugal.reply must be a whole number"},
	}
	for _, c := range cases {
		if c.text == real {
			t.Fatalf("%s: the edit did not apply", c.name)
		}
		p := filepath.Join(t.TempDir(), "rules.toml")
		os.WriteFile(p, []byte(c.text), 0o644)
		_, _, err := Load(p)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want == "":
		case err == nil:
			t.Errorf("%s: read, want %q", c.name, c.want)
		case strings.HasPrefix(c.want, "rules.toml") &&
			err.Error() != c.want:
			t.Errorf("%s: %q, want %q", c.name, err, c.want)
		case !strings.HasSuffix(err.Error(), c.want):
			t.Errorf("%s: %q, want ... %q", c.name, err, c.want)
		}
	}
}
