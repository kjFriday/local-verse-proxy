//go:build windows

package pyfmt

import (
	"strings"
	"syscall"
)

// Windows' message, trimmed as Python trims it: no trailing dots or
// white space.
func errnoText(en syscall.Errno) string {
	return strings.TrimRight(en.Error(), " \t\r\n.")
}

// Python's subprocess on Windows: [WinError 2] The system cannot find
// the file specified.
func notFoundText() string {
	return errnoText(syscall.ERROR_FILE_NOT_FOUND)
}

// cErrno is the C library errno Python's open() reports for a Windows
// error, with the C library's words (the MSVC runtime's table).
func cErrno(en syscall.Errno) (int, string) {
	switch en {
	case 2, 3, 15, 123: // file, path, drive not found; bad name
		return 2, "No such file or directory"
	case 5, 32, 33: // access denied; sharing or lock violation
		return 13, "Permission denied"
	case 80, 183: // file exists; already exists
		return 17, "File exists"
	case 145: // folder not empty
		return 41, "Directory not empty"
	case 8, 14: // not enough memory
		return 12, "Not enough space"
	case 39, 112: // disk full
		return 28, "No space left on device"
	}
	return 22, "Invalid argument"
}
