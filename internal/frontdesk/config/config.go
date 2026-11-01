// Package config is Front Desk's settings: seats.env in host mode,
// reread when it changes, or the environment once with --docker; and
// the log, one "HH:MM:SS msg" line per write.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/litellm"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// Log writes "HH:MM:SS msg" lines in local time, one write per line,
// so lines from many goroutines never mix.
type Log struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

// NewLog logs to w (Front Desk: stderr).
func NewLog(w io.Writer) *Log { return &Log{w: w, now: time.Now} }

// SetNow sets the log's clock, for tests.
func (l *Log) SetNow(now func() time.Time) { l.now = now }

// Line logs one message.
func (l *Log) Line(msg string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	io.WriteString(l.w, l.now().Format("15:04:05")+" "+msg+"\n")
}

// Defaults are the mint form's starting values.
type Defaults struct {
	Models   []string
	Budget   string
	Duration string
}

// Settings are one reading of seats.env (or the environment).
type Settings struct {
	Path     string // absolute; "" with --docker
	Docker   bool
	File     *seats.File
	Master   string
	DBURL    string
	HTTPS    bool
	Host     string // the proxy's host name, lowercased
	Port     int    // its port: given, or 443 or 80
	Base     string // PROXY_URL, checked, for the litellm client
	Defaults Defaults
	// Hidden is every value that must never reach a browser or a log.
	Hidden   []string
	Warnings []string

	mtime time.Time
}

// ChangeMe is the warning for a seats.env still holding the
// template's change-me values.
const ChangeMe = "seats.env still has change-me values, so mint-key " +
	"will refuse to run. Do README Step 3a."

// StartError is a setting Front Desk cannot start with; its text is
// the whole message.
type StartError struct{ Msg string }

func (e *StartError) Error() string { return e.Msg }

// LoadFile reads seats.env at path (host mode). env answers for
// names the file leaves out (nil: the real environment). Errors are
// *StartError, in frontdesk.py's words where it had them.
func LoadFile(path string, env seats.LookupFunc) (*Settings, error) {
	abs, err := filepath.Abs(path)
	if err == nil {
		if r, e := filepath.EvalSymlinks(abs); e == nil {
			abs = r
		}
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		return nil, &StartError{"missing " + path +
			": do README Step 3 first"}
	}
	f, err := seats.Load(abs, env)
	if err != nil {
		var se *seats.SyntaxError
		if errors.As(err, &se) || errors.Is(err, seats.ErrUTF16) {
			return nil, &StartError{err.Error()}
		}
		return nil, &StartError{"missing " + path +
			": do README Step 3 first"}
	}
	s, err := build(f, f.Names(), false, abs)
	if err != nil {
		return nil, err
	}
	s.Path = abs
	s.mtime = st.ModTime()
	return s, nil
}

// FromEnviron is the --docker settings: environ, as os.Environ()
// gives it.
func FromEnviron(environ []string) (*Settings, error) {
	f := seats.FromEnviron(environ)
	return build(f, f.Names(), true, "")
}

func build(f *seats.File, names []string, docker bool,
	path string) (*Settings, error) {
	s := &Settings{Docker: docker, File: f}
	s.Master, _ = f.Lookup("LITELLM_MASTER_KEY")
	s.DBURL, _ = f.Lookup("DATABASE_URL")
	raw := f.Get("PROXY_URL", "http://localhost:4000")
	where := path
	if docker {
		where = "the environment"
	}
	u, err := litellm.ParseBase(raw)
	if err != nil {
		return nil, &StartError{"PROXY_URL in " + where + " " +
			err.Error()}
	}
	s.Base = raw
	s.HTTPS = u.Scheme == "https"
	s.Host = pyfmt.Lower(u.Hostname())
	if s.Host == "" {
		s.Host = "localhost"
	}
	s.Port = 80
	if s.HTTPS {
		s.Port = 443
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n > 65535 {
			return nil, &StartError{"PROXY_URL in " + where +
				" has a bad port. It should look like " +
				"http://localhost:4000"}
		}
		if n != 0 {
			s.Port = n
		}
	}
	for _, m := range strings.Split(f.Get("DEFAULT_MODELS", "local"),
		",") {
		if m = pyfmt.Strip(m); m != "" {
			s.Defaults.Models = append(s.Defaults.Models, m)
		}
	}
	if s.Defaults.Models == nil {
		s.Defaults.Models = []string{}
	}
	s.Defaults.Budget = f.Get("DEFAULT_BUDGET", "5")
	s.Defaults.Duration = f.Get("DEFAULT_DURATION", "30d")
	seen := map[string]bool{}
	for _, k := range names {
		if seen[k] {
			continue
		}
		seen[k] = true
		v, _ := f.Lookup(k)
		if len([]rune(v)) < 6 {
			continue
		}
		if k == "DATABASE_URL" || strings.Contains(k, "KEY") ||
			strings.Contains(k, "PASSWORD") ||
			strings.Contains(k, "SECRET") ||
			strings.Contains(k, "TOKEN") {
			s.Hidden = append(s.Hidden, v)
		}
	}
	salt, _ := f.Lookup("LITELLM_SALT_KEY")
	if strings.Contains(s.Master+salt, "change-me") {
		s.Warnings = append(s.Warnings, ChangeMe)
	}
	return s, nil
}

// Scrub replaces each hidden value by [hidden], in list order.
func (s *Settings) Scrub(text string) string {
	for _, v := range s.Hidden {
		text = strings.ReplaceAll(text, v, "[hidden]")
	}
	return text
}

// Live is the settings now, reread when seats.env changes (host mode
// only; with --docker a new seats.env means a new container).
type Live struct {
	mu  sync.Mutex
	cur *Settings
	log *Log
	env seats.LookupFunc
}

// NewLive starts from s.
func NewLive(s *Settings, log *Log, env seats.LookupFunc) *Live {
	return &Live{cur: s, log: log, env: env}
}

// Get is the settings now.
func (l *Live) Get() *Settings {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur
}

// Scrub is Get().Scrub(text).
func (l *Live) Scrub(text string) string { return l.Get().Scrub(text) }

// ReloadIfChanged rereads seats.env if its time changed, and logs
// "reread seats.env". Any error keeps the old settings, silently.
func (l *Live) ReloadIfChanged() {
	cur := l.Get()
	if cur.Docker || cur.Path == "" {
		return
	}
	st, err := os.Stat(cur.Path)
	if err != nil || st.ModTime().Equal(cur.mtime) {
		return
	}
	s, err := LoadFile(cur.Path, l.env)
	if err != nil {
		return
	}
	l.mu.Lock()
	l.cur = s
	l.mu.Unlock()
	l.log.Line("reread seats.env")
}

// String is for tests: the settings without any secret.
func (s *Settings) String() string {
	return fmt.Sprintf("host=%s port=%d https=%v defaults=%v",
		s.Host, s.Port, s.HTTPS, s.Defaults)
}
