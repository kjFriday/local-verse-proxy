package setup

import (
	"strings"
	"testing"
	"time"
)

func TestWaitUp(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	o, e, c := w.run(w.sys.Wait)
	if c != 0 || o != "The proxy is up.\n" || e != "" {
		t.Errorf("%d %q %q", c, o, e)
	}
}

// Down at first: a dot every 3 seconds, then up.
func TestWaitComesUp(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	p.Down(true)
	w.onWait = func(n int) {
		if n == 3 {
			p.Down(false)
		}
	}
	o, e, c := w.run(w.sys.Wait, "--timeout", "1m")
	want := "Waiting for the proxy at " + p.URL + ", up to 1m. A dot " +
		"every 3\nseconds:\n...\nThe proxy is up.\n"
	if c != 0 || flat(o) != flat(want) || !strings.HasSuffix(o,
		"...\nThe proxy is up.\n") || e != "" {
		t.Errorf("%d %q %q", c, o, e)
	}
}

func TestWaitTimesOut(t *testing.T) {
	w := newWorld(t)
	p := fake(t)
	w.seats(p.URL)
	p.Down(true)
	start := w.sys.Now()
	o, e, c := w.run(w.sys.Wait, "--timeout=10s")
	if c != 1 || !strings.HasSuffix(o, "....\n") ||
		!strings.Contains(e, "did not answer within 10s.") ||
		!strings.Contains(e, "Run: docker compose ps") ||
		!strings.Contains(flat(e), "Troubleshooting: the proxy") {
		t.Errorf("%d %q %q", c, o, e)
	}
	if d := w.sys.Now().Sub(start); d != 12*time.Second {
		t.Errorf("waited %v", d)
	}
}

func TestWaitUsage(t *testing.T) {
	w := newWorld(t)
	for _, a := range [][]string{{"--timeout", "soon"},
		{"--timeout", "-1s"}, {"--timeout"}, {"x"}} {
		if _, e, c := w.run(w.sys.Wait, a...); c != 2 ||
			!strings.HasPrefix(e, "usage: ./lvp wait") {
			t.Errorf("%q: %d %q", a, c, e)
		}
	}
	if d, err := duration("90"); err != nil || d != 90*time.Second {
		t.Errorf("90: %v %v", d, err)
	}
}

// No seats.env yet: the usual address.
func TestWaitNoSeats(t *testing.T) {
	w := newWorld(t)
	w.env["PROXY_URL"] = "http://127.0.0.1:9"
	o, e, c := w.run(w.sys.Wait, "--timeout", "3s")
	if c != 1 || !strings.Contains(o, "http://127.0.0.1:9") ||
		!strings.Contains(e, "within 3s") {
		t.Errorf("%d %q %q", c, o, e)
	}
}
