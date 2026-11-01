package game

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// units and words are office.py's UNITS and WORDS.
var (
	units = map[string]int{"s": 1, "m": 60, "h": 3600, "d": 86400}
	words = strings.Fields("please draft review the quarterly " +
		"report and fix every typo in section two then summarise " +
		"the meeting notes for the team with clear next steps " +
		"owners and dates")
	durationRE = regexp.MustCompile(`^([1-9][0-9]*)([smhd])$`)
)

// dieError is office.py's die(): Main prints the message to standard
// error and exits 1.
type dieError struct{ msg string }

func (e *dieError) Error() string { return e.msg }

func die(msg string) error { return &dieError{msg} }

// seconds is office.py's seconds(): "35s" is 35.
func seconds(text string) (int, error) {
	m := durationRE.FindStringSubmatch(text)
	if m == nil {
		return 0, die("bad duration " + pyfmt.ReprString(text) +
			": use a whole number and s, m, h or d")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		n = math.MaxInt
	}
	return n * units[m[2]], nil
}

// clockText is office.py's clock(): HH:MM:SS of t in UTC.
func clockText(t float64) string {
	return time.Unix(int64(math.Floor(t)), 0).UTC().Format("15:04:05")
}

// money is office.py's money(): dollars with up to 8 decimals.
func money(usd float64) string {
	s := pyfmt.Format(usd, ".8f")
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

// lit is office.py's lit(): a quoted SQL text.
func lit(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// pyInt is Python's int(text) for a str: spaces around it, a sign,
// decimal digits of any script, single _ between digits. ok false
// where Python raises ValueError. Past the int range it holds at the
// largest int, which no count here reaches.
func pyInt(text string) (n int, ok bool) {
	s := []rune(pyfmt.Strip(text))
	neg := false
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if len(s) == 0 || s[0] == '_' || s[len(s)-1] == '_' {
		return 0, false
	}
	big := false
	for i, r := range s {
		if r == '_' {
			if s[i-1] == '_' {
				return 0, false
			}
			continue
		}
		d, isDigit := digitValue(r)
		if !isDigit {
			return 0, false
		}
		if n > (math.MaxInt-d)/10 {
			big = true
			continue
		}
		n = n*10 + d
	}
	if big {
		n = math.MaxInt
	}
	if neg {
		n = -n
	}
	return n, true
}

// digitValue is the value of a decimal digit of any script (Unicode
// Nd), whose digits come in runs that start at a zero.
func digitValue(r rune) (int, bool) {
	if r >= '0' && r <= '9' {
		return int(r - '0'), true
	}
	if !unicode.Is(unicode.Nd, r) {
		return 0, false
	}
	start := r
	for unicode.Is(unicode.Nd, start-1) {
		start--
	}
	return int(r-start) % 10, true
}
