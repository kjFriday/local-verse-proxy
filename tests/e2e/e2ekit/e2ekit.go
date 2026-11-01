// Package e2ekit is what the real-stack tests under tests/e2e share:
// their settings from the environment, requests sent from a chosen
// loopback address (a "device"), logging in, and the secret sweep.
// It uses the standard library only, so the import rules do not
// concern it.
//
// Every test that uses it is built with the e2e tag and needs a
// running Front Desk: see tests/e2e/MANUAL.md for how to start one
// and which variables to set.
package e2ekit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Desk is one Front Desk under test.
type Desk struct {
	Port int    // on 127.0.0.1
	Host string // the Host header sent
	Mode string // "lan": loopback aliases are devices; "docker": one
	PW   string // the password ("" if none)
	Log  string // the file its log goes to ("" if unknown)
	PID  int    // its process, for counting threads (0 if unknown)
}

// FromEnv reads E2E_PORT, E2E_HOST, E2E_MODE, E2E_PWFILE, E2E_LOG and
// E2E_PID. Without E2E_PORT the test is skipped.
func FromEnv(t testing.TB) *Desk {
	t.Helper()
	p, _ := strconv.Atoi(os.Getenv("E2E_PORT"))
	if p == 0 {
		t.Skip("E2E_PORT is not set: no Front Desk to test " +
			"(tests/e2e/MANUAL.md)")
	}
	d := &Desk{Port: p, Host: os.Getenv("E2E_HOST"),
		Mode: os.Getenv("E2E_MODE"), Log: os.Getenv("E2E_LOG")}
	if d.Host == "" {
		d.Host = fmt.Sprintf("192.0.2.2:%d", p)
	}
	if d.Mode == "" {
		d.Mode = "lan"
	}
	if f := os.Getenv("E2E_PWFILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d.PW = strings.TrimSpace(string(b))
	}
	d.PID, _ = strconv.Atoi(os.Getenv("E2E_PID"))
	return d
}

// Devices tells whether loopback aliases count as separate devices.
func (d *Desk) Devices() bool { return d.Mode == "lan" }

// Src is the address to send from: src itself in lan mode, else
// 127.0.0.1 (in Docker every device looks the same anyway).
func (d *Desk) Src(src string) string {
	if d.Devices() {
		return src
	}
	return "127.0.0.1"
}

// Dial opens a connection from src.
func (d *Desk) Dial(src string, timeout time.Duration) (net.Conn,
	error) {
	dl := net.Dialer{Timeout: timeout,
		LocalAddr: &net.TCPAddr{IP: net.ParseIP(d.Src(src))}}
	return dl.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", d.Port))
}

// Answer is one parsed answer.
type Answer struct {
	Status int
	Header http.Header
	Body   []byte
	JSON   map[string]any
}

// Err is the "error" field of a JSON answer, or "".
func (a *Answer) Err() string {
	s, _ := a.JSON["error"].(string)
	return s
}

// Req sends one request from src and reads the answer. body, if not
// nil, is sent as JSON; raw, if not nil, as it is.
func (d *Desk) Req(src, method, path string, body any, raw []byte,
	h map[string]string) (*Answer, error) {
	c, err := d.Dial(src, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\n", method, path,
		d.Host)
	if body != nil && raw == nil {
		raw, _ = json.Marshal(body)
		if h == nil || h["Content-Type"] == "" {
			b.WriteString("Content-Type: application/json\r\n")
		}
	}
	for k, v := range h {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	if raw != nil {
		fmt.Fprintf(&b, "Content-Length: %d\r\n", len(raw))
	}
	b.WriteString("Connection: close\r\n\r\n")
	b.Write(raw)
	if _, err := c.Write(b.Bytes()); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	a := &Answer{Status: resp.StatusCode, Header: resp.Header}
	a.Body, _ = io.ReadAll(resp.Body)
	RecordAnswer(resp.Header, a.Body)
	json.Unmarshal(a.Body, &a.JSON)
	return a, nil
}

// Raw sends data from src and gives every byte that came back, until
// the server closes or timeout passes ("<timeout>" is added then).
func (d *Desk) Raw(src string, data []byte,
	timeout time.Duration) (first string, all []byte) {
	c, err := d.Dial(src, timeout)
	if err != nil {
		return "refused (" + err.Error() + ")", nil
	}
	defer c.Close()
	c.Write(data)
	c.SetReadDeadline(time.Now().Add(timeout))
	all, err = io.ReadAll(c)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		all = append(all, "<timeout>"...)
	}
	Record(all)
	first, _, _ = strings.Cut(string(all), "\r\n")
	return first, all
}

