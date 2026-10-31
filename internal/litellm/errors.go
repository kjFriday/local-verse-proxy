package litellm

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// StatusError means the proxy answered, but with an error status.
type StatusError struct {
	Code     int    // HTTP status code, like 401
	Message  string // LiteLLM's own words, if it gave any
	Location string // where a redirect pointed, if it was one
}

// Status is the code and its name, like "error: 401 (Unauthorized)".
// Front Desk looks for "error: 401" and "error: 404" in mint-key's
// messages, as curl prints them.
func (e *StatusError) Status() string {
	s := fmt.Sprintf("error: %d", e.Code)
	if t := http.StatusText(e.Code); t != "" {
		s += " (" + t + ")"
	}
	return s
}

// Hint says what to do about the error, or "" if there is nothing
// general to say.
func (e *StatusError) Hint() string {
	switch {
	case e.Code == http.StatusUnauthorized:
		return "It did not take the master key. If seats.env " +
			"changed, run: docker compose up -d --force-recreate " +
			"proxy (or, if Front Desk runs in Docker: docker " +
			"compose --profile frontdesk up -d)"
	case e.Code >= 300 && e.Code < 400:
		where := ""
		if e.Location != "" {
			where = " (to " + e.Location + ")"
		}
		return "It sent the request elsewhere" + where + ". Check " +
			"PROXY_URL in seats.env: http or https, and the port."
	case e.Code >= 500:
		return "Its database may be down: run docker compose ps"
	}
	return ""
}

func (e *StatusError) Error() string {
	s := "the proxy returned " + e.Status()
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// AnswerError means the proxy said yes, but its answer is not what
// was expected.
type AnswerError struct {
	Problem string // like "it has no key in it"
	Body    string // the start of the answer, if it helps
}

func (e *AnswerError) Error() string {
	s := "the proxy answered, but " + e.Problem
	if e.Body != "" {
		s += ": " + e.Body
	}
	return s
}

// ConnError means no answer came from the proxy at all.
type ConnError struct {
	URL    string // the proxy's address, without any password
	Reason string // in plain words, like "connection refused"
	Hint   string // what to do
	Err    error  // the underlying error

	// Late means the whole request went out, but no answer came in
	// time. The proxy may be only slow, and may still do what was
	// asked: make the key, say. Hint says so.
	Late bool
}

// Error starts with "Failed to connect", which Front Desk looks for,
// as curl prints it.
func (e *ConnError) Error() string {
	return "Failed to connect to the proxy at " + e.URL + " (" +
		e.Reason + ")"
}

func (e *ConnError) Unwrap() error { return e.Err }

const (
	hintStart = "Is it running? Check with: docker compose ps. " +
		"Just started? Wait 30 seconds, then try again."
	hintURL  = "Check PROXY_URL in seats.env."
	hintLate = "It got the request but did not answer in time, so " +
		"it may have done it anyway."
)

// connError words a request that got no answer. sent says whether the
// whole request went out first.
func (c *Client) connError(err error, sent bool) error {
	wait := c.http.Timeout.String()
	if t := c.http.Timeout; t%time.Second == 0 {
		wait = fmt.Sprintf("%d seconds", t/time.Second)
	}
	reason, hint := explain(err, wait)
	ce := &ConnError{URL: c.BaseURL(), Reason: c.scrub(reason),
		Hint: hint, Err: err}
	if sent && timedOut(err) {
		ce.Late, ce.Hint = true, hintLate
	}
	return ce
}

func timedOut(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &netErr) && netErr.Timeout()
}

// explain puts a failed request in plain words.
func explain(err error, timeout string) (reason, hint string) {
	var (
		dns     *net.DNSError
		unknown x509.UnknownAuthorityError
		badName x509.HostnameError
		invalid x509.CertificateInvalidError
		record  tls.RecordHeaderError
	)
	text := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) {
		text = ue.Err.Error()
	}
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "proxyconnect"):
		return "the web proxy in HTTPS_PROXY or HTTP_PROXY failed: " +
				text,
			"Check that web proxy, or add this address to NO_PROXY."
	case errors.As(err, &dns) && !strings.Contains(dns.Name, "."):
		// A one-word name such as "proxy" is a Compose service: in
		// Docker it has no name while it is stopped.
		return "cannot find the computer named " + dns.Name,
			hintStart + " " + hintURL
	case errors.As(err, &dns):
		return "cannot find the computer named " + dns.Name, hintURL
	case errors.As(err, &unknown), errors.As(err, &badName),
		errors.As(err, &invalid), strings.Contains(low, "x509:"):
		return "its HTTPS certificate is not trusted: " + text, hintURL
	case errors.As(err, &record),
		strings.Contains(low, "http response to https client"):
		return "it does not speak HTTPS on that port", hintURL +
			" Most setups use http://, not https://."
	case timedOut(err):
		return "no answer within " + timeout, hintStart
	case errors.Is(err, syscall.ECONNREFUSED),
		strings.Contains(low, "refused"):
		return "connection refused: nothing is listening there",
			hintStart
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, syscall.ECONNRESET),
		strings.Contains(low, "reset"),
		strings.Contains(low, "forcibly closed"):
		// curl's (52) and (56). Windows words a reset its own way.
		return "it hung up without answering", hintStart
	}
	return text, hintStart
}

// Message picks LiteLLM's own words out of an error answer, the way
// mint-key.sh does with jq: .error.message, else .detail, else the
// whole answer. Text that is not a JSON string is shown as JSON,
// indented; an answer that is not JSON at all is shown as it is.
func Message(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	if !json.Valid(body) {
		return string(body)
	}
	// Where jq would stop with an error, the script prints the
	// whole answer as it is: an answer that is not an object, or
	// an "error" that is not one.
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil || top == nil {
		return string(body)
	}
	pick := json.RawMessage(body)
	var errObj map[string]json.RawMessage
	switch e := top["error"]; {
	case e != nil && string(e) != "null" &&
		(json.Unmarshal(e, &errObj) != nil || errObj == nil):
		return string(body)
	case truthy(errObj["message"]):
		pick = errObj["message"]
	case truthy(top["detail"]):
		pick = top["detail"]
	}
	var s string
	if json.Unmarshal(pick, &s) == nil {
		return s
	}
	var out bytes.Buffer
	if json.Indent(&out, pick, "", "  ") != nil {
		return string(pick)
	}
	return out.String()
}

// truthy is jq's idea of true: present, and not null or false.
func truthy(v json.RawMessage) bool {
	s := string(bytes.TrimSpace(v))
	return s != "" && s != "null" && s != "false"
}
