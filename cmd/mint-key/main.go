// Command mint-key mints and revokes seat keys, like mint-key.sh. It
// ships for one release next to lvp, for scripts that call it by this
// name: it prints exactly what `lvp mint-key` prints.
//
//	mint-key <seat> [--models a,b] [--budget USD] [--duration 30d]
//	                [--budget-duration 1d]
//	mint-key --revoke <key>
//	mint-key --revoke-seat <seat>
//
// The logic lives in internal/mintkey.
package main

import (
	"os"

	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	mintkey.Version = version
	os.Exit(mintkey.Main(os.Args[1:], mintkey.OS(), os.Stdout,
		os.Stderr))
}
