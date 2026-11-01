package pyfmt

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// StrError is the C library's words: "No such file or directory",
// "Address already in use", "Permission denied", ... On Windows,
// Windows' own words, as Python shows them (no trailing dot).
func StrError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, exec.ErrNotFound) {
		return notFoundText()
	}
	var en syscall.Errno
	if errors.As(err, &en) {
		return errnoText(en)
	}
	return err.Error()
}

// OSErrorText is str(OSError): "[Errno 2] No such file or
// directory: 'game/rules.toml'". On Windows a file error reads as
// Python's open() reports it there, with the C library's number.
func OSErrorText(err error, path string) string {
	var en syscall.Errno
	if !errors.As(err, &en) {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	code, text := cErrno(en)
	return "[Errno " + strconv.Itoa(code) + "] " + text + ": " +
		ReprString(path)
}

// capital is the C library's spelling of Go's lowercased errno text.
func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
