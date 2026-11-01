//go:build windows

package fsx

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestMain doubles as the program that holds a file open: with
// FSX_HOLD set, it opens that file without FILE_SHARE_DELETE, prints
// "held", and keeps it for FSX_HOLD_MS.
func TestMain(m *testing.M) {
	if p := os.Getenv("FSX_HOLD"); p != "" {
		hold(p)
		return
	}
	os.Exit(m.Run())
}

func hold(p string) {
	name, _ := windows.UTF16PtrFromString(p)
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		os.Stdout.WriteString("error " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("held\n")
	ms, _ := time.ParseDuration(os.Getenv("FSX_HOLD_MS") + "ms")
	time.Sleep(ms)
	windows.CloseHandle(h)
}

// holdFor starts another process that holds p open for d.
func holdFor(t *testing.T, p string, d time.Duration) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "FSX_HOLD="+p,
		"FSX_HOLD_MS="+itoa(int(d/time.Millisecond)))
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := out.Read(buf); err != nil || string(buf) != "held\n" {
		t.Fatalf("holder: %q %v", buf, err)
	}
	return cmd
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// Windows refuses to remove an open file; Remove waits, and wins once
// the other program lets go.
func TestRemoveWaitsForOpenFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "office.db")
	os.WriteFile(p, []byte("x"), 0o644)
	cmd := holdFor(t, p, 700*time.Millisecond)
	defer cmd.Wait()
	if err := os.Remove(p); err == nil || !InUse(err) {
		t.Fatalf("plain remove of an open file: %v", err)
	}
	start := time.Now()
	if err := Remove(p); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if time.Since(start) > RetryFor {
		t.Fatalf("took %v", time.Since(start))
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("still there")
	}
}

// Held longer than RetryFor: Remove gives up with the in-use error.
func TestRemoveGivesUp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "office.db")
	os.WriteFile(p, []byte("x"), 0o644)
	cmd := holdFor(t, p, RetryFor+2*time.Second)
	defer cmd.Wait()
	start := time.Now()
	err := Remove(p)
	if err == nil || !InUse(err) {
		t.Fatalf("Remove of a held file: %v", err)
	}
	if d := time.Since(start); d < RetryFor || d > RetryFor+time.Second {
		t.Fatalf("gave up after %v; want about %v", d, RetryFor)
	}
}

// WriteFile's rename waits too.
func TestWriteFileWaitsForOpenFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seats.env")
	os.WriteFile(p, []byte("A=1\n"), 0o644)
	cmd := holdFor(t, p, 500*time.Millisecond)
	defer cmd.Wait()
	if err := WriteFile(p, []byte("A=2\n"), true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "A=2\n" {
		t.Fatalf("got %q", b)
	}
}

// The access list: protected, the user and SYSTEM only.
func TestPrivateACL(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.key")
	os.WriteFile(p, []byte("x"), 0o644)
	if ok, _, _ := IsPrivate(p); ok {
		t.Fatal("a new file in the temp folder reads as private")
	}
	if err := Private(p); err != nil {
		t.Fatal(err)
	}
	ok, why, err := IsPrivate(p)
	if !ok || err != nil {
		t.Fatalf("after Private: %s %v", why, err)
	}
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	s := sd.String()
	if !strings.HasPrefix(s, "D:P") || !strings.Contains(s, ";;;SY)") ||
		strings.Count(s, "(A;") != 2 {
		t.Fatalf("access list %s", s)
	}
}

// A private file is the owner's from the moment it exists: while
// WriteFile writes it, its temporary file already has the protected
// list, and another program cannot open it.
func TestWriteFilePrivateFromBirth(t *testing.T) {
	p := filepath.Join(t.TempDir(), "game-fern.key")
	checked := false
	afterCreate = func(tmp string) {
		checked = true
		if ok, why, err := IsPrivate(tmp); !ok || err != nil {
			t.Errorf("the temporary file is not private yet: %s %v",
				why, err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), "FSX_HOLD="+tmp,
			"FSX_HOLD_MS=0")
		out, _ := cmd.Output()
		if !strings.HasPrefix(string(out), "error ") {
			t.Errorf("another program opened the temporary "+
				"file: %q", out)
		}
	}
	defer func() { afterCreate = nil }()
	if err := WriteFile(p, []byte("sk-x\n"), true); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("the hook did not run")
	}
	if ok, why, err := IsPrivate(p); !ok || err != nil {
		t.Fatalf("written: %s %v", why, err)
	}
}

// A private file rewritten with private false keeps its list: a
// rename keeps the moved file's own list, so the new file is made
// with the old one's.
func TestWriteFileKeepsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seats.env")
	os.WriteFile(p, []byte("A=1\n"), 0o644)
	if err := Private(p); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(p, []byte("A=2\n"), false); err != nil {
		t.Fatal(err)
	}
	if ok, why, err := IsPrivate(p); !ok || err != nil {
		t.Fatalf("after a rewrite: %s %v", why, err)
	}
	// And a shared one stays shared, with its folder's list.
	q := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(q, []byte("x"), 0o644)
	if err := WriteFile(q, []byte("y"), false); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := IsPrivate(q); ok {
		t.Fatal("a shared file became private")
	}
}
