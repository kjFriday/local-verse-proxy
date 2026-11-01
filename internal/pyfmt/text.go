package pyfmt

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Space is every character Python's str.isspace() calls white space:
// what str.strip() and str.split() cut. Go's \s is ASCII only and
// has no \v, so the regexps that need Python's \s build a class from
// this.
const Space = "\t\n\v\f\r\x1c\x1d\x1e\x1f \u0085\u00a0\u1680" +
	"\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009" +
	"\u200a\u2028\u2029\u202f\u205f\u3000"

func isSpace(r rune) bool { return strings.ContainsRune(Space, r) }

// SpaceClass is Space for a regexp character class: [<SpaceClass>].
func SpaceClass() string {
	var b strings.Builder
	for _, r := range Space {
		fmt.Fprintf(&b, `\x{%x}`, r)
	}
	return b.String()
}

// Strip is str.strip().
func Strip(s string) string { return strings.TrimFunc(s, isSpace) }

// Fields is str.split() with no separator: runs of white space split,
// and none is kept.
func Fields(s string) []string { return strings.FieldsFunc(s, isSpace) }

// SplitLines is str.splitlines(): lines without their ends, which are
// \n, \r, \r\n, \v, \f, \x1c, \x1d, \x1e, \x85, U+2028 and U+2029.
// A last line end makes no empty line after it.
func SplitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		end := 0
		switch r {
		case '\r':
			end = n
			if i+1 < len(s) && s[i+1] == '\n' {
				end = 2
			}
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085',
			'\u2028', '\u2029':
			end = n
		}
		if end > 0 {
			out = append(out, s[start:i])
			i += end
			start = i
			continue
		}
		i += n
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// Cut is s[:n] in code points.
func Cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for k := 0; k < n && i < len(s); k++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s[:i]
}

// Lower is str.lower(): full lowercase, so İ becomes i and a dot
// above, and a final Σ becomes ς. Letters whose case changed between
// Unicode versions may differ from Python's (ledger F-15).
func Lower(s string) string {
	if isASCII(s) {
		return strings.ToLower(s)
	}
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch {
		case r == 'İ':
			b.WriteString("i\u0307")
		case r == 'Σ':
			if finalSigma(rs, i) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// finalSigma is Unicode's Final_Sigma: a cased letter before (case
// ignorables skipped), and none after.
func finalSigma(rs []rune, i int) bool {
	j := i - 1
	for j >= 0 && caseIgnorable(rs[j]) {
		j--
	}
	if j < 0 || !cased(rs[j]) {
		return false
	}
	k := i + 1
	for k < len(rs) && caseIgnorable(rs[k]) {
		k++
	}
	return k == len(rs) || !cased(rs[k])
}

func cased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) ||
		unicode.IsTitle(r) || unicode.Is(unicode.Other_Lowercase, r) ||
		unicode.Is(unicode.Other_Uppercase, r)
}

func caseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', '^', '`', '¨', '\u00ad', '¯',
		'´', '·', '¸', '‘', '’', '․',
		'‧':
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf,
		unicode.Lm, unicode.Sk)
}

// ReprString is repr(str): in single quotes, or double quotes when
// the text has a ' and no ", with \\, \n, \r, \t, the quote, and
// characters that do not print escaped as Python escapes them.
func ReprString(s string) string {
	q := byte('\'')
	if strings.IndexByte(s, '\'') >= 0 && strings.IndexByte(s, '"') < 0 {
		q = '"'
	}
	var b strings.Builder
	b.WriteByte(q)
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			// Not UTF-8: as Python's surrogateescape would show it.
			fmt.Fprintf(&b, `\udc%02x`, s[i])
			i++
			continue
		}
		i += n
		switch {
		case r == rune(q) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f:
			b.WriteRune(r)
		case printable(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteByte(q)
	return b.String()
}

// printable is str.isprintable() for one character: not a control,
// format, surrogate, private-use, unassigned, or separator other
// than the plain space.
func printable(r rune) bool {
	if r == ' ' {
		return true
	}
	if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co,
		unicode.Zl, unicode.Zp, unicode.Zs) {
		return false
	}
	return unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P,
		unicode.S)
}
