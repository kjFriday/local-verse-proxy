//go:build !windows

package cli

import "github.com/kjFriday/local-verse-proxy/internal/args"

// pauseIfClicked does nothing here: only Windows starts lvp in a
// window of its own when someone double-clicks it.
func pauseIfClicked(std args.Std) {}
