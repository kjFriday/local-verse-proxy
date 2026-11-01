// Package litellm calls a LiteLLM proxy's admin API with the master
// key: minting and revoking virtual keys, for now. mint-key uses it;
// Front Desk and the office game can, once they are in Go.
//
// The master key goes only in the Authorization header of requests to
// the proxy. It is never logged, and it is cut out of every error.
package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultTimeout is how long one request may take, from connecting to
// the last byte of the answer. It is under the 30 seconds Front Desk
// gives mint-key.sh, so a slow proxy gets a message, not a kill.
const DefaultTimeout = 25 * time.Second

// maxAnswer is the most of an answer that is read, by default.
const maxAnswer = 1 << 20

// Client talks to one proxy. Its methods may be called at once from
// several goroutines.
type Client struct {
	base      *url.URL
	master    string
	http      *http.Client
	maxAnswer int64

	// UserAgent names the program in each request.
	UserAgent string
}

// Options are how NewWith makes a Client.
type Options struct {
	BaseURL, MasterKey, UserAgent string

	Timeout   time.Duration // whole request; 0: DefaultTimeout
	MaxAnswer int64         // bytes of an answer read; 0: 1 MiB
	EnvProxy  bool          // HTTPS_PROXY etc.; New sets true
}

// New makes a Client for the proxy at baseURL, like
// http://localhost:4000. As with curl, a URL with no scheme means
// http://. timeout 0 means DefaultTimeout.
//
// Requests honour HTTPS_PROXY, HTTP_PROXY and NO_PROXY (and their
// lowercase forms), except that localhost and loopback addresses such
// as 127.0.0.1 are always reached directly. Redirects are not
// followed, so the master key goes nowhere else.
func New(baseURL, masterKey string, timeout time.Duration) (*Client,
	error) {
	return NewWith(Options{BaseURL: baseURL, MasterKey: masterKey,
		UserAgent: "local-verse-proxy", Timeout: timeout,
		EnvProxy: true})
}

// NewWith makes a Client as o says. With EnvProxy false no web proxy
// is ever used, whatever the environment says. An empty UserAgent
// sends none.
func NewWith(o Options) (*Client, error) {
	u, err := ParseBase(o.BaseURL)
	if err != nil {
		return nil, err
	}
	if o.MasterKey == "" {
		return nil, errors.New("no master key")
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxAnswer <= 0 {
		o.MaxAnswer = maxAnswer
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	if o.EnvProxy {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &Client{
		base:   u,
		master: o.MasterKey,
		http: &http.Client{
			Transport:     tr,
			Timeout:       o.Timeout,
			CheckRedirect: noRedirects,
		},
		maxAnswer: o.MaxAnswer,
		UserAgent: o.UserAgent,
	}, nil
}

func noRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// ParseBase checks a proxy address, such as PROXY_URL in seats.env.
// It adds http:// if no scheme is given and drops a trailing slash.
// Its error starts "is not a web address", for the caller to name
// the setting in front; it leaves out the address, which might hold
// a password.
func ParseBase(s string) (*url.URL, error) {
	raw := strings.TrimSpace(s)
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err == nil && u.Scheme != "http" && u.Scheme != "https" {
		err = errors.New("it must start with http:// or https://")
	} else if err == nil && u.Host == "" {
		err = errors.New("it has no host name")
	} else if err == nil && (u.RawQuery != "" || u.Fragment != "") {
		err = errors.New("it may not have a ? or # part")
	}
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("is not a web address (%v). It "+
			"should look like http://localhost:4000", err)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// BaseURL is the proxy's address, without any password in it.
func (c *Client) BaseURL() string { return c.base.Redacted() }

// Post sends in as JSON to path, such as /key/generate, and decodes a
// JSON answer into out. A nil out skips the answer.
func (c *Client) Post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, body, out)
}

// Get asks for path, such as /key/info?key=..., and decodes the JSON
// answer into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// Raw GETs path and gives the status and body as they came, with
// or without the master key. err is a transport error, with the
// master key cut out, or *TooBigError: the answer is longer than
// MaxAnswer, and body holds its first MaxAnswer bytes.
func (c *Client) Raw(ctx context.Context, path string,
	auth bool) (status int, body []byte, err error) {
	u, err := c.url(path)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, c.hide(err)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.master)
	}
	req.Header["User-Agent"] = []string{c.UserAgent}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, c.hide(err)
	}
	defer resp.Body.Close()
	// As io.ReadAll, but in one piece when the length is known: a
	// page of 100 key cards is half a megabyte.
	var buf bytes.Buffer
	if n := resp.ContentLength; n > 0 && n <= c.maxAnswer {
		buf.Grow(int(n) + bytes.MinRead)
	}
	_, err = buf.ReadFrom(io.LimitReader(resp.Body, c.maxAnswer+1))
	body = buf.Bytes()
	if body == nil {
		body = []byte{}
	}
	if err != nil {
		return resp.StatusCode, body, c.hide(err)
	}
	if int64(len(body)) > c.maxAnswer {
		return resp.StatusCode, body[:c.maxAnswer],
			&TooBigError{Max: c.maxAnswer}
	}
	return resp.StatusCode, body, nil
}

