//go:build linux

package desk

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/litellm/fakelitellm"
)

// benchRig is a desk with 200 cards and a tally of 200 seats, as the
// perf runs (tests/e2e/perf-results.md) had it.
func benchRig(b *testing.B) *rig {
	fake := fakelitellm.Start(fakelitellm.Options{MasterKey: master,
		Models: []string{"local", "big"}, Version: "1.106.6"})
	b.Cleanup(fake.Close)
	// The fake's answers, made once: the time is then the desk's.
	var mu sync.Mutex
	cache := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		body, ok := cache[req.URL.String()]
		mu.Unlock()
		if !ok {
			u := fake.URL + req.URL.String()
			freq, _ := http.NewRequest("GET", u, nil)
			freq.Header = req.Header.Clone()
			resp, err := http.DefaultClient.Do(freq)
			if err != nil {
				w.WriteHeader(502)
				return
			}
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			mu.Lock()
			cache[req.URL.String()] = body
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	b.Cleanup(srv.Close)
	r := newRig(b, srv.URL)
	r.fake = fake
	budget := 5.0
	for i := 0; i < 200; i++ {
		r.fake.AddKey(fakelitellm.Key{
			Alias:     fmt.Sprintf("seat-%03d", i),
			Models:    []string{"local"},
			MaxBudget: &budget, Spend: 0.001 * float64(i),
			BudgetDuration: "1d",
			BudgetResetAt:  "2026-11-02T00:00:00.000000Z",
		})
	}
	var rows []string
	for i := 0; i < 200; i++ {
		rows = append(rows, fmt.Sprintf(`{"k": "seat", `+
			`"name": "seat-%03d", "spend": 0.%06d, `+
			`"tokens": %d, "reqs": %d, "fails": 0}`,
			i, i*37, i*11, i))
	}
	rows = append(rows, `{"k": "model", "name": "local", `+
		`"spend": 0.25, "tokens": 9000, "reqs": 300, "fails": 2}`)
	r.q.today = `{"day": "2026-10-31", "rows": [` +
		strings.Join(rows, ", ") + `]}`
	r.q.hist = r.q.today
	return r
}

// step is one check, the clock Fast seconds on, as Run does it.
func (r *rig) step() {
	done := make(chan struct{})
	go func() {
		r.d.safeRefresh(context.Background())
		close(done)
	}()
	<-done
	r.clk.Advance(Fast)
}

// cpu is this process's CPU time so far (user and system).
func cpu() time.Duration {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// run times n checks, reporting CPU per check (the desk's, and the
// little the cached answers cost) as cpu-ms/op.
func run(b *testing.B, r *rig, force bool) {
	r.step()
	b.ReportAllocs()
	b.ResetTimer()
	start := cpu()
	for i := 0; i < b.N; i++ {
		if force {
			r.d.cmu.Lock()
			r.d.forceKeys = true
			r.d.cmu.Unlock()
		}
		r.step()
	}
	b.ReportMetric(float64(cpu()-start)/float64(b.N)/1e6, "cpu-ms/op")
}

// BenchmarkCheck is the 2-second check while someone watches, in the
// mix Run makes: cards every 5th, tally every other or so.
func BenchmarkCheck(b *testing.B) { run(b, benchRig(b), false) }

// BenchmarkCheckKeys is a check that reads the cards each time.
func BenchmarkCheckKeys(b *testing.B) { run(b, benchRig(b), true) }
