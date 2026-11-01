//go:build !windows

package fsx

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A private file is 0600 from the moment it exists, not only after
// it is written; a new shared file follows the umask, as Python's
// open(); a replaced file keeps its mode, also with private false.
func TestWriteFileModes(t *testing.T) {
	dir := t.TempDir()
	var seen os.FileMode
	afterCreate = func(tmp string) {
		st, err := os.Stat(tmp)
		if err != nil {
			t.Fatal(err)
		}
		seen = st.Mode().Perm()
	}
	defer func() { afterCreate = nil }()

	key := filepath.Join(dir, "k.key")
	if err := WriteFile(key, []byte("sk-x\n"), true); err != nil {
		t.Fatal(err)
	}
	if seen != 0o600 {
		t.Errorf("a private file's temporary file was %04o", seen)
	}

	old := syscall.Umask(0o077)
	err := WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), false)
	syscall.Umask(old)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "new.txt")); st.Mode().Perm() !=
		0o600 {
		t.Errorf("under umask 077 a new file is %04o; Python's open() "+
			"makes 0600", st.Mode().Perm())
	}

	old = syscall.Umask(0o022)
	err = WriteFile(filepath.Join(dir, "plain.txt"), []byte("x"), false)
	syscall.Umask(old)
	if st, _ := os.Stat(filepath.Join(dir, "plain.txt")); err != nil ||
		st.Mode().Perm() != 0o644 {
		t.Errorf("under umask 022: %v %v", st.Mode(), err)
	}

	if err := WriteFile(key, []byte("sk-y\n"), false); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(key); st.Mode().Perm() != 0o600 || seen != 0o600 {
		t.Errorf("a private file rewritten with private false: %04o, "+
			"its temporary file %04o", st.Mode().Perm(), seen)
	}
}
