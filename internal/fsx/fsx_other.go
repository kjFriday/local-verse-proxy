//go:build !windows

package fsx

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// create makes the temporary file: owner only from the start when
// private, with the replaced file's mode, or 0666 less the umask.
func create(name string, private bool, _ string,
	like fs.FileInfo) (*os.File, error) {
	perm := fs.FileMode(0o666)
	switch {
	case private:
		perm = 0o600
	case like != nil:
		perm = like.Mode().Perm()
	}
	return os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
}

func private(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if st.IsDir() {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}

func isPrivate(path string) (bool, string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return false, "", err
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok &&
		int(sys.Uid) != os.Getuid() {
		return false, "it belongs to another user, who may read it " +
			"and change who else may", nil
	}
	perm := st.Mode().Perm()
	if perm&0o077 == 0 {
		return true, "", nil
	}
	who := "other users"
	if perm&0o007 == 0 {
		who = "its group"
	}
	return false, fmt.Sprintf("its mode is %04o, so %s may read it",
		perm, who), nil
}

// Linux and macOS let a program remove or rename a file another one
// has open.
func inUse(err error) bool { return false }
