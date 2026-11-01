package seats

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestFromEnviron(t *testing.T) {
	f := FromEnviron([]string{"LITELLM_MASTER_KEY=sk-1", "A=x=y",
		"EMPTY=", "A=second", "=C:=C:\\x", "NOEQUALS",
		"DEFAULT_MODELS=local, pro"})
	if f.Path != "" {
		t.Fatalf("Path %q", f.Path)
	}
	if v, ok := f.Lookup("A"); !ok || v != "x=y" {
		t.Errorf("A = %q %v (the first wins)", v, ok)
	}
	if v, ok := f.Lookup("EMPTY"); !ok || v != "" {
		t.Errorf("EMPTY = %q %v", v, ok)
	}
	if _, ok := f.Lookup("NOEQUALS"); ok {
		t.Error("a line with no = counts")
	}
	if f.Get("EMPTY", "d") != "d" || f.Get("DEFAULT_MODELS", "") !=
		"local, pro" {
		t.Error("Get")
	}
	if mk, err := f.MasterKey(); err != nil || mk != "sk-1" {
		t.Errorf("MasterKey %q %v", mk, err)
	}
	want := []string{"LITELLM_MASTER_KEY", "A", "EMPTY", "=C:",
		"DEFAULT_MODELS"}
	if got := f.Names(); !reflect.DeepEqual(got, want) {
		t.Errorf("Names %q", got)
	}
	_, ok := f.Lookup("litellm_master_key")
	if ok != (runtime.GOOS == "windows") {
		t.Errorf("case of names: found %v", ok)
	}
}

func TestNames(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seats.env")
	os.WriteFile(p, []byte("# c\nB=1\nexport A=2 C=3\nB=4\nexport D\n"),
		0o600)
	f, err := Load(p, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Names(); !reflect.DeepEqual(got, []string{"B", "A",
		"C"}) {
		t.Errorf("Names %q", got)
	}
	if v, _ := f.Lookup("B"); v != "4" {
		t.Errorf("B = %q", v)
	}
}

// MasterKey's messages name the environment, not an empty file name.
func TestFromEnvironMasterKeyWords(t *testing.T) {
	_, err := FromEnviron([]string{"PROXY_URL=http://proxy:4000"}).
		MasterKey()
	if err == nil || err.Error() !=
		"LITELLM_MASTER_KEY: not set in the environment" {
		t.Errorf("unset: %v", err)
	}
	_, err = FromEnviron([]string{"LITELLM_MASTER_KEY=sk-a",
		"LITELLM_SALT_KEY=sk-change-me"}).MasterKey()
	if err == nil || err.Error() !=
		"replace the change-me values in the environment first" {
		t.Errorf("change-me: %v", err)
	}
}
