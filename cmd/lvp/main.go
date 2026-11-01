// Command lvp is local-verse-proxy's one program: Front Desk, the
// office game, mint-key and the practice model server. Run lvp help.
package main

import (
	"os"

	"github.com/kjFriday/local-verse-proxy/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() { os.Exit(cli.Main(os.Args[1:], version)) }
