package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/fsx"
	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
)

// mintTimeout is how long one mint or revoke may take: mint-key's.
var mintTimeout time.Duration // 0: mint-key's own, 25 s

func (s *System) mintUsage() string {
	p := s.Prog + " mint"
	if len(p) > 10 {
		// A long path: every option on the next lines.
		return p + " <seat>\n      [--models a,b] [--budget USD] " +
			"[--duration 30d]\n      [--budget-duration 1d] " +
			"[--save FILE]"
	}
	pad := strings.Repeat(" ", len(p)+8)
	return p + " <seat> [--models a,b] [--budget USD] " +
		"[--duration 30d]\n" + pad + "[--budget-duration 1d] " +
		"[--save FILE]"
}

const mintHelp = `Mint a key for one seat, with mint-key's options:

  --models a,b           the models it may use (default: DEFAULT_MODELS)
  --budget USD           the most it may spend, in dollars, numbers only
  --duration 30d         until it expires: s, m, h, d, w or mo
  --budget-duration 1d   refill the budget on that schedule
  --save FILE            save the key in FILE, which only you may open,
                         instead of printing it; FILE must be new

Without --save it prints only the key. The master key stays in
seats.env and is never printed.
`

// Mint is `lvp mint <seat> [mint-key's options] [--save FILE]`.
func (s *System) Mint(ctx context.Context, argv []string,
	std args.Std) int {
	cmd := s.Prog + " mint"
	usage := s.mintUsage()
	var rest []string
	save := ""
	hasSave := false
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			rest = append(rest, argv[i:]...)
			i = len(argv)
		case a == "-h" || a == "--help":
			fmt.Fprint(std.Out, "usage: "+usage+"\n\n"+mintHelp)
			return 0
		case a == "--save":
			if i+1 >= len(argv) ||
				strings.HasPrefix(argv[i+1], "-") {
				return usageError(std, cmd, usage, "argument "+
					"--save: expected one argument")
			}
			i++
			save, hasSave = argv[i], true
		case strings.HasPrefix(a, "--save="):
			save, hasSave = a[len("--save="):], true
		case a == "--revoke" || a == "--revoke-seat" ||
			strings.HasPrefix(a, "--revoke="),
			strings.HasPrefix(a, "--revoke-seat="):
			return usageError(std, cmd, usage, "to revoke, use "+
				s.Prog+" revoke")
		case a == "--version":
			return usageError(std, cmd, usage, "unrecognized "+
				"arguments: --version")
		default:
			rest = append(rest, a)
		}
	}
	if hasSave && save == "" {
		return usageError(std, cmd, usage, "argument --save: "+
			"expected one argument")
	}
	if len(rest) == 0 {
		return usageError(std, cmd, usage, "the seat name is missing")
	}
	file, err := s.seatsFile()
	if err != nil {
		return fail(std, err.Error())
	}
	if hasSave {
		// Made now, empty and private, so that it is surely new and
		// can be written before a key exists.
		if err := claim(save, s.Prog); err != nil {
			return fail(std, err.Error())
		}
	}
	r := mintkey.Exec(ctx, file, rest, mintTimeout)
	if r.Code != 0 {
		if hasSave {
			fsx.Remove(save)
		}
		writeMintErr(std.Err, r, usage)
		return r.Code
	}
	key := strings.TrimSpace(r.Stdout)
	if !hasSave {
		fmt.Fprintln(std.Out, key)
		return 0
	}
	if err := fsx.WriteFile(save, []byte(key+"\n"), true); err != nil {
		fsx.Remove(save)
		msg := "The key was made, but cannot be saved in " + save +
			": " + plainErr(err) + "."
		back := mintkey.Exec(context.Background(), file,
			[]string{"--revoke", key}, mintTimeout)
		if back.Code == 0 {
			msg += " It was revoked again; mint once more, with " +
				"another --save FILE."
		} else {
			msg += " Revoke it with " + s.Prog + " revoke " +
				rest[0] + ", then mint again."
		}
		return fail(std, msg)
	}
	say(std.Out, "Saved the key for seat "+rest[0]+" in "+save+
		"; only you may open it. Give it to the seat privately "+
		"(README.md, Step 7).")
	return 0
}

