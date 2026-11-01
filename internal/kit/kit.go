// Package kit finds the proxy folder: the one with compose.yaml in
// it, which lvp came in or was cloned into.
package kit

import (
	"os"
	"path/filepath"
	"strings"
)

// Compose is the file that marks the proxy folder.
const Compose = "compose.yaml"

// Dir is the proxy folder: the current folder if it has
// compose.yaml, else the folder lvp is in, if that has it.
func Dir() (string, error) {
	wd, _ := os.Getwd()
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		if p, err := filepath.EvalSymlinks(exe); err == nil {
			exe = p
		}
		exeDir = filepath.Dir(exe)
	}
	return DirFrom(wd, exeDir)
}

// DirFrom is Dir for a given current folder and program folder; either
// may be "".
func DirFrom(wd, exeDir string) (string, error) {
	var looked []string
	for _, d := range []string{wd, exeDir} {
		if d == "" || len(looked) > 0 && sameDir(looked[0], d) {
			continue
		}
		looked = append(looked, d)
		st, err := os.Stat(filepath.Join(d, Compose))
		if err == nil && st.Mode().IsRegular() {
			return d, nil
		}
	}
	return "", &NotFoundError{Looked: looked}
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

// NotFoundError says no folder looked at has compose.yaml.
type NotFoundError struct{ Looked []string }

// Error: "cannot find compose.yaml: run lvp in the proxy's folder,
// the one you unpacked or cloned (looked in ...)".
func (e *NotFoundError) Error() string {
	msg := "cannot find " + Compose + ": run lvp in the proxy's " +
		"folder, the one you unpacked or cloned"
	if len(e.Looked) > 0 {
		msg += " (looked in " + strings.Join(e.Looked, " and ") + ")"
	}
	return msg
}
