package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEscape(t *testing.T) {
	if got := escape("github.com/BurntSushi/toml"); got !=
		"github.com/!burnt!sushi/toml" {
		t.Fatal(got)
	}
}

// The notices for a program that links SQLite, pgx and the TOML reader:
// Go's license first, then each module with its own license text.
func TestNotices(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	bin := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/prog")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var b bytes.Buffer
	if err := run(bin, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"\nGo go1.", "Copyright",
		"\nmodernc.org/sqlite v1.60.1", "\nmodernc.org/libc v1.77.1",
		"\ngithub.com/jackc/pgx/v5 v5.11.0",
		"\ngithub.com/BurntSushi/toml v1.6.0",
		"Redistribution and use in source and binary forms"} {
		if !strings.Contains(out, want) {
			t.Errorf("notices lack %q", want)
		}
	}
	if strings.Index(out, "\nGo go1.") > strings.Index(out,
		"\nmodernc.org/") {
		t.Error("Go's license is not first")
	}
	if _, err := licenses(t.TempDir()); err == nil {
		t.Error("a module without a license passed")
	}
}
