// Package desk is Front Desk's one poller: it checks the proxy once
// for every viewer, keeps the answer as compact JSON (and gzipped),
// and announces each new version. A port of frontdesk.py's Desk,
// Proxy, GameSeats, seat_state, dollars, merge_tally and http_tally.
package desk

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/config"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
	"github.com/kjFriday/local-verse-proxy/internal/litellm"
	"github.com/kjFriday/local-verse-proxy/internal/pgread"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// State is what the page gets from /api/state, kept for mint and
// revoke to check against.
type State struct {
	Up     bool
	Models []string
	Cards  []Card
}

type override struct {
	keys []pyjson.Obj
	at   int
}

type tallyResult struct {
	t   Tally
	err error
}

// Desk checks the proxy once for every viewer and keeps the answer.
type Desk struct {
	live   *config.Live
	log    *config.Log
	clk    clock.Clock
	q      pgread.Querier
	lan    bool
	proxy  *Proxy
	games  *GameSeats
	secret []byte
	times  times
	kick   chan struct{}

	// cmu stands for Python's Condition: counters, the published
	// answer and its version, viewers.
	cmu       sync.Mutex
	starts    int
	checks    int
	mine      int
	version   int
	checked   float64
	viewers   int
	seen      float64
	forceKeys bool
	state     *State
	body, gz  []byte
	etag      string
	changed   chan struct{} // closed on each new version
	done      chan struct{} // closed when a check finishes

	// mu is Python's self.lock: the cards, overrides, and publish.
	mu        sync.Mutex
	rawKeys   []pyjson.Obj
	overrides map[string]override
	overOrder []string
	notes     []string
	tokens    map[string]string
	game      map[string]bool
	models    []string
	litellm   any
	db        any
	tally     *Tally

	// The desk goroutine's own.
	slowAt, keysAt, tallyAt, fallbackAt, histAt float64
	hist                                        *dbTally
	tallyBusy                                   bool
	tallyDone                                   chan tallyResult
	lastTally                                   float64
}

// Options are what New needs.
type Options struct {
	Live  *config.Live
	Log   *config.Log
	Clock clock.Clock // nil: the real one
	Tally pgread.Querier
	LAN   bool   // --lan, or always with --docker
	Game  string // the game's folder
}

// New makes a Desk; Run starts it.
func New(o Options) *Desk {
	if o.Clock == nil {
		o.Clock = clock.Real()
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		panic("desk: no random bytes: " + err.Error())
	}
	inf := math.Inf(-1)
	d := &Desk{live: o.Live, log: o.Log, clk: o.Clock, q: o.Tally,
		lan: o.LAN, proxy: NewProxy(o.Live),
		games: NewGameSeats(o.Game), secret: secret,
		kick:    make(chan struct{}, 1),
		changed: make(chan struct{}), done: make(chan struct{}),
		seen: inf, overrides: map[string]override{},
		tokens: map[string]string{}, game: map[string]bool{},
		models: []string{},
		slowAt: inf, keysAt: inf, tallyAt: inf, fallbackAt: inf,
		histAt: inf, lastTally: inf}
	d.times.warn = func(text string) {
		d.log.Line("a card's time cannot be read, so it counts as " +
			"none: " + pyfmt.Cut(d.live.Scrub(
			pyfmt.ReprString(text)), 200))
	}
	return d
}

// SetSecret sets the card id secret, for tests and goldens.
func (d *Desk) SetSecret(s []byte) { d.secret = append([]byte(nil), s...) }

// Games is the office game's seats.
func (d *Desk) Games() *GameSeats { return d.games }

// Kick asks for a check now.
func (d *Desk) Kick() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// Run is the desk's loop, until ctx ends. Wake each second to see if
// anyone is watching; check every Fast seconds while they are, at
// once when kicked.
func (d *Desk) Run(ctx context.Context) {
	last := math.Inf(-1)
	for {
		kicked := false
		select {
		case <-ctx.Done():
			return
		case <-d.kick:
			kicked = true
		case <-d.clk.After(1):
		}
		d.cmu.Lock()
		busy := d.viewers > 0 || d.clk.Mono()-d.seen < IdleAfter
		d.cmu.Unlock()
		if !kicked && !(busy && d.clk.Mono()-last >= Fast) {
			continue
		}
		// A kick that came while deciding counts for this check.
		select {
		case <-d.kick:
		default:
		}
		last = d.clk.Mono()
		d.cmu.Lock()
		d.starts++
		d.mine = d.starts
		d.cmu.Unlock()
		d.safeRefresh(ctx)
		d.cmu.Lock()
		d.checks++
		close(d.done)
		d.done = make(chan struct{})
		d.cmu.Unlock()
	}
}

