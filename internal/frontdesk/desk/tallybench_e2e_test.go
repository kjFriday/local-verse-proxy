//go:build e2e

package desk

// TestTallyBench is tests/reference/perf/tally_bench.py for the Go
// desk: psql start, the tally as the desk asks it, the history query,
// and whole checks at the real cadence for 60 s, with the CPU the
// proxy's Postgres and LiteLLM used meanwhile. Run it in the proxy
// folder's compose settings (COMPOSE_PROJECT_NAME, COMPOSE_FILE), on
// a live stack, next to tally_bench.py for Python
// (tests/e2e/perf-results.md):
//
//	E2E_REPO=<proxy folder> go test -tags e2e -count=1 -v \
//	  -run TestTallyBench ./internal/frontdesk/desk
//
// E2E_TALLY_N, E2E_HISTORY_N and E2E_CHECK_SECS change how many
// tallies and history queries it times (40, 10) and for how long it
// checks (60 s); 0 skips that part.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/config"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
	"github.com/kjFriday/local-verse-proxy/internal/pgread"
)

// pct is tally_bench.py's pct(): median, 95th, count, in ms.
func pct(xs []float64) string {
	sort.Float64s(xs)
	n := len(xs)
	med := xs[n/2]
	if n%2 == 0 {
		med = (xs[n/2-1] + xs[n/2]) / 2
	}
	return fmt.Sprintf("p50 %7.2f p95 %7.2f ms n=%d", med,
		xs[min(n-1, int(0.95*float64(n)))], n)
}

// cgroup is a compose service's CPU time so far, in ms (cgroup v1).
func cgroup(t *testing.T, repo, svc string) func() float64 {
	c := exec.Command("docker", "compose", "ps", "-q", svc)
	c.Dir = repo
	out, err := c.Output()
	if err != nil {
		t.Fatalf("docker compose ps %s: %v", svc, err)
	}
	path := "/sys/fs/cgroup/cpuacct/docker/" +
		strings.TrimSpace(string(out)) + "/cpuacct.usage"
	return func() float64 {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		return n / 1e6
	}
}

// knob is a count from the environment, or def.
func knob(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n >= 0 {
		return n
	}
	return def
}

func since(t0 time.Time) float64 {
	return float64(time.Since(t0).Microseconds()) / 1000
}

func TestTallyBench(t *testing.T) {
	repo := os.Getenv("E2E_REPO")
	if repo == "" {
		t.Skip("E2E_REPO is not set: no live stack to measure")
	}
	ctx := context.Background()
	pg, lt := cgroup(t, repo, "db"), cgroup(t, repo, "proxy")
	s, err := config.LoadFile(filepath.Join(repo, "seats.env"),
		os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	psql := pgread.NewSession(pgread.SessionConfig{Dir: repo,
		Clock: clock.Real()})
	defer psql.Stop()
	t0 := time.Now()
	if _, err := psql.Query(ctx, "select json_build_object('a', 1)"); err !=
		nil {
		t.Fatal(err)
	}
	t.Logf("psql spawn + first query %.1f ms", since(t0))
	log := config.NewLog(os.Stderr)
	d := New(Options{Live: config.NewLive(s, log, os.LookupEnv),
		Log: log, Tally: psql, Game: filepath.Join(repo, "game")})

	var xs []float64
	var tl Tally
	c0 := pg()
	n := knob("E2E_TALLY_N", 40)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		if tl, err = d.tallyQuery(ctx); err != nil {
			t.Fatal(err)
		}
		xs = append(xs, since(t0))
	}
	if n > 0 {
		t.Logf("tally as polled  %s  Postgres CPU %.1f ms/run, rows %d",
			pct(xs), (pg()-c0)/float64(n), len(tl.Rows))
	}

	xs, c0 = nil, pg()
	n = knob("E2E_HISTORY_N", 10)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		if _, err := psql.Query(ctx, HistorySQL); err != nil {
			t.Fatal(err)
		}
		xs = append(xs, since(t0))
	}
	if n > 0 {
		t.Logf("history query    %s  Postgres CPU %.1f ms/run (once "+
			"per %.0f s)", pct(xs), (pg()-c0)/float64(n),
			HistoryEvery)
	}
	secs := knob("E2E_CHECK_SECS", 60)
	if secs == 0 {
		return
	}

	// Whole checks at the real cadence for 60 s, as with one open
	// page.
	xs = nil
	c0, l0, w0 := pg(), lt(), time.Now()
	end := time.Now().Add(time.Duration(secs) * time.Second)
	for time.Now().Before(end) {
		t0 := time.Now()
		if err := d.refresh(ctx); err != nil {
			t.Fatal(err)
		}
		xs = append(xs, since(t0))
		time.Sleep(time.Duration(Fast*float64(time.Second)) -
			time.Since(t0))
	}
	dt := time.Since(w0).Seconds()
	t.Logf("check (refresh)  %s; per second of watching: Postgres "+
		"%.2f%% core, LiteLLM %.2f%% core", pct(xs),
		(pg()-c0)/dt/10, (lt()-l0)/dt/10)
	_, body, _ := d.Answer()
	v, _ := pyjson.Decode(body)
	o, _ := v.(pyjson.Obj)
	tally, _ := o.Val("tally").(pyjson.Obj)
	seats, _ := tally.Val("seats").([]any)
	models, _ := tally.Val("models").([]any)
	cards, _ := o.Val("cards").([]any)
	t.Logf("cards %d tally rows %d", len(cards), len(seats)+len(models))
}
