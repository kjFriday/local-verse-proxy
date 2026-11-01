package desk

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/config"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
	"github.com/kjFriday/local-verse-proxy/internal/litellm/fakelitellm"
	"github.com/kjFriday/local-verse-proxy/internal/pgread"
)

const master = "sk-fake-desk-master-key"

type lines struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// querier is a scripted tally source.
type querier struct {
	mu    sync.Mutex
	calls int
	block chan struct{} // if set, each query waits on it
	err   error
	today string
	hist  string
}

func (q *querier) Query(ctx context.Context, sql string) (json.RawMessage,
	error) {
	q.mu.Lock()
	q.calls++
	block, err := q.block, q.err
	q.mu.Unlock()
	if block != nil {
		<-block
	}
	if err != nil {
		return nil, err
	}
	if sql == HistorySQL {
		return json.RawMessage(q.hist), nil
	}
	return json.RawMessage(q.today), nil
}

func (q *querier) Stop() {}

func (q *querier) n() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.calls
}

type rig struct {
	d    *Desk
	fake *fakelitellm.Server
	clk  *clock.Fake
	log  *lines
	q    *querier
	path string
}

func newRig(t testing.TB, proxyURL string) *rig {
	t.Helper()
	r := &rig{log: &lines{}, q: &querier{
		today: `{"day": "2026-10-31", "rows": [{"k": "seat", ` +
			`"name": "sam", "spend": 0.5, "tokens": 3, ` +
			`"reqs": 1, "fails": 0}]}`,
		hist: `{"day": "2026-10-31", "rows": []}`}}
	if proxyURL == "" {
		r.fake = fakelitellm.Start(fakelitellm.Options{
			MasterKey: master, Models: []string{"local", "big"},
			Version: "1.106.6"})
		t.Cleanup(r.fake.Close)
		proxyURL = r.fake.URL
	}
	dir := t.TempDir()
	r.path = filepath.Join(dir, "seats.env")
	os.WriteFile(r.path, []byte("LITELLM_MASTER_KEY="+master+
		"\nPROXY_URL="+proxyURL+"\n"), 0o600)
	s, err := config.LoadFile(r.path, func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	log := config.NewLog(r.log)
	r.clk = clock.NewFake(float64(time.Now().Unix()), 1000)
	r.d = New(Options{Live: config.NewLive(s, log, nil), Log: log,
		Clock: r.clk, Tally: r.q, Game: dir})
	return r
}

// check runs one check, moving the fake clock past any wait in it.
func (r *rig) check(t *testing.T) pyjson.Obj {
	t.Helper()
	done := make(chan struct{})
	go func() {
		r.d.safeRefresh(context.Background())
		close(done)
	}()
	for {
		select {
		case <-done:
			_, body, _ := r.d.Answer()
			v, err := pyjson.Decode(body)
			if err != nil {
				t.Fatalf("answer: %v", err)
			}
			return v.(pyjson.Obj)
		case <-time.After(20 * time.Millisecond):
			if r.clk.Waiting() > 0 {
				r.clk.Advance(tallyWait)
			}
		}
	}
}

func notes(o pyjson.Obj) []string {
	var out []string
	for _, n := range o.Val("desk").(pyjson.Obj).Val("notes").([]any) {
		out = append(out, n.(string))
	}
	return out
}

func TestNotes(t *testing.T) {
	r := newRig(t, "")
	o := r.check(t)
	if d := o.Val("desk").(pyjson.Obj); d.Val("up") != true ||
		len(notes(o)) != 0 || d.Val("litellm") != "1.106.6" {
		t.Fatalf("up: %s", pyjson.Compact(o))
	}
	r.clk.Advance(SlowEvery)
	r.fake.Fail("/v1/models", 500, `{"error": "x"}`)
	o = r.check(t)
	if n := notes(o); len(n) != 1 || n[0] != "The proxy answered 500 "+
		"to /v1/models. Its database may be down: run docker compose "+
		"ps." {
		t.Fatalf("500: %q", n)
	}
	r.fake.Fail("/v1/models", 0, "")
	r.fake.Fail("/key/list", 401, `{"error": "no"}`)
	r.clk.Advance(SlowEvery)
	if n := notes(r.check(t)); len(n) != 1 || n[0] != MasterRefused {
		t.Fatalf("401: %q", n)
	}
	r.fake.Fail("/key/list", 200, `{"keys": [NaN]}`)
	r.clk.Advance(KeysEvery)
	if n := notes(r.check(t)); len(n) != 1 || n[0] != "Could not read "+
		"the key cards: JSONDecodeError" {
		t.Fatalf("NaN: %q", n)
	}
	r.fake.Fail("/key/list", 200, `{"keys": "x", "total_pages": 1}`)
	r.clk.Advance(KeysEvery)
	if n := notes(r.check(t)); len(n) != 1 || !strings.HasSuffix(n[0],
		"JSONDecodeError") {
		t.Fatalf("shape: %q", n)
	}
	r.fake.Fail("/key/list", 0, "")
	r.fake.Down(true)
	o = r.check(t)
	if n := notes(o); o.Val("desk").(pyjson.Obj).Val("up") != false ||
		len(n) != 1 || !strings.HasPrefix(n[0], "Details: nothing "+
		"answers at 127.0.0.1:") || !strings.HasSuffix(n[0],
		" (ConnectionRefusedError).") {
		t.Fatalf("down: %q", n)
	}
	r.fake.Down(false)
	r.fake.Delay("/health/liveliness", 3*time.Second)
	if n := notes(r.check(t)); len(n) != 1 ||
		!strings.HasSuffix(n[0], " (TimeoutError).") {
		t.Fatalf("slow: %q", n)
	}
	if strings.Contains(r.log.String(), master) {
		t.Fatal("the master key reached the log")
	}
}

func TestNotesNonsense(t *testing.T) {
	// Something that answers, but not HTTP.
	l, err := listenGarbage()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	r := newRig(t, "http://"+l.Addr().String())
	if n := notes(r.check(t)); len(n) != 1 || !strings.HasSuffix(n[0],
		" (BadStatusLine).") && !strings.HasSuffix(n[0],
		" (RemoteDisconnected).") {
		t.Fatalf("garbage: %q", n)
	}
}

func TestPaging(t *testing.T) {
	r := newRig(t, "")
	for i := 0; i < 250; i++ {
		r.fake.AddKey(fakelitellm.Key{Alias: fmt.Sprintf("seat-%03d",
			i)})
	}
	o := r.check(t)
	cards := o.Val("cards").([]any)
	if len(cards) != 250 {
		t.Fatalf("%d cards", len(cards))
	}
	if cards[0].(pyjson.Obj).Val("seat") != "seat-000" {
		t.Fatalf("order: %v", cards[0])
	}
	pages := 0
	for _, q := range r.fake.Requests() {
		if strings.HasPrefix(q.Path, "/key/list") {
			pages++
			if q.Header.Get("Authorization") != "Bearer "+master {
				t.Fatal("no master key on /key/list")
			}
		}
		if q.Path == "/health/liveliness" &&
			q.Header.Get("Authorization") != "" {
			t.Fatal("the master key went to /health/liveliness")
		}
	}
	if pages != 3 {
		t.Fatalf("%d pages", pages)
	}
}

func TestTallyAndFallback(t *testing.T) {
	r := newRig(t, "")
	o := r.check(t)
	tl := o.Val("tally").(pyjson.Obj)
	if tl.Val("source") != "database" || tl.Val("why") != nil {
		t.Fatalf("tally: %s", pyjson.Compact(tl))
	}
	if r.q.n() != 2 {
		t.Fatalf("%d questions: today and the days before", r.q.n())
	}
	// Today only, until HistoryEvery.
	r.clk.Advance(TallyEvery)
	r.check(t)
	if r.q.n() != 3 {
		t.Fatalf("%d questions", r.q.n())
	}
	// The database fails: the proxy's own numbers, and why.
	r.q.mu.Lock()
	r.q.err = &pgread.Error{Msg: "psql stopped " + master}
	r.q.mu.Unlock()
	r.clk.Advance(TallyEvery)
	o = r.check(t)
	tl = o.Val("tally").(pyjson.Obj)
	if tl.Val("source") != "proxy" || tl.Val("why") !=
		"psql stopped [hidden]" {
		t.Fatalf("fallback: %s", pyjson.Compact(tl))
	}
}

// A tally question still running is not asked again (ledger F-13);
// the check stops waiting after 10 s with why "".
func TestTallySkippedWhileBusy(t *testing.T) {
	r := newRig(t, "")
	block := make(chan struct{})
	r.q.mu.Lock()
	r.q.block = block
	r.q.mu.Unlock()
	o := r.check(t)
	tl := o.Val("tally").(pyjson.Obj)
	if tl.Val("source") != "proxy" || tl.Val("why") != "" {
		t.Fatalf("late: %s", pyjson.Compact(tl))
	}
	r.clk.Advance(TallyEvery)
	r.check(t)
	if r.q.n() != 1 {
		t.Fatalf("%d questions while one ran", r.q.n())
	}
	close(block)
	time.Sleep(100 * time.Millisecond)
	r.clk.Advance(TallyEvery)
	r.check(t)
	if r.q.n() < 2 {
		t.Fatal("no new question once the old one ended")
	}
}

func TestReread(t *testing.T) {
	r := newRig(t, "")
	r.check(t)
	text := "LITELLM_MASTER_KEY=" + master + "\nPROXY_URL=" + r.fake.URL +
		"\nDEFAULT_BUDGET=9\n"
	os.WriteFile(r.path, []byte(text), 0o600)
	later := time.Now().Add(time.Minute)
	os.Chtimes(r.path, later, later)
	o := r.check(t)
	if o.Val("defaults").(pyjson.Obj).Val("budget") != "9" ||
		!strings.Contains(r.log.String(), " reread seats.env\n") {
		t.Fatalf("reread: %s / %q", pyjson.Compact(o), r.log.String())
	}
	// A broken file keeps the old settings, silently.
	os.WriteFile(r.path, []byte("X=$(rm -rf /)\n"), 0o600)
	later = later.Add(time.Minute)
	os.Chtimes(r.path, later, later)
	o = r.check(t)
	if o.Val("defaults").(pyjson.Obj).Val("budget") != "9" ||
		strings.Count(r.log.String(), "reread") != 1 {
		t.Fatal("a broken seats.env replaced the settings")
	}
}

func TestShowChange(t *testing.T) {
	r := newRig(t, "")
	r.check(t)
	r.fake.AddKey(fakelitellm.Key{Alias: "new-seat"})
	r.d.ShowChange(context.Background(), "new-seat", false)
	_, body, _ := r.d.Answer()
	if !strings.Contains(string(body), `"seat":"new-seat"`) {
		t.Fatal("a new card did not show at once")
	}
	// A check that began before the change keeps it.
	r.d.cmu.Lock()
	r.d.mine = r.d.starts
	r.d.cmu.Unlock()
	r.d.setKeys(nil)
	r.d.mu.Lock()
	n := len(r.d.rawKeys)
	r.d.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d keys after an older check", n)
	}
	r.d.ShowChange(context.Background(), "new-seat", true)
	_, body, _ = r.d.Answer()
	if strings.Contains(string(body), `"seat":"new-seat"`) {
		t.Fatal("a cancelled card stayed")
	}
}