func (d *Desk) safeRefresh(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			d.log.Line("check failed: " + pyfmt.Cut(
				d.live.Scrub(fmt.Sprint(r)), 200))
		}
	}()
	if err := d.refresh(ctx); err != nil {
		d.log.Line("check failed: " + pyfmt.Cut(
			d.live.Scrub(err.Error()), 200))
	}
}

// Fresh makes sure the shared answer is at most Fast seconds old.
func (d *Desk) Fresh() {
	d.cmu.Lock()
	d.seen = d.clk.Mono()
	if len(d.body) > 0 && d.clk.Wall()-d.checked < Fast+1 {
		d.cmu.Unlock()
		return
	}
	n := d.checks
	d.Kick()
	d.waitLocked(func() bool { return d.checks != n }, freshWait)
	d.cmu.Unlock()
}

// RefreshNow waits for a full check that began after this call.
func (d *Desk) RefreshNow() {
	d.cmu.Lock()
	target := d.starts + 1
	d.forceKeys = true
	d.Kick()
	d.waitLocked(func() bool { return d.checks >= target }, refreshWait)
	d.cmu.Unlock()
}

// waitLocked waits, cmu held, until ok() or seconds pass.
func (d *Desk) waitLocked(ok func() bool, seconds float64) {
	timer := d.clk.After(seconds)
	for !ok() {
		ch := d.done
		d.cmu.Unlock()
		select {
		case <-ch:
		case <-timer:
			d.cmu.Lock()
			return
		}
		d.cmu.Lock()
	}
}

