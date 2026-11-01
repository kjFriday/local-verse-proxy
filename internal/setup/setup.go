// Owner: C5

// Package setup is the beginner helpers: lvp init, check, wait, test,
// mint, revoke, env and game practice. They come after the port is
// signed off (chunk C5); until then each only says so.
package setup

import (
	"fmt"

	"github.com/kjFriday/local-verse-proxy/internal/args"
)

// Ready is false until the helpers are built; lvp help lists them,
// and lvp runs them, once it is true.
const Ready = false

func notYet(std args.Std, name string) int {
	fmt.Fprintln(std.Err, "lvp "+name+": not built yet.")
	return 1
}

// Init is `lvp init`.
func Init(argv []string, std args.Std) int { return notYet(std, "init") }

// Check is `lvp check`.
func Check(argv []string, std args.Std) int { return notYet(std, "check") }

// Wait is `lvp wait`.
func Wait(argv []string, std args.Std) int { return notYet(std, "wait") }

// Test is `lvp test`.
func Test(argv []string, std args.Std) int { return notYet(std, "test") }

// Mint is `lvp mint`.
func Mint(argv []string, std args.Std) int { return notYet(std, "mint") }

// Revoke is `lvp revoke`.
func Revoke(argv []string, std args.Std) int {
	return notYet(std, "revoke")
}

// Env is `lvp env`.
func Env(argv []string, std args.Std) int { return notYet(std, "env") }

// Practice is `lvp game practice`.
func Practice(argv []string, std args.Std) int {
	return notYet(std, "game practice")
}
