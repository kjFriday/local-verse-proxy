// Package fsx writes and removes files safely on Windows, macOS and
// Linux: writes go through a temporary file and a rename, so a reader
// never sees half a file; removes and renames wait up to RetryFor while
// another program has the file open (Windows refuses them then); and
// Private makes a file or folder its owner's only.
package fsx

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// RetryFor is how long a remove or rename waits for another program
// to let go of a file.
const RetryFor = 2 * time.Second

// retryGap is the pause between tries.
const retryGap = 50 * time.Millisecond

// WriteFile writes through a temporary file in the same folder and
// a rename, retried for RetryFor while the target is in use.
// private: owner only (Private) from the moment the temporary file
// exists. Otherwise a file being replaced keeps its mode (Linux,
// macOS) or its own access list (Windows), so a private file stays
// private; a new file gets 0666 less the umask, as Python's open().
func WriteFile(path string, data []byte, private bool) error {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	like, err := os.Stat(path)
	if err != nil {
		like = nil
	}
	tmp, err := createTemp(dir, base, private, path, like)
	if err != nil {
		return err
	}
	name := tmp.Name()
	done := false
	defer func() {
		if !done {
			os.Remove(name)
		}
	}()
	if afterCreate != nil {
		afterCreate(name)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := finish(name, private, like); err != nil {
		return err
	}
	rename := func() error { return os.Rename(name, path) }
	if err := retry(rename); err != nil {
		return err
	}
	done = true
	return nil
}

// afterCreate, when a test sets it, runs between making the
// temporary file and writing to it.
var afterCreate func(tmp string)

// createTemp makes a new temporary file ".<base>.<random>.tmp" in
// dir, never one that was there.
func createTemp(dir, base string, private bool, target string,
	like fs.FileInfo) (*os.File, error) {
	for try := 0; ; try++ {
		var b [6]byte
		rand.Read(b[:])
		name := filepath.Join(dir,
			"."+base+"."+hex.EncodeToString(b[:])+".tmp")
		f, err := create(name, private, target, like)
		if errors.Is(err, fs.ErrExist) && try < 100 {
			continue
		}
		return f, err
	}
}

// finish gives the temporary file its last mode before the rename.
func finish(name string, private bool, like fs.FileInfo) error {
	switch {
	case private:
		return Private(name) // made so; this is the second lock
	case like != nil:
		return os.Chmod(name, like.Mode().Perm())
	}
	return nil
}

// Remove removes path; a missing file is fine; retried for
// RetryFor while another program has it open (Windows).
func Remove(path string) error {
	return retry(func() error {
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	})
}

// RemoveGlob removes every file the pattern matches, as Remove does,
// stopping at the first that cannot go. Only the last part of the
// pattern is a pattern, as Python's folder.glob("office.db*"): the
// folder is taken as it is, so one named "proxy [old]" works. On
// Windows the match ignores case, as there.
func RemoveGlob(pattern string) error {
	dir, base := filepath.Split(pattern)
	if dir == "" {
		dir = "."
	}
	if _, err := filepath.Match(base, ""); err != nil {
		return err
	}
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !match(base, e.Name()) {
			continue
		}
		if err := Remove(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func match(pattern, name string) bool {
	if runtime.GOOS == "windows" {
		pattern, name = strings.ToLower(pattern), strings.ToLower(name)
	}
	ok, _ := filepath.Match(pattern, name)
	return ok
}

func retry(op func() error) error {
	end := time.Now().Add(RetryFor)
	for {
		err := op()
		if err == nil || !InUse(err) || time.Now().After(end) {
			return err
		}
		time.Sleep(retryGap)
	}
}

// IsPrivate says whether only the owner may open path: owned by this
// user, with mode 0600 or 0700 and nothing for group or others, on
// Linux and macOS; on Windows owned by this user (or SYSTEM or the
// Administrators), with a protected access list that lets in only the
// current user and SYSTEM. why says what lets others in, in plain
// words. On macOS only the mode is read: an extended access list
// (chmod +a) is not seen.
func IsPrivate(path string) (ok bool, why string, err error) {
	return isPrivate(path)
}

// Private: mode 0600 (0700 for a folder) on Linux and macOS; on
// Windows a protected access list: the current user and SYSTEM,
// full control, nobody else.
func Private(path string) error { return private(path) }

// InUse: Windows' sharing violation, or access denied while open.
func InUse(err error) bool { return inUse(err) }
