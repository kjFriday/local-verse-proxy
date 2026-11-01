//go:build !windows

package pyfmt

import "syscall"

// Go's errno table is the C library's, first letter lowered.
func errnoText(en syscall.Errno) string { return capital(en.Error()) }

func notFoundText() string { return errnoText(syscall.ENOENT) }

func cErrno(en syscall.Errno) (int, string) {
	return int(en), errnoText(en)
}