// Login posts the password from src.
func (d *Desk) Login(src, pw string) (*Answer, error) {
	return d.Req(src, "POST", "/api/login",
		map[string]string{"password": pw}, nil, nil)
}

// Session logs in from src and gives the Cookie header and csrf.
func (d *Desk) Session(t testing.TB, src string) (cookie, csrf string) {
	t.Helper()
	if d.PW == "" {
		a, err := d.Req(src, "GET", "/api/session", nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := a.JSON["csrf"].(string)
		return "", c
	}
	a, err := d.Login(src, d.PW)
	if err != nil || a.Status != 200 {
		t.Fatalf("login from %s: %v %v", src, err, a)
	}
	ck, _, _ := strings.Cut(a.Header.Get("Set-Cookie"), ";")
	c, _ := a.JSON["csrf"].(string)
	return ck, c
}

// Page loads / from src and says how it went, as the reference
// scripts print it: "200 7631B in 1.9 ms" or "refused (...)".
func (d *Desk) Page(src string) string {
	t0 := time.Now()
	c, err := d.Dial(src, 5*time.Second)
	if err != nil {
		return "refused (" + kind(err) + ")"
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", d.Host)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return "refused (" + kind(err) + ")"
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return fmt.Sprintf("%d %dB in %.1f ms", resp.StatusCode, n,
		float64(time.Since(t0).Microseconds())/1000)
}

// kind names an error as Python's class names did in the outputs.
func kind(err error) string {
	s := err.Error()
	switch {
	case err == io.EOF || err == io.ErrUnexpectedEOF ||
		strings.Contains(s, "EOF"):
		return "RemoteDisconnected"
	case strings.Contains(s, "reset"):
		return "ConnectionResetError"
	case strings.Contains(s, "refused"):
		return "ConnectionRefusedError"
	case strings.Contains(s, "timeout"):
		return "TimeoutError"
	}
	return s
}

// Threads counts the desk's threads (Linux), or -1.
func (d *Desk) Threads() int {
	if d.PID == 0 {
		return -1
	}
	e, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", d.PID))
	if err != nil {
		return -1
	}
	return len(e)
}

// LogSize is the log file's size now, or -1.
func (d *Desk) LogSize() int64 {
	if d.Log == "" {
		return -1
	}
	fi, err := os.Stat(d.Log)
	if err != nil {
		return -1
	}
	return fi.Size()
}

// LogSince is the log written after offset.
func (d *Desk) LogSince(off int64) string {
	if d.Log == "" || off < 0 {
		return ""
	}
	b, _ := os.ReadFile(d.Log)
	if int64(len(b)) < off {
		return ""
	}
	return string(b[off:])
}

// Mask shows only the ends of a secret, as the scripts did.
func Mask(s string) string {
	if len(s) > 12 {
		return s[:3] + "..." + s[len(s)-4:]
	}
	return s
}

var (
	seenMu sync.Mutex
	seen   bytes.Buffer
)

// Record keeps bytes that came back, for the secret sweep. What a
// test sends is not kept: logins send the password on purpose.
func Record(b []byte) {
	seenMu.Lock()
	seen.Write(b)
	seen.WriteByte('\n')
	seenMu.Unlock()
}

// RecordAnswer keeps an answer's headers and body for the sweep.
func RecordAnswer(h http.Header, body []byte) {
	var b bytes.Buffer
	h.Write(&b)
	b.Write(body)
	Record(b.Bytes())
}

// Answers is every byte recorded so far.
func Answers() []byte {
	seenMu.Lock()
	defer seenMu.Unlock()
	return append([]byte(nil), seen.Bytes()...)
}

// Secrets are the values the sweep looks for: the secret settings of
// the seats.env named by E2E_SEATS_ENV, and the desk's password.
func Secrets(d *Desk) map[string]string {
	out := map[string]string{}
	if d.PW != "" {
		out["FRONTDESK_PASSWORD"] = d.PW
	}
	f := os.Getenv("E2E_SEATS_ENV")
	if f == "" {
		return out
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || len(v) < 8 {
			continue
		}
		switch k {
		case "LITELLM_MASTER_KEY", "LITELLM_SALT_KEY",
			"POSTGRES_PASSWORD", "FRONTDESK_PASSWORD":
			out[k] = strings.Trim(v, `"'`)
		}
	}
	return out
}

// Sweep reports each secret found in data, by name only.
func Sweep(data []byte, secrets map[string]string) []string {
	var hits []string
	for k, v := range secrets {
		if v != "" && bytes.Contains(data, []byte(v)) {
			hits = append(hits, k)
		}
	}
	return hits
}
