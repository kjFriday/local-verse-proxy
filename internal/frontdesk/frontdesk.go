// Owner: C2

// Package frontdesk is Front Desk: the web page for the proxy's key
// cards and tally (lvp desk). Until chunk C2 lands it only says so.
package frontdesk

import (
	"fmt"
	"io"

	"github.com/kjFriday/local-verse-proxy/internal/args"
)

// Main is `lvp desk`.
func Main(argv []string, std args.Std) int {
	say := func(w io.Writer) {
		fmt.Fprintln(w, "lvp desk: not built yet. For now, run "+
			"python3 gui/frontdesk.py (gui/README.md).")
	}
	if len(argv) > 0 && (argv[0] == "--help" || argv[0] == "-h") {
		say(std.Out)
		return 0
	}
	say(std.Err)
	return 1
}
