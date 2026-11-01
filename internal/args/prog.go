package args

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Prog is how to type lvp here, for messages: "./lvp" or ".\lvp"
// when it sits in the current folder, "lvp" when found on PATH,
// else its path (quoted when it has a space, or on Linux and macOS
// anything else a shell would read as code).
func Prog() string {
	exe, err := os.Executable()
	if err != nil {
		return "lvp"
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	wd, _ := os.Getwd()
	onPath := false
	found, err := exec.LookPath(baseName(runtime.GOOS, exe))
	if err == nil {
		if p, err := filepath.Abs(found); err == nil {
			if q, err := filepath.EvalSymlinks(p); err == nil {
				p = q
			}
			onPath = samePath(runtime.GOOS, p, exe)
		}
	}
	return ProgFor(runtime.GOOS, exe, wd, onPath)
}

// ProgFor is Prog for a given system, program path, current folder,
// and whether PATH finds that same program by its name.
func ProgFor(goos, exe, wd string, onPath bool) string {
	name := baseName(goos, exe)
	dir := parent(goos, exe)
	switch {
	case wd != "" && samePath(goos, dir, wd):
		if goos == "windows" {
			return `.\` + name
		}
		return "./" + name
	case onPath:
		return name
	case goos == "windows" && strings.ContainsAny(exe, " \t&()"):
		return `"` + exe + `"`
	case goos != "windows" && strings.ContainsAny(exe,
		" \t'\"$`\\;&|()<>*?[]#~!{}"):
		// A shell would split it or read it as code.
		return "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
	}
	return exe
}

// baseName is the program's name as typed: lvp, not lvp.exe.
func baseName(goos, exe string) string {
	i := strings.LastIndexAny(exe, sepsFor(goos))
	name := exe[i+1:]
	if goos == "windows" && strings.HasSuffix(strings.ToLower(name),
		".exe") {
		name = name[:len(name)-4]
	}
	return name
}

func parent(goos, p string) string {
	i := strings.LastIndexAny(p, sepsFor(goos))
	if i < 0 {
		return ""
	}
	if i == 0 || goos == "windows" && i == 2 && p[1] == ':' {
		return p[:i+1]
	}
	return p[:i]
}

func sepsFor(goos string) string {
	if goos == "windows" {
		return `\/`
	}
	return "/"
}

func samePath(goos, a, b string) bool {
	clean := func(p string) string {
		if goos == "windows" {
			p = strings.ToLower(strings.ReplaceAll(p, "/", `\`))
			return strings.TrimRight(p, `\`)
		}
		if len(p) > 1 {
			p = strings.TrimRight(p, "/")
		}
		return p
	}
	return clean(a) == clean(b)
}