func TestFreshAndRefreshNow(t *testing.T) {
	r := newRig(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.d.Run(ctx)
	done := make(chan struct{})
	go func() { r.d.Fresh(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Fresh did not return")
	}
	if _, body, _ := r.d.Answer(); len(body) == 0 {
		t.Fatal("no answer after Fresh")
	}
	before := r.d.Current()
	done = make(chan struct{})
	go func() { r.d.RefreshNow(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RefreshNow did not return")
	}
	r.d.cmu.Lock()
	checks := r.d.checks
	r.d.cmu.Unlock()
	if checks < 2 || before == nil {
		t.Fatalf("checks %d", checks)
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"sam":           "sam",
		"a b/c":         "a%20b/c",
		"\u00e9t\u00e9": "%C3%A9t%C3%A9",
		"x&y=z?#":       "x%26y%3Dz%3F%23",
		"a.b_c-d~":      "a.b_c-d~",
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q", in, got)
		}
	}
}

// listenGarbage answers every connection with bytes that are not
// HTTP.
func listenGarbage() (net.Listener, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				buf := make([]byte, 4096)
				c.Read(buf)
				c.Write([]byte("SSH-2.0-nope\r\n\r\n"))
				c.Close()
			}()
		}
	}()
	return l, nil
}

func cardState(t *testing.T, o pyjson.Obj, seat string) string {
	t.Helper()
	for _, c := range o.Val("cards").([]any) {
		if c := c.(pyjson.Obj); c.Val("seat") == seat {
			return c.Val("state").(string)
		}
	}
	t.Fatalf("no card %q", seat)
	return ""
}

