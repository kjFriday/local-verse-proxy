package pgread

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// SessionConfig is how a Session starts psql.
type SessionConfig struct {
	Argv    []string      // default DefaultArgv
	Dir     string        // the proxy folder
	Env     []string      // nil: os.Environ()
	App     string        // application_name; "" is frontdesk
	Timeout time.Duration // per query, 8 s
	Backoff time.Duration // after a failure, 30 s
	Clock   clock.Clock   // nil: the real one
}

// DefaultArgv is Front Desk's psql: quiet, unaligned, tuples only,
// terse errors, in the db container.
var DefaultArgv = []string{"docker", "compose", "exec", "-T", "db",
	"psql", "-X", "-q", "-A", "-t", "-U", "litellm", "-d",
	"litellm", "-v", "VERBOSITY=terse"}

// Session is one long-lived psql (Front Desk host mode), a port of
// frontdesk.py's Psql: started on the first question, fed questions
// on its input, each ended by an \echo of a random mark.
type Session struct {
	c    SessionConfig
	mark func() string // the end-of-answer mark; tests fix it

	mu      sync.Mutex // one question at a time
	retryAt float64    // monotonic; refuse until then

	pm sync.Mutex // the process, for Stop
	p  *proc
}

type proc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *lineQueue
	done  chan struct{} // closed when it has exited
}

// NewSession makes a Session; psql starts on the first Query.
func NewSession(c SessionConfig) *Session {
	if len(c.Argv) == 0 {
		c.Argv = DefaultArgv
	}
	if c.App == "" {
		c.App = "frontdesk"
	}
	if c.Timeout <= 0 {
		c.Timeout = 8 * time.Second
	}
	if c.Backoff <= 0 {
		c.Backoff = 30 * time.Second
	}
	if c.Clock == nil {
		c.Clock = clock.Real()
	}
	return &Session{c: c, mark: randomMark, retryAt: -inf}
}

func randomMark() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "__fd_" + hex.EncodeToString(b)
}

// Query asks one question and gives the JSON object psql printed.
// A failure stops psql, and for Backoff every question gets "waiting
// before trying psql again". An answer that is not JSON is an error
// too, but psql keeps running (as in Python). A question whose ctx
// ends stops psql too (its answer may still come), but is not
// psql's failure: the next question starts a new psql at once.
// Text with a ";" before its end is refused, and psql never sees it.
func (s *Session) Query(ctx context.Context,
	sql string) (json.RawMessage, error) {
	if err := noBackslash(sql); err != nil {
		return nil, err
	}
	if err := oneStatement(sql); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c.Clock.Mono() < s.retryAt {
		return nil, fail("waiting before trying psql again")
	}
	text, err := s.query(ctx, sql)
	if err != nil {
		s.Stop()
		if ctx.Err() == nil {
			s.retryAt = s.c.Clock.Mono() + s.c.Backoff.Seconds()
		}
		return nil, err
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *Session) query(ctx context.Context, sql string) (string, error) {
	s.pm.Lock()
	p := s.p
	s.pm.Unlock()
	if p == nil || p.exited() {
		var err error
		if p, err = s.start(); err != nil {
			return "", err
		}
	}
	mark := s.mark()
	// It may have died: its last words are in the queue.
	io.WriteString(p.stdin, "begin read only;\n"+strings.TrimSpace(sql)+
		"\ncommit;\n\\echo "+mark+"\n")
	var lines []string
	timeout := s.c.Clock.After(s.c.Timeout.Seconds())
	for {
		line, ok, err := p.out.get(ctx, timeout)
		if err != nil {
			return "", err
		}
		if !ok {
			n := len(lines)
			var said []string
			for _, l := range lines[max(0, n-2):] {
				said = append(said, pyfmt.Strip(l))
			}
			if msg := strings.Join(said, " "); msg != "" {
				return "", fail(msg)
			}
			return "", fail("psql stopped")
		}
		if pyfmt.Strip(line) == mark {
			break
		}
		lines = append(lines, line)
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "ERROR") ||
			strings.HasPrefix(l, "FATAL") {
			return "", fail(pyfmt.Strip(l))
		}
	}
	// json_agg spreads over many lines.
	text := strings.Join(lines, "\n")
	i := strings.IndexByte(text, '{')
	if i < 0 {
		return "", fail("psql gave no answer")
	}
	return text[i:], nil
}

func (s *Session) start() (*proc, error) {
	cmd := exec.Command(s.c.Argv[0], s.c.Argv[1:]...)
	cmd.Dir = s.c.Dir
	cmd.Env = s.c.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fail("cannot run docker: " + pyfmt.StrError(err))
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fail("cannot run docker: " + pyfmt.StrError(err))
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, fail("cannot run docker: " + pyfmt.StrError(err))
	}
	pw.Close()
	p := &proc{cmd: cmd, stdin: stdin, out: newLineQueue(),
		done: make(chan struct{})}
	go p.out.fill(pr)
	go func() {
		cmd.Wait()
		close(p.done)
	}()
	s.pm.Lock()
	s.p = p
	s.pm.Unlock()
	app := strings.ReplaceAll(s.c.App, "'", "''")
	io.WriteString(stdin, "set default_transaction_read_only = on;\n"+
		"set statement_timeout = '5s';\n"+
		"set application_name = '"+app+"';\n")
	return p, nil
}

func (p *proc) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Stop kills psql, if it runs.
func (s *Session) Stop() {
	s.pm.Lock()
	p := s.p
	s.p = nil
	s.pm.Unlock()
	if p != nil {
		p.cmd.Process.Kill()
		p.stdin.Close()
	}
}

// lineQueue is the lines one psql printed, as Python's queue: it
// never blocks the reader, so a killed psql's reader always ends.
type lineQueue struct {
	mu    sync.Mutex
	lines []string
	eof   bool
	more  chan struct{} // closed and replaced when something comes
}

func newLineQueue() *lineQueue {
	return &lineQueue{more: make(chan struct{})}
}

func (q *lineQueue) fill(r io.ReadCloser) {
	defer r.Close()
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			line = strings.TrimSuffix(line, "\n")
			q.put(strings.TrimSuffix(line, "\r"), false)
		}
		if err != nil {
			q.put("", true)
			return
		}
	}
}

func (q *lineQueue) put(line string, eof bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if eof {
		q.eof = true
	} else {
		q.lines = append(q.lines, line)
	}
	close(q.more)
	q.more = make(chan struct{})
}

// get is the next line; ok false at the end. timeout closing first
// is "psql did not answer in time".
func (q *lineQueue) get(ctx context.Context,
	timeout <-chan struct{}) (string, bool, error) {
	for {
		q.mu.Lock()
		if len(q.lines) > 0 {
			line := q.lines[0]
			q.lines = q.lines[1:]
			q.mu.Unlock()
			return line, true, nil
		}
		if q.eof {
			q.mu.Unlock()
			return "", false, nil
		}
		more := q.more
		q.mu.Unlock()
		select {
		case <-more:
		case <-timeout:
			return "", false, fail("psql did not answer in time")
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
}
