package game

import (
	"errors"
	"syscall"
)

// isReset: the other side reset the connection (WSAECONNRESET,
// WSAECONNABORTED).
func isReset(err error) bool {
	var en syscall.Errno
	if errors.As(err, &en) {
		return en == 10054 || en == 10053
	}
	return false
}
