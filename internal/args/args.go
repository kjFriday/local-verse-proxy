// Package args is the one small flag parser every lvp command uses.
//
// It reads --name value and --name=value, -n value and -nvalue, flags
// after positionals, and -- to end the flags. Mistakes read as
// Python's argparse words them: "usage: ..." then "<cmd>: error: ...",
// exit code 2. Unlike argparse, it never guesses a flag from the
// start of its name: --po is not --port.
package args

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Std is where a command reads and writes.
type Std struct {
	In       io.Reader
	Out, Err io.Writer
}

// OS is the program's own standard input, output and error.
func OS() Std { return Std{In: os.Stdin, Out: os.Stdout, Err: os.Stderr} }

type kind int

const (
	kindBool kind = iota
	kindString
	kindList
	kindInt
)

type flag struct {
	long, short, meta, help string
	kind                    kind

	b     *bool
	s     *string
	l     *[]string
	i     *int
	check func(string) (int, error)
	def   string // shown in the help, if any
}

// name is how argparse names the flag in an error: -d/--days.
func (f *flag) name() string {
	switch {
	case f.short != "" && f.long != "":
		return "-" + f.short + "/--" + f.long
	case f.long != "":
		return "--" + f.long
	}
	return "-" + f.short
}

// Set is one command's flags.
type Set struct {
	cmd, usage string
	flags      []*flag
}

// New starts a command's flags. cmd is how the command is typed, like
// "lvp game run"; usage is its usage line without "usage: ", like
// "lvp game run [-d N | --days N] [-q | --quiet]".
func New(cmd, usage string) *Set { return &Set{cmd: cmd, usage: usage} }

func (s *Set) add(f *flag) *flag {
	s.flags = append(s.flags, f)
	return f
}

// Bool is a flag with no value: present means true.
func (s *Set) Bool(long, short, help string) *bool {
	f := s.add(&flag{long: long, short: short, help: help,
		kind: kindBool, b: new(bool)})
	return f.b
}

// String is a flag with one value; def if it is not given.
func (s *Set) String(long, short, meta, def, help string) *string {
	v := def
	f := s.add(&flag{long: long, short: short, meta: meta, help: help,
		kind: kindString, s: &v})
	return f.s
}

// List is a flag that may be given many times; each value is kept, in
// order.
func (s *Set) List(long, short, meta, help string) *[]string {
	f := s.add(&flag{long: long, short: short, meta: meta, help: help,
		kind: kindList, l: new([]string)})
	return f.l
}

// Int is a flag with a whole number. check, if not nil, reads the text
// instead of strconv.Atoi; its error is shown after the flag's name,
// as argparse shows an ArgumentTypeError.
func (s *Set) Int(long, short, meta string, def int,
	check func(text string) (int, error), help string) *int {
	v := def
	f := s.add(&flag{long: long, short: short, meta: meta, help: help,
		kind: kindInt, i: &v, check: check})
	return f.i
}

// ErrHelp means -h or --help was given.
var ErrHelp = errors.New("help")

// UsageError is a mistake in how the command was typed.
type UsageError struct{ Cmd, Usage, Msg string }

// Error is "usage: <Usage>\n<Cmd>: error: <Msg>", as argparse.
func (e *UsageError) Error() string {
	return "usage: " + e.Usage + "\n" + e.Cmd + ": error: " + e.Msg
}

func (s *Set) fail(format string, a ...any) error {
	return &UsageError{Cmd: s.cmd, Usage: s.usage,
		Msg: fmt.Sprintf(format, a...)}
}

func (s *Set) find(name string, long bool) *flag {
	for _, f := range s.flags {
		if long && f.long == name || !long && f.short == name {
			return f
		}
	}
	return nil
}

