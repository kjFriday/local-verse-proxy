// Package seats reads seats.env, the file of NAME=value lines that
// holds the proxy's secrets and the mint defaults.
//
// It reads the file the way bash's "source" would for a file of this
// shape, and refuses anything it cannot read safely, such as $(...),
// which would run a command. Nothing in the file is ever run.
package seats

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Name is the file's usual name.
const Name = "seats.env"

// LookupFunc finds a variable outside the file, such as an environment
// variable. os.LookupEnv is one.
type LookupFunc func(name string) (string, bool)

// File is one seats.env, read.
type File struct {
	Path string // where it was read from, as given

	vals map[string]string
	env  LookupFunc
}

// Find picks the seats.env to use: the path in SEATS_ENV if that is
// set; else seats.env in dir (the current folder); else seats.env in
// exeDir (the program's own folder). exeDir may be empty.
func Find(env LookupFunc, dir, exeDir string) (string, error) {
	if env == nil {
		env = os.LookupEnv
	}
	if p, _ := env("SEATS_ENV"); p != "" {
		if isFile(p) {
			return p, nil
		}
		return "", &MissingError{Tried: []string{p}, FromEnv: true}
	}
	var tried []string
	for i, d := range []string{dir, exeDir} {
		if d == "" || i == 1 && dir != "" && sameDir(dir, d) {
			continue
		}
		p := filepath.Join(d, Name)
		if isFile(p) {
			return p, nil
		}
		tried = append(tried, p)
	}
	if len(tried) == 0 {
		tried = []string{Name}
	}
	return "", &MissingError{Tried: tried}
}

// MissingError says no seats.env was found.
type MissingError struct {
	Tried   []string // the paths looked at, first one first
	FromEnv bool     // the one path came from SEATS_ENV
}

func (e *MissingError) Error() string {
	msg := "missing " + e.Tried[0]
	if e.FromEnv {
		return msg
	}
	for _, p := range e.Tried[1:] {
		msg += "\n(also looked for " + p + ")"
	}
	return msg + "\nMake it as README.md Step 3 shows, or set " +
		"SEATS_ENV to its path."
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

// Load reads the file at path. env answers for names the file uses
// but does not set, like $HOME, and for settings the file leaves out,
// as a shell would. A nil env means the real environment.
func Load(path string, env LookupFunc) (*File, error) {
	if env == nil {
		env = os.LookupEnv
	}
	b, err := os.ReadFile(path)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, fmt.Errorf("cannot read %s: %v", path, err)
	}
	vals, err := Parse(string(b), env)
	if err != nil {
		return nil, fmt.Errorf("%s %w", path, err)
	}
	return &File{Path: path, vals: vals, env: env}, nil
}

// Lookup finds a setting: the file's value if the file sets it, even
// to nothing; else the environment's.
func (f *File) Lookup(name string) (string, bool) {
	if v, ok := f.vals[name]; ok {
		return v, true
	}
	if f.env != nil {
		return f.env(name)
	}
	return "", false
}

// Get returns a setting, or def if it is unset or empty, like
// ${NAME:-def} in a shell.
func (f *File) Get(name, def string) string {
	if v, _ := f.Lookup(name); v != "" {
		return v
	}
	return def
}

// MasterKey returns LITELLM_MASTER_KEY, after the same checks as
// mint-key.sh: it must be set, and neither it nor LITELLM_SALT_KEY may
// still hold the template's change-me.
func (f *File) MasterKey() (string, error) {
	mk := f.Get("LITELLM_MASTER_KEY", "")
	if mk == "" {
		return "", errors.New("LITELLM_MASTER_KEY: not set in " +
			f.Path)
	}
	if strings.Contains(mk+f.Get("LITELLM_SALT_KEY", ""), "change-me") {
		return "", errors.New("replace the change-me values in " +
			f.Path + " first")
	}
	return mk, nil
}
