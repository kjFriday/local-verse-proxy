package game

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestResetWhileOpen: on Windows, office.db held open by another
// program (here with no sharing at all) cannot be removed. reset
// retries for 2 s, then says so plainly (ledger O-11).
func TestResetWhileOpen(t *testing.T) {
	root := rulesFolder(t)
	db := filepath.Join(root, "game", "office.db")
	os.WriteFile(db, nil, 0o644)
	p, _ := syscall.UTF16PtrFromString(db)
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(h)
	exe, _ := os.Executable()
	t.Setenv("GAMETEST_AS", "mint")
	t.Setenv("GOLDEN_MINT_LOG", filepath.Join(t.TempDir(), "log"))
	t.Setenv("GOLDEN_MINT", `{"*": {"err": "No keys found", "code": 1}}`)
	start := time.Now()
	out, errs, code := runGame(t, Options{Dir: root, Prog: "lvp",
		MintArgv: []string{exe}}, "reset")
	want := "game/office.db is in use by another program, likely a " +
		"running lvp game run or Front Desk. Stop it, then run lvp " +
		"game " +
		"reset again.\n"
	if code != 1 || errs != want {
		t.Errorf("%d %q %q", code, out, errs)
	}
	if time.Since(start) < 2*time.Second {
		t.Errorf("gave up after %v", time.Since(start))
	}
	if _, err := os.Stat(db); err != nil {
		t.Errorf("office.db: %v", err)
	}
}
