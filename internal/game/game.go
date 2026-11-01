// Owner: C3

// Package game is the office game on top of the proxy (lvp game).
// Until chunk C3 lands it only says so.
package game

import (
	"fmt"
	"io"

	"github.com/kjFriday/local-verse-proxy/internal/args"
)

// Main is `lvp game`.
func Main(argv []string, std args.Std) int {
	say := func(w io.Writer) {
		fmt.Fprintln(w, "lvp game: not built yet. For now, run "+
			"python3 game/office.py (game/README.md).")
	}
	if len(argv) > 0 && (argv[0] == "--help" || argv[0] == "-h") {
		say(std.Out)
		return 0
	}
	say(std.Err)
	return 1
}
