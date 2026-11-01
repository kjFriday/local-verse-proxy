//go:build pg

// lvp desk --docker against a real Postgres 16 seeded with
// internal/pgread/testdata/schema.sql (ci.yml's pg job): PGREAD_TEST_URL
// names it.
package frontdesk

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/litellm/fakelitellm"
)

func TestEndToEndDocker(t *testing.T) {
	url := os.Getenv("PGREAD_TEST_URL")
	if url == "" {
		t.Skip("PGREAD_TEST_URL is not set")
	}
	fake := fakelitellm.Start(fakelitellm.Options{MasterKey: e2eMaster,
		Models: []string{"local"}})
	defer fake.Close()
	dir := proxyDir(t, "http://unused:1")
	os.Remove(dir + "/seats.env") // --docker reads the environment
	pw := "docker-password-1234"
	d := startDesk(t, dir, []string{
		"LITELLM_MASTER_KEY=" + e2eMaster,
		"LITELLM_SALT_KEY=sk-fake-e2e-salt",
		"PROXY_URL=" + fake.URL,
		"DATABASE_URL=" + url,
		"FRONTDESK_PASSWORD=" + pw,
		"FRONTDESK_HOSTS=desk.example, other.example",
		"FRONTDESK_BIND=0.0.0.0",
		"SEATS_ENV=",
	}, "--docker")
	if !strings.HasPrefix(d.banner, "Front Desk is open, in Docker.\n") ||
		!strings.Contains(d.banner, "Password: the one in "+
			"FRONTDESK_PASSWORD in seats.env\n") {
		t.Fatalf("banner %q", d.banner)
	}
	code, s, _ := d.do("GET", "/api/session", "", "", "")
	if code != 200 || s["docker"] != true || s["lan"] != false ||
		s["reset_cmd"] != "lvp game reset" {
		t.Fatalf("session %d %v", code, s)
	}
	time.Sleep(1100 * time.Millisecond)
	code, j, h := d.do("POST", "/api/login", `{"password": "`+pw+`"}`, "",
		"")
	if code != 200 {
		t.Fatalf("login %d %v", code, j)
	}
	cookie := strings.SplitN(strings.Split(h.Get("Set-Cookie"), ";")[0],
		"=", 2)[1]
	csrf := j["csrf"].(string)
	d.waitFor(cookie, func(st map[string]any) bool {
		tl, _ := st["tally"].(map[string]any)
		return tl != nil && tl["source"] == "database"
	})
	code, m, _ := d.do("POST", "/api/mint", `{"seat": "pg-seat", `+
		`"models": ["local"], "budget": "1", "duration": "1d"}`, csrf,
		cookie)
	if code != 200 || !strings.HasPrefix(m["key"].(string), "sk-") ||
		m["base_url"] != "http://127.0.0.1:4000" {
		t.Fatalf("mint %d %v", code, m)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if c := d.stop(os.Interrupt); c != 0 {
		t.Fatalf("exit %d", c)
	}
	for _, secret := range []string{e2eMaster, pw, m["key"].(string)} {
		if strings.Contains(d.errb.String(), secret) {
			t.Fatal("a secret reached the log")
		}
	}
}
