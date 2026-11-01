package seats

import (
	"errors"
	"fmt"
	"strings"
)

// SyntaxError is a line Parse would not read.
type SyntaxError struct {
	Line int    // counting from 1
	Msg  string // what is wrong, and what to do
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

// ErrUTF16 means the text is UTF-16, as Windows PowerShell 5.1 saves
// a file made with > or Out-File, and Notepad when told "Unicode".
// It reads as if the path came first, as Load prints it.
var ErrUTF16 = errors.New("seems to be saved as UTF-16 (Notepad calls " +
	"it Unicode). Save it as UTF-8 instead: in Notepad, File, Save as, " +
	"then Encoding: UTF-8")

// Parse reads seats.env text the way bash's "source" would, for lines
// like these:
//
//	# a comment, and blank lines
//	NAME=value
//	export NAME=value
//	NAME='single quoted' or NAME="double quoted"
//	NAME=value  # a comment after the value
//	URL=http://${HOST}:4000   ($NAME and ${NAME}, set earlier or in env)
//	A=1 B=2                   (several on one line, as bash allows)
//	export NAME               (sets nothing, so it is skipped)
//
// Anything else is refused with its line number, not guessed at:
// $(...) and backticks (they run commands), other shell syntax, values
// spread over several lines, and $NAME when NAME is not set (mint-key.sh
// runs with "set -u", so bash refuses that too). A Windows line ending
// (\r\n) and a byte order mark at the start, which Notepad may add,
// are dropped; bash would keep them in the values. UTF-16 text gets
// ErrUTF16. An error never repeats any part of a value.
func Parse(text string, env LookupFunc) (map[string]string, error) {
	vals, _, err := parse(text, env)
	return vals, err
}

// parse is Parse, and the names in the order the file first sets
// them.
func parse(text string, env LookupFunc) (map[string]string, []string,
	error) {
	if strings.HasPrefix(text, "\xff\xfe") ||
		strings.HasPrefix(text, "\xfe\xff") ||
		strings.IndexByte(text, 0) >= 0 {
		return nil, nil, ErrUTF16
	}
	text = strings.TrimPrefix(text, "\ufeff")
	vals := map[string]string{}
	var names []string
	lookup := func(name string) (string, bool) {
		if v, ok := vals[name]; ok {
			return v, true
		}
		if env != nil {
			return env(name)
		}
		return "", false
	}
	for i, line := range strings.Split(text, "\n") {
		p := &lineParser{s: strings.TrimSuffix(line, "\r"),
			lookup: lookup}
		if msg := p.parse(func(name, value string) {
			if _, ok := vals[name]; !ok {
				names = append(names, name)
			}
			vals[name] = value
		}); msg != "" {
			return nil, nil, &SyntaxError{Line: i + 1, Msg: msg}
		}
	}
	return vals, names, nil
}

type lineParser struct {
	s      string
	i      int
	lookup LookupFunc
}

const quoteIt = "; to keep it as is, put the value in single quotes"

// parse reads one line and calls set for each NAME=value on it. It
// returns what is wrong with the line, or "" if nothing is.
func (p *lineParser) parse(set func(name, value string)) string {
	p.skipBlanks()
	if p.done() || p.s[p.i] == '#' {
		return ""
	}
	export := false
	if rest := p.s[p.i:]; strings.HasPrefix(rest, "export") &&
		len(rest) > 6 && isBlank(rest[6]) {
		p.i += 6
		p.skipBlanks()
		export = true
	}
	// bash sets plain NAME=value words one by one, so B=$A sees the A
	// just before it. export works out all its values first, then sets
	// them.
	var later [][2]string
	n := 0 // values read on this line
	for {
		start := p.i
		for !p.done() && isNameChar(p.s[p.i], p.i == start) {
			p.i++
		}
		name := p.s[start:p.i]
		switch {
		case name != "" && !p.done() && p.s[p.i] == '=':
			p.i++
			value, msg := p.word()
			if msg != "" {
				return msg
			}
			if export {
				later = append(later, [2]string{name, value})
			} else {
				set(name, value)
			}
			n++
		case export && n == 0 && name != "" &&
			(p.done() || isBlank(p.s[p.i])):
			// "export NAME" only hands NAME, set above, on to the
			// programs the shell starts. It sets nothing.
		case n > 0:
			return "there is more after the value; " +
				"put quotes around a value with spaces in it"
		default:
			return "this is not a NAME=value line " +
				"(the shell would run it as a command)"
		}
		p.skipBlanks()
		if p.done() || p.s[p.i] == '#' {
			break
		}
	}
	for _, kv := range later {
		set(kv[0], kv[1])
	}
	return ""
}

// word reads one shell word: plain text, quoted parts and $NAME,
// up to the first blank outside quotes.
func (p *lineParser) word() (string, string) {
	var b strings.Builder
	start := p.i
	for !p.done() && !isBlank(p.s[p.i]) {
		c := p.s[p.i]
		switch {
		case c == '\'':
			end := strings.IndexByte(p.s[p.i+1:], '\'')
			if end < 0 {
				return "", "a ' quote is not closed; " +
					"a value must fit on one line"
			}
			b.WriteString(p.s[p.i+1 : p.i+1+end])
			p.i += end + 2
		case c == '"':
			if msg := p.double(&b); msg != "" {
				return "", msg
			}
		case c == '\\':
			if p.i+1 >= len(p.s) {
				return "", "a \\ at the end of a line joins " +
					"lines; a value must fit on one line"
			}
			b.WriteByte(p.s[p.i+1])
			p.i += 2
		case c == '$':
			if msg := p.dollar(&b, false); msg != "" {
				return "", msg
			}
		case c == '`':
			return "", "backticks (`) would run a command" + quoteIt
		case c == '~' && (p.i == start || p.s[p.i-1] == ':'):
			return "", "a ~ here would mean a home folder" + quoteIt
		case strings.IndexByte(";&|<>()", c) >= 0:
			return "", fmt.Sprintf("%q is shell syntax here", c) +
				quoteIt
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return b.String(), ""
}

// double reads a "double quoted" part, p.i on its opening quote.
func (p *lineParser) double(b *strings.Builder) string {
	p.i++
	for !p.done() {
		c := p.s[p.i]
		switch c {
		case '"':
			p.i++
			return ""
		case '\\':
			if p.i+1 >= len(p.s) {
				return "a \" quote is not closed; " +
					"a value must fit on one line"
			}
			// Only these lose their \ inside double quotes.
			if n := p.s[p.i+1]; n == '$' || n == '`' || n == '"' ||
				n == '\\' {
				b.WriteByte(n)
			} else {
				b.WriteByte('\\')
				b.WriteByte(n)
			}
			p.i += 2
		case '$':
			if msg := p.dollar(b, true); msg != "" {
				return msg
			}
		case '`':
			return "backticks (`) would run a command" + quoteIt
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return "a \" quote is not closed; a value must fit on one line"
}

// dollar reads $NAME or ${NAME}, p.i on the $. A $ that cannot start
// anything stays a plain $, as in bash.
func (p *lineParser) dollar(b *strings.Builder, quoted bool) string {
	rest := p.s[p.i+1:]
	var name string
	switch {
	case rest == "" || isBlank(rest[0]):
		b.WriteByte('$')
		p.i++
		return ""
	case isNameChar(rest[0], true):
		n := 1
		for n < len(rest) && isNameChar(rest[n], false) {
			n++
		}
		name = rest[:n]
		p.i += 1 + n
	case rest[0] == '{':
		end := strings.IndexByte(rest, '}')
		inner := ""
		if end > 0 {
			inner = rest[1:end]
		}
		if !validName(inner) {
			return "only ${NAME} works here, not other ${...} " +
				"forms" + quoteIt
		}
		name = inner
		p.i += 1 + end + 1
	case rest[0] == '(':
		return "$(...) would run a command" + quoteIt
	case quoted && (rest[0] == '"' || rest[0] == '\''):
		b.WriteByte('$')
		p.i++
		return ""
	case strings.IndexByte("0123456789@*#?$!-['\"", rest[0]) >= 0:
		return fmt.Sprintf("$%c means something special to the "+
			"shell", rest[0]) + quoteIt
	default:
		b.WriteByte('$')
		p.i++
		return ""
	}
	v, ok := p.lookup(name)
	if !ok {
		// Not named: in a password, the "name" is part of the value.
		return "a $ here starts the name of a setting that is not " +
			"set (set it on a line above)" + quoteIt
	}
	b.WriteString(v)
	return ""
}

func (p *lineParser) done() bool { return p.i >= len(p.s) }

func (p *lineParser) skipBlanks() {
	for !p.done() && isBlank(p.s[p.i]) {
		p.i++
	}
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

func isNameChar(c byte, first bool) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' ||
		!first && '0' <= c && c <= '9'
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isNameChar(s[i], i == 0) {
			return false
		}
	}
	return true
}
