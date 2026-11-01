//go:build e2e

package live

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCapture records LiteLLM's answers to Steps on a live stack into
// testdata/golden/captures.json (statuses, error types and field
// names only; no key, no value).
//
//	E2E_PROXY_URL=http://localhost:4223 E2E_SEATS_ENV=seats.env \
//	  E2E_CAPTURE=1 go test -tags e2e -run TestCapture ./tests/e2e/live
func TestCapture(t *testing.T) {
	base := os.Getenv("E2E_PROXY_URL")
	if base == "" || os.Getenv("E2E_CAPTURE") != "1" {
		t.Skip("E2E_PROXY_URL and E2E_CAPTURE=1 record the answers")
	}
	master := ""
	b, err := os.ReadFile(os.Getenv("E2E_SEATS_ENV"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "LITELLM_MASTER_KEY="); ok {
			master = strings.TrimSpace(v)
		}
	}
	got := Play(base, master, fmt.Sprintf("cap-live-%d", os.Getpid()),
		Steps(time.Now()))
	for _, s := range got {
		t.Logf("%-20s %d %s %v", s.Name, s.Status, s.ErrorType,
			s.Fields)
	}
	out, _ := json.MarshalIndent(got, "", " ")
	p := filepath.Join("testdata", "golden", "captures.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