// Parse reads argv and returns the positionals. Errors: ErrHelp, or
// *UsageError.
func (s *Set) Parse(argv []string) ([]string, error) {
	var pos, unknown []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			pos = append(pos, argv[i+1:]...)
			break
		}
		if a == "-h" || a == "--help" {
			return nil, ErrHelp
		}
		if len(a) < 2 || a[0] != '-' || isNumber(a) {
			pos = append(pos, a)
			continue
		}
		var f *flag
		var val string
		has := false
		if strings.HasPrefix(a, "--") {
			name := a[2:]
			if k := strings.IndexByte(name, '='); k >= 0 {
				name, val, has = name[:k], name[k+1:], true
			}
			f = s.find(name, true)
		} else {
			f = s.find(a[1:2], false)
			if f != nil && len(a) > 2 {
				val, has = a[2:], true
				if f.kind == kindBool {
					f = nil // -qx is refused
				}
			}
		}
		if f == nil {
			unknown = append(unknown, a)
			continue
		}
		if f.kind == kindBool {
			if has {
				return nil, s.fail("argument %s: ignored "+
					"explicit argument %s", f.name(),
					quote(val))
			}
			*f.b = true
			continue
		}
		if !has {
			if i+1 >= len(argv) || isFlag(argv[i+1]) {
				return nil, s.fail("argument %s: expected one "+
					"argument", f.name())
			}
			i++
			val = argv[i]
		}
		if err := s.set(f, val); err != nil {
			return nil, err
		}
	}
	if len(unknown) > 0 {
		return nil, s.fail("unrecognized arguments: %s",
			strings.Join(unknown, " "))
	}
	return pos, nil
}

func (s *Set) set(f *flag, val string) error {
	switch f.kind {
	case kindString:
		*f.s = val
	case kindList:
		*f.l = append(*f.l, val)
	case kindInt:
		if f.check != nil {
			n, err := f.check(val)
			if err != nil {
				return s.fail("argument %s: %s", f.name(), err)
			}
			*f.i = n
			return nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return s.fail("argument %s: invalid int value: %s",
				f.name(), quote(val))
		}
		*f.i = n
	}
	return nil
}

// isFlag is argparse's test for "this looks like an option": a dash
// and more, unless it reads as a negative number.
func isFlag(a string) bool {
	return len(a) > 1 && a[0] == '-' && !isNumber(a)
}

func isNumber(a string) bool {
	_, err := strconv.ParseFloat(a, 64)
	return err == nil && a[0] == '-' && len(a) > 1 &&
		(a[1] >= '0' && a[1] <= '9' || a[1] == '.')
}

// quote is Python's repr of a plain string, enough for flag values.
func quote(s string) string {
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

// Help is the command's help text: the usage line and one line or
// more per flag.
func (s *Set) Help() string {
	var b strings.Builder
	b.WriteString("usage: " + s.usage + "\n\noptions:\n")
	rows := [][2]string{{"-h, --help", "show this help and exit"}}
	for _, f := range s.flags {
		var names []string
		meta := ""
		if f.kind != kindBool {
			meta = " " + f.meta
		}
		if f.short != "" {
			names = append(names, "-"+f.short+meta)
		}
		if f.long != "" {
			names = append(names, "--"+f.long+meta)
		}
		rows = append(rows, [2]string{strings.Join(names, ", "),
			f.help})
	}
	width := 0
	for _, r := range rows {
		if len(r[0]) > width && len(r[0]) <= 24 {
			width = len(r[0])
		}
	}
	for _, r := range rows {
		left := "  " + r[0]
		if len(r[0]) > width {
			b.WriteString(left + "\n")
			left = ""
		}
		b.WriteString(wrap(left, width+4, r[1]))
	}
	return b.String()
}

// wrap puts text after left, padded to col, folding at 79 columns.
func wrap(left string, col int, text string) string {
	var b strings.Builder
	line := left + strings.Repeat(" ", col-len(left))
	n := 0
	for _, w := range strings.Fields(text) {
		if n > 0 && len(line)+1+len(w) > 79 {
			b.WriteString(line + "\n")
			line, n = strings.Repeat(" ", col), 0
		}
		if n > 0 {
			line += " "
		}
		line += w
		n++
	}
	b.WriteString(strings.TrimRight(line, " ") + "\n")
	return b.String()
}

// Exit prints err and gives the exit code: nil 0; ErrHelp 0, with
// the help on Out; a *UsageError 2, on Err; anything else 1, on Err.
func Exit(std Std, s *Set, err error) int {
	var ue *UsageError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrHelp):
		fmt.Fprint(std.Out, s.Help())
		return 0
	case errors.As(err, &ue):
		fmt.Fprintln(std.Err, err)
		return 2
	}
	fmt.Fprintln(std.Err, err)
	return 1
}
