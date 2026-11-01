// Package gate is who may use Front Desk: the password, sessions,
// the login lockout and the CSRF tokens. A line-by-line port of
// frontdesk.py's Gate, with the clocks injected.
package gate

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
)

// The limits, as frontdesk.py.
const (
	SessionHours = 12
	LockAfter    = 5   // wrong passwords from one device in 10 minutes
	FailGap      = 1.0 // seconds between tries of devices that got it wrong
	MaxSessions  = 100
	MaxPassword  = 200 // code points
	lockSeconds  = 600
)

// Refused is a login that was not even checked. Wait: seconds to
// retry (Retry-After).
type Refused struct {
	Msg  string
	Wait int
}

func (e *Refused) Error() string { return e.Msg }

// WrongPassword is a password that was checked and was wrong: it
// counts. Locked: the one that locks its device for 10 minutes.
type WrongPassword struct {
	Msg    string
	Locked bool
}

func (e *WrongPassword) Error() string { return e.Msg }

type session struct {
	expires float64
	csrf    string
	addr    string
}

// Gate is who may use the desk. Without a password, everyone who can
// reach 127.0.0.1 may.
type Gate struct {
	has      bool
	want     [32]byte
	clk      clock.Clock
	rand     io.Reader
	mu       sync.Mutex
	sessions map[string]session
	order    []string // session cookies, oldest first (dict order)
	fails    map[string][]float64
	failKeys []string // fails' keys in dict order
	lastFail float64
	openCSRF string
}

// New makes a Gate. has false: no password. r nil: crypto/rand.
func New(password string, has bool, c clock.Clock, r io.Reader) *Gate {
	if r == nil {
		r = rand.Reader
	}
	if c == nil {
		c = clock.Real()
	}
	g := &Gate{has: has, clk: c, rand: r,
		sessions: map[string]session{},
		fails:    map[string][]float64{},
		lastFail: math.Inf(-1)}
	if has {
		g.want = sha256.Sum256([]byte(password))
	}
	g.openCSRF = g.token(24)
	return g
}

// HasPassword is false when anyone on this computer may use it.
func (g *Gate) HasPassword() bool { return g.has }

// OpenCSRF is the one token for everyone, with no password.
func (g *Gate) OpenCSRF() string { return g.openCSRF }

// token is secrets.token_urlsafe(n).
func (g *Gate) token(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(g.rand, b); err != nil {
		panic("gate: no random bytes: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// CSRFFor is the session's CSRF token, or false if not logged in. A
// session works only from the address that logged in.
func (g *Gate) CSRFFor(cookie, addr string) (string, bool) {
	if !g.has {
		return g.openCSRF, true
	}
	if cookie == "" {
		return "", false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessions[cookie]
	if ok && s.expires <= g.clk.Wall() {
		g.drop(cookie)
	} else if ok && s.addr == addr {
		return s.csrf, true
	}
	return "", false
}

func (g *Gate) drop(cookie string) {
	delete(g.sessions, cookie)
	for i, c := range g.order {
		if c == cookie {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
}

func (g *Gate) dropFail(addr string) {
	delete(g.fails, addr)
	for i, a := range g.failKeys {
		if a == addr {
			g.failKeys = append(g.failKeys[:i],
				g.failKeys[i+1:]...)
			break
		}
	}
}

// Login gives (cookie, csrf), or a *WrongPassword with a plain
// reason, or a *Refused before the password is even checked.
func (g *Gate) Login(addr, password string) (string, string, error) {
	now, tick := g.clk.Wall(), g.clk.Mono()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, a := range append([]string(nil), g.failKeys...) {
		q := g.fails[a]
		if len(q) == 0 || q[len(q)-1] < tick-lockSeconds {
			g.dropFail(a)
		}
	}
	mine, ok := g.fails[addr]
	if !ok {
		g.fails[addr] = nil
		g.failKeys = append(g.failKeys, addr)
	}
	if len(mine) >= LockAfter {
		// Locked for 10 minutes from the try that locked it: the
		// cleanup above keeps the record until then.
		wait := int(math.Max(1, math.Ceil(mine[len(mine)-1]+
			lockSeconds-tick)))
		mins := int(math.Ceil(float64(wait) / 60))
		m := strconv.Itoa(mins) + " minutes"
		if mins == 1 {
			m = "1 minute"
		}
		return "", "", &Refused{"Too many wrong passwords. Wait " +
			m + ".", wait}
	}
	for len(mine) > 0 && mine[0] < tick-lockSeconds {
		mine = mine[1:]
	}
	g.fails[addr] = mine
	// Devices that got it wrong lately get one try a second between
	// them all: far too slow to guess. A device with no wrong tries
	// is always let through, so nobody is kept out.
	if len(mine) > 0 && tick-g.lastFail < FailGap {
		return "", "", &Refused{"Too many tries just now. Wait a " +
			"second and try again.", 1}
	}
	got := sha256.Sum256([]byte(password))
	if !g.has || !hmac.Equal(got[:], g.want[:]) {
		mine = append(mine, tick)
		g.fails[addr] = mine
		g.lastFail = tick
		left := LockAfter - len(mine)
		if left <= 0 {
			return "", "", &WrongPassword{"Wrong password. That " +
				"was the last try: wait 10 minutes.", true}
		}
		tries := strconv.Itoa(left) + " tries"
		if left == 1 {
			tries = "1 try"
		}
		return "", "", &WrongPassword{"Wrong password. " + tries +
			" left before a 10-minute lock.", false}
	}
	g.dropFail(addr)
	for _, c := range append([]string(nil), g.order...) {
		if g.sessions[c].expires < now {
			g.drop(c)
		}
	}
	if len(g.sessions) >= MaxSessions {
		oldest := ""
		for _, c := range g.order {
			if oldest == "" || less(g.sessions[c],
				g.sessions[oldest]) {
				oldest = c
			}
		}
		g.drop(oldest)
	}
	cookie := g.token(32)
	csrf := g.token(24)
	g.sessions[cookie] = session{now + SessionHours*3600, csrf, addr}
	g.order = append(g.order, cookie)
	return cookie, csrf, nil
}

// less is Python's tuple order on (expires, csrf, address).
func less(a, b session) bool {
	if a.expires != b.expires {
		return a.expires < b.expires
	}
	if a.csrf != b.csrf {
		return a.csrf < b.csrf
	}
	return a.addr < b.addr
}

// Logout ends a session.
func (g *Gate) Logout(cookie string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.sessions[cookie]; ok {
		g.drop(cookie)
	}
}

// Sessions is how many sessions are kept, for tests.
func (g *Gate) Sessions() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sessions)
}

const abc = "abcdefghjkmnpqrstuvwxyz23456789"

// MakePassword is 16 easy-to-type characters, about 79 bits, dashed
// in fours.
func MakePassword() string {
	var raw []string
	for i := 0; i < 16; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(abc))))
		if err != nil {
			panic("gate: no random bytes: " + err.Error())
		}
		raw = append(raw, abc[n.Int64():n.Int64()+1])
	}
	s := strings.Join(raw, "")
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}
