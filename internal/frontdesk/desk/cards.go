package desk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// Truthy is Python's bool() of a decoded JSON value.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		return pyjson.FromJSON(x).F != 0
	case float64:
		return x != 0
	case pyjson.Num:
		return x.F != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case pyjson.Obj:
		return len(x) > 0
	}
	return true
}

// Number reads a JSON number (or a bool, which Python counts as an
// int). ok false: not a number.
func Number(v any) (pyjson.Num, bool) {
	switch x := v.(type) {
	case pyjson.Num:
		return x, true
	case json.Number:
		return pyjson.FromJSON(x), true
	case float64:
		return pyjson.Float(x), true
	case bool:
		if x {
			return pyjson.IntNum(1), true
		}
		return pyjson.IntNum(0), true
	}
	return pyjson.Num{}, false
}

// orZero is Python's `x or 0` for a number.
func orZero(v any) pyjson.Num {
	if n, ok := Number(v); ok && n.F != 0 {
		return n
	}
	return pyjson.IntNum(0)
}

// Dollars is spend for the page: max(0.0, round(x or 0, 6)); a refill
// can leave -1e-18, which is $0. Not a finite number: 0.
func Dollars(v any) pyjson.Num {
	n := orZero(v)
	r := n
	if !n.Int {
		r = pyjson.Float(pyfmt.Round(n.F, 6))
	}
	if math.IsNaN(r.F) || math.IsInf(r.F, 0) || !(r.F > 0) {
		return pyjson.Float(0)
	}
	return r
}

// times remembers what each time text reads as: every check asks
// again for each card's next refill.
type times struct {
	mu   sync.Mutex
	m    map[string]timeRead
	bad  map[string]bool
	warn func(string)
}

type timeRead struct {
	t   time.Time
	ok  bool
	iso string
}

func (c *times) read(text string) timeRead {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.m[text]; ok {
		return r
	}
	if c.m == nil || len(c.m) >= 4096 {
		c.m = map[string]timeRead{}
	}
	var r timeRead
	t, naive, err := pyfmt.ParseISO(text)
	if err == nil {
		if naive {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(),
				t.Minute(), t.Second(), t.Nanosecond(),
				time.UTC)
		}
		r = timeRead{t: t, ok: true, iso: pyfmt.ISOFormat(t, false)}
	}
	c.m[text] = r
	return r
}

// UTCText is utc_text: a time from the proxy with its zone spelled
// out, so a browser never reads it as local time; nil if it is not
// one.
func (c *times) UTCText(v any) *string {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	r := c.read(s)
	if !r.ok {
		return nil
	}
	return &r.iso
}

// expired reads a card's expires: (time, true) if set and readable.
// A time that cannot be read counts as absent and is logged once per
// text (ledger F-9: Python's check failed there).
func (c *times) expires(v any) (time.Time, bool) {
	if !Truthy(v) {
		return time.Time{}, false
	}
	s, ok := v.(string)
	if ok {
		if r := c.read(s); r.ok {
			return r.t, true
		}
	}
	key := pyfmt.Str(v)
	c.mu.Lock()
	first := !c.bad[key]
	if c.bad == nil {
		c.bad = map[string]bool{}
	}
	if len(c.bad) < 4096 {
		c.bad[key] = true
	}
	c.mu.Unlock()
	if first && c.warn != nil {
		c.warn(key)
	}
	return time.Time{}, false
}

// SeatState is seat_state: blocked, expired, spent or waiting, ok.
func (c *times) SeatState(k pyjson.Obj, now time.Time) string {
	if Truthy(k.Val("blocked")) {
		return "blocked"
	}
	if exp, ok := c.expires(k.Val("expires")); ok && !exp.After(now) {
		return "expired"
	}
	if b := k.Val("max_budget"); b != nil {
		budget, okb := Number(b)
		spend := orZero(k.Val("spend"))
		if _, oks := Number(k.Val("spend")); okb &&
			(oks || k.Val("spend") == nil) && spend.F >= budget.F {
			// A budget that refills on a schedule is only waiting.
			if Truthy(k.Val("budget_duration")) {
				return "waiting"
			}
			return "spent"
		}
	}
	return "ok"
}

// CardID is the card's id: 16 hex of HMAC-SHA256(secret, token).
func CardID(secret []byte, token string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// Card is one key card, as the page gets it.
type Card struct {
	ID      string
	Seat    string
	Ends    string
	Models  any
	Budget  any
	Spent   pyjson.Num
	Refills any
	Resets  *string
	Expires any
	Created any
	Used    any
	State   string
	Game    bool
}

// Obj is the card as JSON, in frontdesk.py's key order.
func (c Card) Obj() pyjson.Obj {
	var resets any
	if c.Resets != nil {
		resets = *c.Resets
	}
	return pyjson.O("id", c.ID, "seat", c.Seat, "ends", c.Ends,
		"models", c.Models, "budget", c.Budget, "spent", c.Spent,
		"refills", c.Refills, "resets", resets, "expires", c.Expires,
		"created", c.Created, "used", c.Used, "state", c.State,
		"game", c.Game)
}

func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// lastRunes is s[-n:].
func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[len(r)-n:]
	}
	return string(r)
}

// BuildCards is build()'s card list and the id-to-token map.
func (c *times) BuildCards(keys []pyjson.Obj, secret []byte,
	game map[string]bool, now time.Time) ([]Card, map[string]string) {
	cards := make([]Card, 0, len(keys))
	tokens := map[string]string{}
	for _, k := range keys {
		token := text(k.Val("token"))
		id := CardID(secret, token)
		tokens[id] = token
		models := k.Val("models")
		if !Truthy(models) {
			models = []any{}
		}
		var refills any
		if Truthy(k.Val("budget_duration")) {
			refills = k.Val("budget_duration")
		}
		seat := text(k.Val("key_alias"))
		cards = append(cards, Card{
			ID: id, Seat: seat,
			Ends:   lastRunes(text(k.Val("key_name")), 4),
			Models: models, Budget: k.Val("max_budget"),
			Spent: Dollars(k.Val("spend")), Refills: refills,
			Resets:  c.UTCText(k.Val("budget_reset_at")),
			Expires: k.Val("expires"), Created: k.Val("created_at"),
			Used: k.Val("last_active"), State: c.SeatState(k, now),
			Game: game[seat],
		})
	}
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		if (a.Seat == "") != (b.Seat == "") {
			return a.Seat != ""
		}
		return pyfmt.Lower(a.Seat) < pyfmt.Lower(b.Seat)
	})
	return cards, tokens
}