// claim makes path, new and empty, owner only; an error if it is
// there already.
func claim(path, prog string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return errors.New(path + " is there already, so nothing was " +
			"minted: it may hold a key in use. Pick another file " +
			"name, or revoke that key first (" + prog +
			" revoke --key-file " + path + ") and delete the file.")
	}
	if err != nil {
		return errors.New("cannot make " + path + ": " + plainErr(err))
	}
	f.Close()
	if err := fsx.Private(path); err != nil {
		fsx.Remove(path)
		return errors.New("cannot make " + path + " private: " +
			plainErr(err))
	}
	return nil
}

// writeMintErr prints mint-key's error, with lvp's usage in place of
// mint-key's own.
func writeMintErr(w io.Writer, r mintkey.Result, usage string) {
	if r.Code == 2 && strings.TrimSpace(r.Stderr) ==
		strings.TrimSpace(mintkey.Usage()) {
		fmt.Fprint(w, "usage: "+usage+"\n\n"+mintHelp)
		return
	}
	io.WriteString(w, fold(r.Stderr))
}

// fold is mint-key's text with each line folded at 80 columns.
func fold(text string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(text, "\n"),
		"\n") {
		if l == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(wrap("", l))
	}
	return b.String()
}

// Revoke is `lvp revoke <seat> | --key-file FILE | --key KEY`.
func (s *System) Revoke(ctx context.Context, argv []string,
	std args.Std) int {
	cmd := s.Prog + " revoke"
	usage := cmd + " <seat> | --key-file FILE | --key KEY"
	fl := args.New(cmd, usage)
	keyFile := fl.String("key-file", "", "FILE", "",
		"revoke the key saved in FILE, as lvp mint --save made it")
	key := fl.String("key", "", "KEY", "", "revoke this key")
	pos, err := fl.Parse(argv)
	if err != nil {
		if errors.Is(err, args.ErrHelp) {
			fmt.Fprint(std.Out, fl.Help()+revokeHelp)
			return 0
		}
		return args.Exit(std, fl, err)
	}
	given := len(pos)
	if *keyFile != "" {
		given++
	}
	if *key != "" {
		given++
	}
	if given != 1 {
		return usageError(std, cmd, usage, "give one seat name, or "+
			"--key-file, or --key")
	}
	var mk []string
	switch {
	case len(pos) == 1:
		if strings.HasPrefix(pos[0], "sk-") {
			return usageError(std, cmd, usage, "that looks "+
				"like a key, not a seat name: use --key, or "+
				"--key-file")
		}
		mk = []string{"--revoke-seat", pos[0]}
	case *keyFile != "":
		k, err := readKey(*keyFile)
		if err != nil {
			return fail(std, err.Error())
		}
		mk = []string{"--revoke", k}
	default:
		mk = []string{"--revoke", *key}
	}
	file, err := s.seatsFile()
	if err != nil {
		return fail(std, err.Error())
	}
	r := mintkey.Exec(ctx, file, mk, mintTimeout)
	if r.Code != 0 {
		writeMintErr(std.Err, r, usage)
		return r.Code
	}
	io.WriteString(std.Out, r.Stdout)
	if *keyFile != "" {
		say(std.Out, "That key no longer works; "+*keyFile+" may go "+
			"now (delete it).")
	}
	return 0
}

const revokeHelp = `
Revokes one key: by its seat name (this works even if the key is
lost), by the file it was saved in, or by the key itself. Every other
key keeps working.
`

// readKey is the one key saved in path, as `mint --save` wrote it.
func readKey(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New("cannot read " + path + ": " +
			plainErr(err) + ". Revoke by seat name instead.")
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 4096))
	k := strings.TrimSpace(strings.TrimPrefix(string(b), bom))
	if !strings.HasPrefix(k, "sk-") || strings.ContainsAny(k,
		" \t\r\n") {
		return "", errors.New(path + " does not hold one key (one " +
			"line that starts with sk-). Revoke by seat name " +
			"instead.")
	}
	return k, nil
}