// TooBigError is an answer longer than a Client reads.
type TooBigError struct{ Max int64 }

func (e *TooBigError) Error() string {
	return fmt.Sprintf("the proxy's answer is over %d bytes", e.Max)
}

// hidden is an error whose text has the master key cut out; errors.As
// and errors.Is still see what it wraps.
type hidden struct {
	err  error
	text string
}

func (h *hidden) Error() string { return h.text }
func (h *hidden) Unwrap() error { return h.err }

func (c *Client) hide(err error) error {
	return &hidden{err: err, text: c.scrub(err.Error())}
}

func (c *Client) url(path string) (string, error) {
	ref, err := url.Parse(path)
	if err != nil || ref.Scheme != "" || ref.Host != "" {
		return "", fmt.Errorf("bad API path %q", path)
	}
	u := *c.base
	u.Path += "/" + strings.TrimLeft(ref.Path, "/")
	u.RawQuery = ref.RawQuery
	return u.String(), nil
}

func (c *Client) do(ctx context.Context, method, path string,
	body []byte, out any) error {
	u, err := c.url(path)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	// Whether the whole request went out: if so, a proxy that is only
	// slow may still act on it.
	var sent atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err == nil {
				sent.Store(true)
			}
		},
	})
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.master)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header["User-Agent"] = []string{c.UserAgent}
	resp, err := c.http.Do(req)
	if err != nil {
		return c.connError(err, sent.Load())
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, c.maxAnswer))
	if err != nil {
		return c.connError(err, true)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{
			Code:     resp.StatusCode,
			Message:  c.scrub(Message(answer)),
			Location: c.where(resp.Header.Get("Location")),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return &AnswerError{Problem: "it is not the JSON expected",
			Body: clip(c.scrub(string(answer)))}
	}
	return nil
}

// scrub cuts the master key out of text, leaving its last 4
// characters, as LiteLLM itself does. Text cut short (at the most an
// answer is read) may end in the key's first characters: those go
// too. A proxy that echoes the request would otherwise show the key.
func (c *Client) scrub(s string) string {
	m := c.master
	s = strings.ReplaceAll(s, m, Mask(m))
	for k := min(len(m)-1, len(s)); k >= 4; k-- {
		if strings.HasSuffix(s, m[:k]) {
			return s[:len(s)-k] + Mask(m)
		}
	}
	return s
}

// where is a redirect's Location as a message shows it: scheme, host
// and path, the path decoded and scrubbed; no user, query or
// fragment, which could carry the master key in any spelling.
func (c *Client) where(loc string) string {
	if loc == "" {
		return ""
	}
	u, err := url.Parse(loc)
	if err != nil {
		return "an address that cannot be read"
	}
	out := ""
	if u.Host != "" {
		out = u.Scheme + "://" + u.Host
	}
	return c.scrub(out + u.Path)
}

// Mask shows a key as sk-...last4, the way LiteLLM's own messages do.
func Mask(key string) string {
	if len(key) <= 8 {
		return "sk-..."
	}
	return "sk-..." + key[len(key)-4:]
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// GenerateRequest is the body of /key/generate, as mint-key sends it.
type GenerateRequest struct {
	KeyAlias  string   `json:"key_alias"`
	Models    []string `json:"models"`
	MaxBudget float64  `json:"max_budget"` // US dollars
	Duration  string   `json:"duration"`   // like 30d; m is minutes
	Metadata  Metadata `json:"metadata"`
	// BudgetDuration refills the budget, like 1d. Empty: the budget is
	// a lifetime total.
	BudgetDuration string `json:"budget_duration,omitempty"`
}

// Metadata is stored with a key.
type Metadata struct {
	Seat string `json:"seat"`
}

// GenerateKey mints a key and returns it. It refuses an empty model
// list, which LiteLLM would read as "all models".
func (c *Client) GenerateKey(ctx context.Context, r GenerateRequest) (
	string, error) {
	if len(r.Models) == 0 {
		return "", errors.New("no models named; LiteLLM would " +
			"allow them all")
	}
	var out struct {
		Key any `json:"key"`
	}
	if err := c.Post(ctx, "/key/generate", r, &out); err != nil {
		return "", err
	}
	key, _ := out.Key.(string)
	if key == "" {
		return "", &AnswerError{Problem: "it has no key in it"}
	}
	return key, nil
}

// DeleteKeys revokes keys, given the keys themselves.
func (c *Client) DeleteKeys(ctx context.Context, keys ...string) error {
	return c.Post(ctx, "/key/delete",
		map[string][]string{"keys": keys}, nil)
}

// DeleteSeats revokes keys by their seat names (key aliases). LiteLLM
// answers 404, "No keys found", if a name has no key.
func (c *Client) DeleteSeats(ctx context.Context, seats ...string) error {
	return c.Post(ctx, "/key/delete",
		map[string][]string{"key_aliases": seats}, nil)
}
