package fsx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := WriteFile(p, []byte("one\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(p, []byte("two\n"), false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "two\n" {
		t.Fatalf("got %q", b)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temporary files left behind: %v", ents)
	}
	if err := WriteFile(filepath.Join(dir, "no", "x"), nil,
		false); err == nil {
		t.Fatal("wrote into a missing folder")
	}
}

func TestPrivate(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "keys")
	if err := os.Mkdir(key, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Private(key); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(key, "game-fern.key")
	if err := WriteFile(p, []byte("sk-x\n"), true); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{key, p} {
		ok, why, err := IsPrivate(q)
		if !ok || err != nil {
			t.Errorf("%s: not private: %s %v", q, why, err)
		}
	}
	open := filepath.Join(dir, "open.txt")
	os.WriteFile(open, nil, 0o644)
	if runtime.GOOS != "windows" {
		os.Chmod(open, 0o644)
	}
	ok, why, err := IsPrivate(open)
	if ok || why == "" || err != nil {
		t.Errorf("a shared file reads as private: %v %q %v", ok, why,
			err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		dst, _ := os.Stat(key)
		if st.Mode().Perm() != 0o600 || dst.Mode().Perm() != 0o700 {
			t.Errorf("modes %v %v", st.Mode(), dst.Mode())
		}
		if why != "its mode is 0644, so other users may read it" {
			t.Errorf("why: %q", why)
		}
	}
	// A rewrite of a private file stays private.
	if err := WriteFile(p, []byte("sk-y\n"), true); err != nil {
		t.Fatal(err)
	}
	if ok, why, _ := IsPrivate(p); !ok {
		t.Errorf("rewritten: %s", why)
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	if err := Remove(filepath.Join(dir, "none")); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	for _, n := range []string{"office.db", "office.db-journal", "keep"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	if err := RemoveGlob(filepath.Join(dir, "office.db*")); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != "keep" {
		t.Fatalf("left: %v", ents)
	}
	if InUse(os.ErrNotExist) || InUse(nil) {
		t.Fatal("InUse of a plain error")
	}
}

// The folder part of RemoveGlob's pattern is taken as it is: a folder
// whose name has [ in it works, as Python's folder.glob("office.db*").
func TestRemoveGlobOddFolder(t *testing.T) {
	names := []string{"proxy [old]", "proxy[1]", "plain"}
	if runtime.GOOS != "windows" { // Windows allows no * or ? in names
		names = append(names, "a*b?")
	}
	for _, name := range names {
		dir := filepath.Join(t.TempDir(), name, "game")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"office.db", "office.db-journal",
			"rules.toml"} {
			os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
		}
		err := RemoveGlob(filepath.Join(dir, "office.db*"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ents, _ := os.ReadDir(dir)
		if len(ents) != 1 || ents[0].Name() != "rules.toml" {
			t.Errorf("%s: left %v", name, ents)
		}
	}
	if err := RemoveGlob(filepath.Join(t.TempDir(), "none",
		"office.db*")); err != nil {
		t.Errorf("a missing folder: %v", err)
	}
	if err := RemoveGlob(filepath.Join(t.TempDir(), "[")); err == nil {
		t.Error("a bad pattern was taken")
	}
}