func (d *Desk) refresh(ctx context.Context) error {
	d.live.ReloadIfChanged()
	s := d.live.Get()
	now := d.clk.Mono()
	var job chan tallyResult
	if now-d.tallyAt >= TallyEvery && !d.tallyBusyNow() {
		d.tallyAt = now
		job = d.startTally(ctx)
	}
	notes := append([]string{}, s.Warnings...)
	up := false
	status, err := d.proxy.Live(ctx)
	if err != nil {
		notes = append(notes, fmt.Sprintf("Details: nothing answers "+
			"at %s:%d (%s).", s.Host, s.Port, Class(err)))
	} else {
		up = status == 200
	}
	if up {
		var err error
		d.mu.Lock()
		noModels := len(d.models) == 0
		d.mu.Unlock()
		if now-d.slowAt >= SlowEvery || noModels {
			if err = d.loadSlow(ctx); err == nil {
				d.slowAt = now
			}
		}
		d.cmu.Lock()
		force := d.forceKeys
		d.cmu.Unlock()
		if err == nil && (force || now-d.keysAt >= KeysEvery) {
			d.cmu.Lock()
			d.forceKeys = false
			d.cmu.Unlock()
			var keys []pyjson.Obj
			if keys, err = d.loadKeys(ctx); err == nil {
				d.setKeys(keys)
				d.keysAt = now
				d.loadGame()
			}
		}
		if err != nil {
			notes = append(notes, keysNote(err))
		}
	}
	if job != nil {
		var r tallyResult
		select {
		case r = <-job:
		case <-d.clk.After(tallyWait):
			r.err = errors.New("") // as Python's TimeoutError: ""
		}
		if r.err == nil {
			r.t.Source = "database"
			d.mu.Lock()
			d.tally = &r.t
			d.mu.Unlock()
		} else {
			why := pyfmt.Cut(s.Scrub(r.err.Error()), 160)
			// Proxy down too? Keep the last tally, like the cards.
			if up && now-d.fallbackAt >= FallbackEvery {
				d.fallbackAt = now
				d.mu.Lock()
				keys := d.rawKeys
				d.mu.Unlock()
				t := d.HTTPTally(ctx, why, keys)
				d.mu.Lock()
				d.tally = &t
				d.mu.Unlock()
			}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.notes = notes
	return d.publish(d.build(up, notes))
}

// tallyBusyNow says if a tally job still runs.
func (d *Desk) tallyBusyNow() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tallyBusy
}

func keysNote(err error) string {
	var pe *ProxyError
	var big *litellm.TooBigError
	switch {
	case errors.As(err, &pe):
		return pe.Msg
	case errors.As(err, &big):
		return "Could not read the key cards: " + big.Error()
	}
	return "Could not read the key cards: " + Class(err)
}

// startTally runs tallyQuery in its own goroutine; the answer comes
// on the channel. Only one runs at a time (ledger F-13).
func (d *Desk) startTally(ctx context.Context) chan tallyResult {
	out := make(chan tallyResult, 1)
	d.mu.Lock()
	d.tallyBusy = true
	d.mu.Unlock()
	go func() {
		t, err := d.tallyQuery(ctx)
		out <- tallyResult{t, err}
		d.mu.Lock()
		d.tallyBusy = false
		d.mu.Unlock()
	}()
	return out
}

// tallyQuery is today on every check; the days before only every
// HistoryEvery seconds: they are most of the work.
func (d *Desk) tallyQuery(ctx context.Context) (Tally, error) {
	ask := func(sql string) (dbTally, error) {
		raw, err := d.q.Query(ctx, sql)
		if err != nil {
			return dbTally{}, err
		}
		return readTally(raw)
	}
	today, err := ask(TodaySQL)
	if err != nil {
		return Tally{}, err
	}
	d.mu.Lock()
	hist, histAt := d.hist, d.histAt
	d.mu.Unlock()
	if hist == nil || !sameDay(hist.Day, today.Day) ||
		d.clk.Mono()-histAt >= HistoryEvery {
		h, err := ask(HistorySQL)
		if err != nil {
			return Tally{}, err
		}
		hist = &h
		d.mu.Lock()
		d.hist, d.histAt = hist, d.clk.Mono()
		d.mu.Unlock()
		if !sameDay(hist.Day, today.Day) { // midnight just passed
			if today, err = ask(TodaySQL); err != nil {
				return Tally{}, err
			}
		}
	}
	return MergeTally(hist.Rows, today.Rows, today.Day), nil
}

func sameDay(a, b any) bool {
	return string(pyjson.Compact(a)) == string(pyjson.Compact(b))
}

// setKeys takes a fresh card list, unless it began before one of our
// own changes: then that seat's cards come from the change.
func (d *Desk) setKeys(keys []pyjson.Obj) {
	d.cmu.Lock()
	mine := d.mine
	d.cmu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	var keep []string
	for _, seat := range d.overOrder {
		o := d.overrides[seat]
		if mine <= o.at {
			var out []pyjson.Obj
			for _, k := range keys {
				if !aliasIs(k, seat) {
					out = append(out, k)
				}
			}
			keys = append(out, o.keys...)
			keep = append(keep, seat)
		} else {
			delete(d.overrides, seat)
		}
	}
	d.overOrder = keep
	d.rawKeys = keys
}

func aliasIs(k pyjson.Obj, seat string) bool {
	a, ok := k.Val("key_alias").(string)
	return ok && a == seat
}

// loadGame reads which cards the office game made. Read with the
// cards, since only they use it; it never fails the check.
func (d *Desk) loadGame() {
	defer func() {
		if r := recover(); r != nil {
			d.log.Line("could not read the game's seats: " +
				pyfmt.Cut(fmt.Sprint(r), 160))
		}
	}()
	g := d.games.Get()
	d.mu.Lock()
	d.game = g
	d.mu.Unlock()
}

// ShowChange puts a new or cancelled card in the shared answer now,
// with one small lookup instead of a whole check.
func (d *Desk) ShowChange(ctx context.Context, seat string, gone bool) {
	keys := []pyjson.Obj{}
	if !gone {
		j, err := NewProxy(d.live).JSON(ctx,
			"/key/list?return_full_object=true&key_alias="+
				Quote(seat))
		o, _ := j.(pyjson.Obj)
		list, ok := o.Val("keys").([]any)
		if err == nil && !ok {
			if _, has := o.Get("keys"); !has {
				err = errors.New("KeyError")
			}
		}
		if err != nil {
			d.RefreshNow() // the card exists; just slower
			return
		}
		for _, k := range list {
			if ko, ok := k.(pyjson.Obj); ok {
				keys = append(keys, ko)
			}
		}
	}
	d.cmu.Lock()
	starts := d.starts
	d.cmu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.overrides[seat]; !ok {
		d.overOrder = append(d.overOrder, seat)
	}
	d.overrides[seat] = override{keys, starts}
	var out []pyjson.Obj
	for _, k := range d.rawKeys {
		if !aliasIs(k, seat) {
			out = append(out, k)
		}
	}
	d.rawKeys = append(out, keys...)
	if err := d.publish(d.build(true, d.notes)); err != nil {
		d.log.Line("check failed: " + pyfmt.Cut(
			d.live.Scrub(err.Error()), 200))
	}
}

// Quote is urllib.parse.quote(s): "/" and letters, digits and _.-~
// kept, everything else %XX of its UTF-8 bytes.
func Quote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || strings.IndexByte("_.-~/",
			c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func (d *Desk) loadSlow(ctx context.Context) error {
	j, err := d.proxy.JSON(ctx, "/health/readiness/details")
	if err != nil {
		return err
	}
	info, ok := j.(pyjson.Obj)
	if !ok {
		return &ShapeError{"readiness is not an object"}
	}
	j, err = d.proxy.JSON(ctx, "/v1/models")
	if err != nil {
		return err
	}
	mo, ok := j.(pyjson.Obj)
	if !ok {
		return &ShapeError{"models is not an object"}
	}
	var models []string
	data := mo.Val("data")
	if Truthy(data) {
		list, ok := data.([]any)
		if !ok {
			return &ShapeError{"data is not a list"}
		}
		for _, m := range list {
			mm, ok := m.(pyjson.Obj)
			if !ok {
				return &ShapeError{"a model is not an object"}
			}
			id := mm.Val("id")
			if !Truthy(id) {
				continue
			}
			s, ok := id.(string)
			if !ok {
				return &ShapeError{"a model id is not text"}
			}
			models = append(models, s)
		}
	}
	sort.Strings(models)
	if models == nil {
		models = []string{}
	}
	d.mu.Lock()
	d.litellm = info.Val("litellm_version")
	d.db = info.Val("db")
	d.models = models
	d.mu.Unlock()
	return nil
}

func (d *Desk) loadKeys(ctx context.Context) ([]pyjson.Obj, error) {
	var keys []pyjson.Obj
	for page := 1; page <= maxPages; page++ {
		j, err := d.proxy.JSON(ctx, "/key/list?return_full_object="+
			"true&size=100&page="+strconv.Itoa(page))
		if err != nil {
			return nil, err
		}
		o, ok := j.(pyjson.Obj)
		if !ok {
			return nil, &ShapeError{"the key list is not an object"}
		}
		if ks := o.Val("keys"); Truthy(ks) {
			list, ok := ks.([]any)
			if !ok {
				return nil, &ShapeError{"keys is not a list"}
			}
			for _, k := range list {
				ko, ok := k.(pyjson.Obj)
				if !ok {
					return nil,
						&ShapeError{"a key is not " +
							"an object"}
				}
				keys = append(keys, ko)
			}
		}
		total := 1.0
		if tp := o.Val("total_pages"); Truthy(tp) {
			n, ok := Number(tp)
			if !ok {
				return nil, &ShapeError{"total_pages is not " +
					"a number"}
			}
			total = n.F
		}
		if float64(page) >= total {
			break
		}
	}
	// A card made or cancelled between pages can shift another
	// across a page edge: drop repeats.
	seen := map[string]bool{}
	out := []pyjson.Obj{}
	for _, k := range keys {
		t := string(pyjson.Compact(k.Val("token")))
		if !seen[t] {
			seen[t] = true
			out = append(out, k)
		}
	}
	return out, nil
}

// built is one answer: its JSON and what mint and revoke check.
type built struct {
	obj   pyjson.Obj
	state *State
}

// build is the answer for the page; d.mu held.
func (d *Desk) build(up bool, notes []string) built {
	now := time.Unix(0, int64(d.clk.Wall()*1e9)).UTC()
	cards, tokens := d.times.BuildCards(d.rawKeys, d.secret, d.game,
		now)
	d.tokens = tokens
	var tally any
	if d.tally != nil {
		live := map[string]bool{}
		for _, c := range cards {
			live[c.Seat] = true
		}
		seats, models := []pyjson.Obj{}, []pyjson.Obj{}
		for i := range d.tally.Rows {
			r := &d.tally.Rows[i]
			if r.All[0] != nil {
				n := Dollars(*r.All[0])
				r.All[0] = &n
			}
			r.Today[0] = Dollars(r.Today[0])
			switch r.K {
			case "seat":
				l := live[r.Name]
				seats = append(seats, r.Obj(&l))
			case "model":
				models = append(models, r.Obj(nil))
			}
		}
		tally = pyjson.O("day", d.tally.Day, "source", d.tally.Source,
			"why", d.tally.Why, "seats", seats, "models", models)
	}
	scrubbed := []string{}
	s := d.live.Get()
	for _, n := range notes {
		scrubbed = append(scrubbed, s.Scrub(n))
	}
	cardObjs := make([]pyjson.Obj, 0, len(cards))
	for _, c := range cards {
		cardObjs = append(cardObjs, c.Obj())
	}
	def := s.Defaults
	st := &State{Up: up, Models: d.models, Cards: cards}
	return built{pyjson.O(
		"desk", pyjson.O("up", up, "litellm", d.litellm, "db", d.db,
			"lan", d.lan, "notes", scrubbed),
		"models", d.models,
		"defaults", pyjson.O("models", def.Models,
			"budget", def.Budget, "duration", def.Duration),
		"cards", cardObjs,
		"tally", tally,
	), st}
}

// publish stores a new answer if it differs; d.mu held.
func (d *Desk) publish(b0 built) error {
	st := b0.state
	body := pyjson.Compact(b0.obj)
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	var gz []byte
	d.cmu.Lock()
	defer d.cmu.Unlock()
	if etag != d.etag {
		var b bytes.Buffer
		w, err := gzip.NewWriterLevel(&b, 6)
		if err != nil {
			return err
		}
		w.Write(body)
		if err := w.Close(); err != nil {
			return err
		}
		gz = b.Bytes()
		d.state, d.body, d.gz, d.etag = st, body, gz, etag
		d.version++
		close(d.changed)
		d.changed = make(chan struct{})
	}
	d.checked = d.clk.Wall()
	return nil
}

// Answer is the shared answer now.
func (d *Desk) Answer() (etag string, body, gz []byte) {
	d.cmu.Lock()
	defer d.cmu.Unlock()
	return d.etag, d.body, d.gz
}

// Current is the published state, or nil before the first check.
func (d *Desk) Current() *State {
	d.cmu.Lock()
	defer d.cmu.Unlock()
	return d.state
}

// Token is the token behind a card id.
func (d *Desk) Token(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tokens[id]
}

// AddViewer counts one more live page, unless max are open.
func (d *Desk) AddViewer(max int) bool {
	d.cmu.Lock()
	defer d.cmu.Unlock()
	if d.viewers >= max {
		return false
	}
	d.viewers++
	return true
}

// DropViewer counts one fewer.
func (d *Desk) DropViewer() {
	d.cmu.Lock()
	d.viewers--
	d.cmu.Unlock()
}

// Viewers is how many live pages are open.
func (d *Desk) Viewers() int {
	d.cmu.Lock()
	defer d.cmu.Unlock()
	return d.viewers
}

// Seen marks that a page was just served.
func (d *Desk) Seen() {
	d.cmu.Lock()
	d.seen = d.clk.Mono()
	d.cmu.Unlock()
}

// Wait waits up to a second, or until ctx ends, for a version other
// than sent; it gives the version now and when the desk last checked.
func (d *Desk) Wait(ctx context.Context, sent int) (int, float64) {
	d.cmu.Lock()
	if d.version == sent {
		ch := d.changed
		d.cmu.Unlock()
		select {
		case <-ch:
		case <-d.clk.After(1):
		case <-ctx.Done():
		}
		d.cmu.Lock()
	}
	defer d.cmu.Unlock()
	return d.version, d.checked
}

// Stop stops the tally source (psql).
func (d *Desk) Stop() { d.q.Stop() }
