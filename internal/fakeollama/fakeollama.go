// Owner: C3

// Package fakeollama is the practice model server (lvp fake-ollama),
// which answers like Ollama with made-up text. Until chunk C3 lands
// it only says so.
package fakeollama

import (
	"fmt"
	"io"

	"github.com/kjFriday/local-verse-proxy/internal/args"
)

// Main is `lvp fake-ollama`.
func Main(argv []string, std args.Std) int {
	say := func(w io.Writer) {
		fmt.Fprintln(w, "lvp fake-ollama: not built yet. For now, "+
			"game/fake_ollama.py runs in\nDocker (game/README.md).")
	}
	if len(argv) > 0 && (argv[0] == "--help" || argv[0] == "-h") {
		say(std.Out)
		return 0
	}
	say(std.Err)
	return 1
}
