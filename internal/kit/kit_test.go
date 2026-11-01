package kit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirFrom(t *testing.T) {
	proxy, other := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(proxy, Compose), nil, 0o644)
	if d, err := DirFrom(proxy, other); err != nil || d != proxy {
		t.Errorf("current folder: %s %v", d, err)
	}
	if d, err := DirFrom(other, proxy); err != nil || d != proxy {
		t.Errorf("program's folder: %s %v", d, err)
	}
	_, err := DirFrom(other, other)
	var nf *NotFoundError
	if !errors.As(err, &nf) || len(nf.Looked) != 1 || err.Error() !=
		"cannot find compose.yaml: run lvp in the proxy's folder, the "+
			"one you unpacked or cloned (looked in "+other+")" {
		t.Errorf("same folder twice: %v", err)
	}
	third := t.TempDir()
	_, err = DirFrom(other, third)
	if err == nil || err.Error() != "cannot find compose.yaml: run lvp "+
		"in the proxy's folder, the one you unpacked or cloned "+
		"(looked "+
		"in "+other+" and "+third+")" {
		t.Errorf("two folders: %v", err)
	}
	os.Mkdir(filepath.Join(third, Compose), 0o755)
	if _, err := DirFrom(third, ""); err == nil {
		t.Error("a folder named compose.yaml counts")
	}
	if _, err := Dir(); err == nil {
		t.Log("Dir found one; fine where tests run in a proxy folder")
	}
}