// A check that changes nothing keeps the answer and its version; a
// card whose time runs out still turns expired on time, with nothing
// else changed, and back if the clock is set back.
func TestSameAnswer(t *testing.T) {
	r := newRig(t, "")
	// Ends between the checks at 2 and 4 s, before any new tally (5
	// s) or card list (10 s).
	ends := time.Unix(int64(r.clk.Wall())+3, 0).UTC()
	r.fake.AddKey(fakelitellm.Key{Alias: "sam",
		Expires: ends.Format("2006-01-02T15:04:05.000000Z")})
	o := r.check(t)
	v0, _ := r.d.Wait(context.Background(), -1)
	if s := cardState(t, o, "sam"); s != "ok" {
		t.Fatalf("state %q", s)
	}
	r.clk.Advance(Fast)
	o = r.check(t)
	if v, _ := r.d.Wait(context.Background(), -1); v != v0 ||
		cardState(t, o, "sam") != "ok" {
		t.Fatalf("version %d, was %d: %s", v, v0, pyjson.Compact(o))
	}
	r.clk.Advance(Fast)
	o = r.check(t)
	if v, _ := r.d.Wait(context.Background(), -1); v == v0 ||
		cardState(t, o, "sam") != "expired" {
		t.Fatalf("not expired: version %d: %s", v, pyjson.Compact(o))
	}
	r.clk.SetWall(r.clk.Wall() - 60)
	if s := cardState(t, r.check(t), "sam"); s != "ok" {
		t.Fatalf("clock set back: %q", s)
	}
}
