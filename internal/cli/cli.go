// Package cli is lvp's command table: which command runs which Main,
// the help, and the version.
package cli

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/fakeollama"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk"
	"github.com/kjFriday/local-verse-proxy/internal/game"
	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
	"github.com/kjFriday/local-verse-proxy/internal/setup"
)

// Name is the program's name, in help and messages.
const Name = "lvp"

type command struct {
	name, help string
	run        func(argv []string, std args.Std) int
	helper     bool // one of C5's helpers: shown once ready
}

var commands = []command{
	{"desk", "Front Desk: the web page for key cards", frontdesk.Main,
		false},
	{"game", "the office game: setup, run, board, reset", runGame,
		false},
	{"mint-key", "mint or revoke a seat key, as the mint-key program",
		runMintKey, false},
	{"fake-ollama", "the practice model server (in Docker)",
		fakeollama.Main, false},
	{"init", "make seats.env, with its secrets", setup.Init, true},
	{"check", "check the setup, step by step", setup.Check, true},
	{"wait", "wait until the proxy answers", setup.Wait, true},
	{"test", "try the proxy with a throwaway seat key", setup.Test,
		true},
	{"mint", "mint a seat key, and save it", setup.Mint, true},
	{"revoke", "revoke a seat key", setup.Revoke, true},
	{"env", "set or unset a line in Compose's .env", setup.Env, true},
}

// version is set by Main for the commands that print it.
var version = "dev"

// Main runs lvp with argv, the arguments after the program's name.
func Main(argv []string, v string) int {
	return run(argv, v, args.OS())
}

func run(argv []string, v string, std args.Std) int {
	version = v
	mintkey.Version = v
	if len(argv) == 0 {
		fmt.Fprint(std.Err, usage())
		pauseIfClicked(std)
		return 2
	}
	switch argv[0] {
	case "-h", "--help":
		fmt.Fprint(std.Out, usage())
		return 0
	case "help":
		if len(argv) > 1 {
			return help(argv[1], std)
		}
		fmt.Fprint(std.Out, usage())
		return 0
	case "version", "--version":
		fmt.Fprintln(std.Out, Name+" "+fullVersion())
		return 0
	}
	if c := find(argv[0]); c != nil {
		return c.run(argv[1:], std)
	}
	return unknown(std, argv[0], argv)
}

// mintFlags are mint-key's options: typed straight after lvp, they
// mean mint-key was left out.
var mintFlags = map[string]bool{"--revoke": true, "--revoke-seat": true,
	"--models": true, "--budget": true, "--duration": true,
	"--budget-duration": true}

// help is `lvp help <name>`: the command's own help, on stdout.
func help(name string, std args.Std) int {
	prog := args.Prog()
	switch name {
	case "mint-key":
		// mint-key reads seats.env first, and calls --help a
		// mistake: show its usage here, as lvp types it.
		fmt.Fprint(std.Out, mintkey.UsageFor(prog+" mint-key"))
		return 0
	case "version":
		fmt.Fprintf(std.Out, "usage: %s version (or %s --version)\n\n"+
			"Prints lvp's version.\n", prog, prog)
		return 0
	case "help":
		fmt.Fprintf(std.Out, "usage: %s help [command]\n\nLists the "+
			"commands, or says how to use one.\n", prog)
		return 0
	}
	if c := find(name); c != nil {
		return c.run([]string{"--help"}, std)
	}
	return unknown(std, name, nil)
}

func find(name string) *command {
	for i := range commands {
		c := &commands[i]
		if c.name == name && (!c.helper || setup.Ready) {
			return c
		}
	}
	return nil
}

// unknown says name is no command. When argv starts with one of
// mint-key's options, it also shows the line with mint-key put in.
func unknown(std args.Std, name string, argv []string) int {
	prog := args.Prog()
	fmt.Fprintf(std.Err, "%s: no command %q. To see them all, run: "+
		"%s help\n", Name, name, prog)
	if len(argv) > 0 && mintFlags[argv[0]] {
		line := prog + " mint-key " + strings.Join(argv, " ")
		if strings.ContainsAny(strings.Join(argv, ""), " \t\"'") {
			line = prog + " mint-key " + argv[0] + " ..."
		}
		fmt.Fprintf(std.Err, "Did you mean: %s\n", line)
	}
	return 2
}

func runGame(argv []string, std args.Std) int {
	if len(argv) > 0 && argv[0] == "practice" && setup.Ready {
		return setup.Practice(argv[1:], std)
	}
	return game.Main(argv, std)
}

func runMintKey(argv []string, std args.Std) int {
	sys := mintkey.OS()
	sys.Cmd = args.Prog() + " mint-key"
	return mintkey.Main(argv, sys, std.Out, std.Err)
}

func usage() string {
	var b strings.Builder
	b.WriteString(Name + ": local-verse-proxy's program. One lock, " +
		"many keys.\n\nusage: " + Name + " <command> [options]\n\n" +
		"commands:\n")
	for _, c := range commands {
		if c.helper && !setup.Ready {
			continue
		}
		fmt.Fprintf(&b, "  %-12s %s\n", c.name, c.help)
	}
	fmt.Fprintf(&b, "  %-12s %s\n", "version", "print the version")
	fmt.Fprintf(&b, "  %-12s %s\n", "help", "this text; "+Name+
		" help <command> for one command")
	b.WriteString("\nType ./" + Name + " on Linux and macOS, ." +
		`\` + Name + " in PowerShell, " + Name + " in cmd.\n")
	return b.String()
}

func fullVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok &&
		bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}
