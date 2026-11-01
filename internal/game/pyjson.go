package game

import (
	"fmt"
	"strings"
)

// jsonString is Python's json.dumps(s) with its defaults: ASCII only,
// "\uXXXX" for anything outside space to tilde, surrogate pairs above
// U+FFFF.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r >= ' ' && r <= '~':
				b.WriteRune(r)
			case r > 0xFFFF:
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xD800+(r>>10),
					0xDC00+(r&0x3FF))
			default:
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// jsonList is json.dumps of a list of text: ["a", "b"].
func jsonList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = jsonString(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}
