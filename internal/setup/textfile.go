package setup

import (
	"bytes"
	"regexp"
	"strings"
	"unicode/utf16"
)

// textFile is a file of lines, read so that writing it back gives the
// same bytes: a byte order mark at the start and each line's own
// ending (\n, \r\n or none, on the last line) are kept.
type textFile struct {
	bom   bool
	lines []string // without their endings
	ends  []string
}

const bom = "\xef\xbb\xbf"

func readText(b []byte) *textFile {
	s := string(b)
	t := &textFile{}
	if strings.HasPrefix(s, bom) {
		t.bom, s = true, s[len(bom):]
	}
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			t.lines, t.ends = append(t.lines, s), append(t.ends, "")
			break
		}
		line, end := s[:i], "\n"
		if strings.HasSuffix(line, "\r") {
			line, end = line[:len(line)-1], "\r\n"
		}
		t.lines, t.ends = append(t.lines, line), append(t.ends, end)
		s = s[i+1:]
	}
	return t
}

// eol is the line ending new lines get: the file's first one, else
// \n.
func (t *textFile) eol() string {
	for _, e := range t.ends {
		if e != "" {
			return e
		}
	}
	return "\n"
}

// add puts line at the end, after ending the last line if it has no
// ending.
func (t *textFile) add(line string) {
	eol := t.eol()
	if n := len(t.ends); n > 0 && t.ends[n-1] == "" {
		t.ends[n-1] = eol
	}
	t.lines, t.ends = append(t.lines, line), append(t.ends, eol)
}

// drop removes line i.
func (t *textFile) drop(i int) {
	t.lines = append(t.lines[:i], t.lines[i+1:]...)
	t.ends = append(t.ends[:i], t.ends[i+1:]...)
}

func (t *textFile) bytes() []byte {
	var b bytes.Buffer
	if t.bom {
		b.WriteString(bom)
	}
	for i, l := range t.lines {
		b.WriteString(l + t.ends[i])
	}
	return b.Bytes()
}

// setting is a NAME=value line, perhaps with export in front, as
// bash and Compose read it.
var setting = regexp.MustCompile(
	`^[ \t]*(?:export[ \t]+)?([A-Za-z_][A-Za-z0-9_]*)=`)

// name is the setting a line sets, or "".
func name(line string) string {
	if m := setting.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// value is what follows the = on a setting line.
func value(line string) string {
	_, v, _ := strings.Cut(line, "=")
	return v
}

// find is the lines that set n, first first.
func (t *textFile) find(n string) []int {
	var at []int
	for i, l := range t.lines {
		if name(l) == n {
			at = append(at, i)
		}
	}
	return at
}

// fromUTF16 turns UTF-16 text with a byte order mark (Windows
// PowerShell 5.1's >> and Out-File write it) into UTF-8; ok false if b
// is not that.
func fromUTF16(b []byte) (string, bool) {
	if len(b) < 2 || len(b)%2 != 0 {
		return "", false
	}
	var le bool
	switch {
	case b[0] == 0xff && b[1] == 0xfe:
		le = true
	case b[0] == 0xfe && b[1] == 0xff:
	default:
		return "", false
	}
	u := make([]uint16, 0, len(b)/2-1)
	for i := 2; i+1 < len(b); i += 2 {
		if le {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		} else {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		}
	}
	return string(utf16.Decode(u)), true
}
