package game

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
)

// TestResetWhileOpen: on Windows, office.db held open by another
// program cannot be removed. The handle here shares reading and
// writing but not deleting, as SQLite's own does in a running game
// or Front Desk, so reset can read the file but not remove it. It
// retries for 2 s, then says so plainly (ledger O-11).
func TestResetWhileOpen(t *testing.T) {
	root := rulesFolder(t)
	db := filepath.Join(root, "game", "office.db")
	made, err := gamestate.Create(db)
	if err != nil {
		t.Fatal(err)
	}
	made.Close()
	p, _ := syscall.UTF16PtrFromString(db)
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil,
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
