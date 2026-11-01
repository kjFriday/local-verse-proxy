//go:build !windows

package game

import (
	"errors"
	"syscall"
)

// isReset: the other side reset the connection.
func isReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED)
}
