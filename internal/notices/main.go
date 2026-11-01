// Command notices writes THIRD_PARTY_NOTICES.txt for a built program:
// Go's own license, then each module the program links, with its
// license file from the module cache. It fails if a module has none.
//
//	go run ./internal/notices -bin lvp > THIRD_PARTY_NOTICES.txt
package main

import (
	"bytes"
	"debug/buildinfo"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func main() {
	bin := flag.String("bin", "", "the built program to read")
	flag.Parse()
	if *bin == "" || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: go run ./internal/notices "+
			"-bin <program> > THIRD_PARTY_NOTICES.txt")
		os.Exit(2)
	}
	if err := run(*bin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

type module struct{ path, version string }

func run(bin string, w io.Writer) error {
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		return err
	}
	goroot, cache, err := goEnv()
	if err != nil {
		return err
	}
	var mods []module
	for _, d := range info.Deps {
		if d.Replace != nil {
			d = d.Replace
		}
		mods = append(mods, module{d.Path, d.Version})
	}
	sort.Slice(mods, func(i, j int) bool {
		return mods[i].path < mods[j].path
	})

	var b bytes.Buffer
	fmt.Fprintf(&b, "Third-party notices for %s\n\n", filepath.Base(bin))
	b.WriteString("This program is built with Go and links the " +
		"modules below. Each\none's license follows, as its authors " +
		"wrote it.\n")
	goLicense, err := os.ReadFile(filepath.Join(goroot, "LICENSE"))
	if err != nil {
		return fmt.Errorf("Go's license: %w", err)
	}
	section(&b, "Go "+info.GoVersion, "the Go standard library and "+
		"runtime", goLicense)
	for _, m := range mods {
		dir := filepath.Join(cache,
			escape(m.path)+"@"+escape(m.version))
		texts, err := licenses(dir)
		if err != nil {
			return fmt.Errorf("%s %s: %w", m.path, m.version, err)
		}
		for _, t := range texts {
			what := m.path + " " + m.version
			if len(texts) > 1 {
				what += " (" + t.name + ")"
			}
			section(&b, what, "", t.text)
		}
	}
	_, err = w.Write(b.Bytes())
	return err
}

func section(b *bytes.Buffer, title, about string, text []byte) {
	b.WriteString("\n" + strings.Repeat("=", 72) + "\n" + title + "\n")
	if about != "" {
		b.WriteString(about + "\n")
	}
	b.WriteString(strings.Repeat("=", 72) + "\n\n")
	b.Write(bytes.TrimRight(text, "\n"))
	b.WriteString("\n")
}

type license struct {
	name string
	text []byte
}

// licenses are the license and notice files at a module's top.
func licenses(dir string) ([]license, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []license
	for _, e := range ents {
		n := strings.ToUpper(e.Name())
		if e.IsDir() || !(strings.HasPrefix(n, "LICENSE") ||
			strings.HasPrefix(n, "LICENCE") ||
			strings.HasPrefix(n, "COPYING") ||
			strings.HasPrefix(n, "NOTICE")) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, license{e.Name(), b})
	}
	if len(out) == 0 {
		return nil, errors.New("no license file in " + dir)
	}
	return out, nil
}

func goEnv() (goroot, cache string, err error) {
	out, err := exec.Command("go", "env", "GOROOT", "GOMODCACHE").Output()
	if err != nil {
		return "", "", fmt.Errorf("go env: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", "", errors.New("go env gave no GOROOT and " +
			"GOMODCACHE")
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
}

// escape is the module cache's spelling: each capital letter becomes
// ! and the letter in lower case.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsUpper(r) {
			b.WriteByte('!')
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
