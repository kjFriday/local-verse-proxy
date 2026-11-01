package rules

import (
	"fmt"
	"strings"
)

// indexLines finds the line of every table and key in a TOML file,
// for messages: "day" for [day], "day.length" for its key, and
// "roster.seats[2]" and "roster.seats[2].name" for the third seat,
// written either as [[roster.seats]] or as an inline table in an
// array. It reads only enough TOML for that; a file it cannot follow
// gives fewer lines, never an error.
func indexLines(text string) map[string]int {
	ix := &lineIndex{lines: map[string]int{}, counts: map[string]int{}}
	ix.scan(text)
	return ix.lines
}

type lineIndex struct {
	lines  map[string]int
	counts map[string]int // [[array]] tables seen so far

	s    string
	i    int
	line int
}

func (ix *lineIndex) set(path string, line int) {
	if _, ok := ix.lines[path]; !ok {
		ix.lines[path] = line
	}
}

func (ix *lineIndex) scan(text string) {
	ix.s, ix.i, ix.line = text, 0, 1
	table := ""
	for ix.i < len(ix.s) {
		ix.blanks(true)
		if ix.i >= len(ix.s) {
			return
		}
		c := ix.s[ix.i]
		switch {
		case c == '#':
			ix.comment()
		case c == '[':
			double := strings.HasPrefix(ix.s[ix.i:], "[[")
			if double {
				ix.i += 2
			} else {
				ix.i++
			}
			key := ix.key()
			ix.blanks(false)
			if double {
				ix.i += 2
				n := ix.counts[key]
				ix.counts[key] = n + 1
				ix.set(key, ix.line)
				table = fmt.Sprintf("%s[%d]", key, n)
			} else {
				ix.i++
				table = key
			}
			ix.set(table, ix.line)
			ix.rest()
		default:
			line := ix.line
			key := ix.key()
			if key == "" {
				ix.rest()
				continue
			}
			path := join(table, key)
			ix.set(path, line)
			ix.blanks(false)
			if ix.i < len(ix.s) && ix.s[ix.i] == '=' {
				ix.i++
			}
			ix.blanks(false)
			ix.value(path)
			ix.rest()
		}
	}
}

func join(table, key string) string {
	if table == "" {
		return key
	}
	return table + "." + key
}

// blanks skips spaces and tabs, and new lines too if nl.
func (ix *lineIndex) blanks(nl bool) {
	for ix.i < len(ix.s) {
		switch ix.s[ix.i] {
		case ' ', '\t', '\r':
		case '\n':
			if !nl {
				return
			}
			ix.line++
		default:
			return
		}
		ix.i++
	}
}

func (ix *lineIndex) comment() {
	for ix.i < len(ix.s) && ix.s[ix.i] != '\n' {
		ix.i++
	}
}

// rest skips to the end of the line.
func (ix *lineIndex) rest() { ix.comment() }

// key reads a dotted key, bare or quoted, as TOML writes it in a
// path.
func (ix *lineIndex) key() string {
	var parts []string
	for {
		ix.blanks(false)
		if ix.i >= len(ix.s) {
			break
		}
		c := ix.s[ix.i]
		var part string
		switch {
		case c == '"' || c == '\'':
			part = ix.quoted(c)
		case isBare(c):
			start := ix.i
			for ix.i < len(ix.s) && isBare(ix.s[ix.i]) {
				ix.i++
			}
			part = ix.s[start:ix.i]
		default:
			return strings.Join(parts, ".")
		}
		parts = append(parts, tomlKey(part))
		ix.blanks(false)
		if ix.i < len(ix.s) && ix.s[ix.i] == '.' {
			ix.i++
			continue
		}
		break
	}
	return strings.Join(parts, ".")
}

func isBare(c byte) bool {
	return c == '_' || c == '-' || c >= 'a' && c <= 'z' ||
		c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// quoted reads a one-line quoted string, q on its opening quote.
func (ix *lineIndex) quoted(q byte) string {
	ix.i++
	var b strings.Builder
	for ix.i < len(ix.s) && ix.s[ix.i] != q && ix.s[ix.i] != '\n' {
		if q == '"' && ix.s[ix.i] == '\\' && ix.i+1 < len(ix.s) {
			ix.i++
		}
		b.WriteByte(ix.s[ix.i])
		ix.i++
	}
	if ix.i < len(ix.s) && ix.s[ix.i] == q {
		ix.i++
	}
	return b.String()
}

// value skips one value, noting the lines of inline tables in arrays
// and of their keys.
func (ix *lineIndex) value(path string) {
	if ix.i >= len(ix.s) {
		return
	}
	switch c := ix.s[ix.i]; {
	case strings.HasPrefix(ix.s[ix.i:], `"""`),
		strings.HasPrefix(ix.s[ix.i:], `'''`):
		q := ix.s[ix.i : ix.i+3]
		ix.i += 3
		for ix.i < len(ix.s) && !strings.HasPrefix(ix.s[ix.i:], q) {
			if ix.s[ix.i] == '\n' {
				ix.line++
			}
			if q == `"""` && ix.s[ix.i] == '\\' {
				ix.i++
			}
			ix.i++
		}
		ix.i += 3
		for ix.i < len(ix.s) && ix.s[ix.i] == q[0] {
			ix.i++
		}
	case c == '"' || c == '\'':
		ix.quoted(c)
	case c == '[':
		ix.i++
		for n := 0; ix.i < len(ix.s); {
			ix.blanks(true)
			if ix.i >= len(ix.s) {
				return
			}
			switch ix.s[ix.i] {
			case ']':
				ix.i++
				return
			case ',':
				ix.i++
			case '#':
				ix.comment()
			default:
				item := fmt.Sprintf("%s[%d]", path, n)
				if ix.s[ix.i] == '{' {
					ix.set(item, ix.line)
				}
				ix.value(item)
				n++
			}
		}
	case c == '{':
		ix.i++
		for ix.i < len(ix.s) {
			ix.blanks(true)
			if ix.i >= len(ix.s) {
				return
			}
			switch ix.s[ix.i] {
			case '}':
				ix.i++
				return
			case ',':
				ix.i++
			default:
				line := ix.line
				k := ix.key()
				if k == "" {
					ix.i++
					continue
				}
				ix.set(path+"."+k, line)
				ix.blanks(false)
				if ix.i < len(ix.s) && ix.s[ix.i] == '=' {
					ix.i++
				}
				ix.blanks(true)
				ix.value(path + "." + k)
			}
		}
	default:
		for ix.i < len(ix.s) {
			c := ix.s[ix.i]
			if c == ',' || c == ']' || c == '}' || c == '\n' ||
				c == '#' {
				return
			}
			ix.i++
		}
	}
}
