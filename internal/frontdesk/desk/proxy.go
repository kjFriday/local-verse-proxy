package desk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/config"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
	"github.com/kjFriday/local-verse-proxy/internal/litellm"
)

// MasterRefused is the note when the proxy refuses the master key.
const MasterRefused = "The proxy did not accept the master key. If " +
	"seats.env changed, run: docker compose up -d --force-recreate proxy"

// MasterRefusedDocker is its --docker form: Front Desk keeps the
// seats.env it started with, so it may be the one out of date.
const MasterRefusedDocker = "Front Desk and the proxy have different " +
	"master keys. If seats.env changed, run: docker compose " +
	"--profile frontdesk up -d"

// ProxyError is a proxy answer in plain words, shown as it is.
type ProxyError struct{ Msg string }

func (e *ProxyError) Error() string { return e.Msg }

// TransportError is no answer at all; Class is Python's class name.
type TransportError struct {
	Class string
	Err   error
}

func (e *TransportError) Error() string { return e.Err.Error() }

// ShapeError is an answer that is not JSON, or not the JSON expected:
// Python's JSONDecodeError (ledger F-9, F-10).
type ShapeError struct{ Msg string }

func (e *ShapeError) Error() string { return e.Msg }

// Class names an error for "Could not read the key cards: <Class>".
func Class(err error) string {
	var t *TransportError
	var s *ShapeError
	switch {
	case errors.As(err, &t):
		return t.Class
	case errors.As(err, &s):
		return "JSONDecodeError"
	}
	return "OSError"
}

// Proxy is the desk's GETs to LiteLLM: no web proxy from the
// environment (as http.client), answers of up to 16 MiB.
type Proxy struct {
	mu     sync.Mutex
	live   *config.Live
	at     *config.Settings
	client *litellm.Client
	err    error
}

// NewProxy makes a Proxy that follows live's settings.
func NewProxy(live *config.Live) *Proxy { return &Proxy{live: live} }

func (p *Proxy) get(ctx context.Context, path string, auth bool,
	timeout float64) (int, []byte, error) {
	s := p.live.Get()
	p.mu.Lock()
	if p.at != s {
		master := s.Master
		if master == "" {
			master = "none" // never sent: auth is off then
		}
		p.client, p.err = litellm.NewWith(litellm.Options{
			BaseURL: s.Base, MasterKey: master,
			Timeout:   time.Minute,
			MaxAnswer: maxAnswer, EnvProxy: false})
		p.at = s
	}
	c, err := p.client, p.err
	p.mu.Unlock()
	if err != nil {
		return 0, nil, &TransportError{"OSError", err}
	}
	ctx, cancel := context.WithTimeout(ctx,
		time.Duration(timeout*float64(time.Second)))
	defer cancel()
	status, body, err := c.Raw(ctx, path, auth && s.Master != "")
	if err != nil {
		var big *litellm.TooBigError
		if errors.As(err, &big) {
			return 0, nil, err
		}
		return 0, nil, &TransportError{ClassName(err), err}
	}
	return status, body, nil
}

// Live is GET /health/liveliness without the master key, 2 s.
func (p *Proxy) Live(ctx context.Context) (int, error) {
	status, _, err := p.get(ctx, "/health/liveliness", false,
		liveTimeout)
	return status, err
}

// JSON is frontdesk.py's Proxy.json: 401 is the master key refused,
// another code than 200 a ProxyError, then the JSON decoded with
// numbers kept as json.Number (NaN refused: ledger F-10).
func (p *Proxy) JSON(ctx context.Context, path string) (any, error) {
	status, body, err := p.get(ctx, path, true, proxyTimeout)
	if err != nil {
		return nil, err
	}
	if status == 401 {
		if p.live.Get().Docker {
			return nil, &ProxyError{MasterRefusedDocker}
		}
		return nil, &ProxyError{MasterRefused}
	}
	if status != 200 {
		hint := ""
		if status >= 500 {
			hint = " Its database may be down: run docker " +
				"compose ps."
		}
		return nil, &ProxyError{fmt.Sprintf("The proxy answered %d "+
			"to %s.%s", status, strings.SplitN(path, "?", 2)[0],
			hint)}
	}
	return Decode(body)
}

// Decode reads one JSON value as pyjson.Decode; its errors are
// ShapeErrors (Python's JSONDecodeError).
func Decode(body []byte) (any, error) {
	v, err := pyjson.Decode(body)
	if err != nil {
		return nil, &ShapeError{err.Error()}
	}
	return v, nil
}
