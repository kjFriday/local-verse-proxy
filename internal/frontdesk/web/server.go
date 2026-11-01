// Package web is Front Desk's HTTP side: the server and its caps,
// the routes, the page's files, login, mint and revoke, the live
// stream, and the Host checks. A port of frontdesk.py's App, Server
// and Handler.
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/clock"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/config"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/desk"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/gate"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// MintFunc runs mint-key's rules: mintkey.Exec, or a test's stand-in.
type MintFunc func(ctx context.Context, file *seats.File, args []string,
	timeout time.Duration) mintkey.Result

// Options are how Front Desk was started.
type Options struct {
	Docker, LAN bool
	Port        int      // --port: what this server listens on
	PublicPort  int      // the port browsers use (FRONTDESK_PORT)
	Hosts       []string // --host and FRONTDESK_HOSTS
	SeatURL     string   // --seat-url or FRONTDESK_SEAT_URL
	Hostname    string   // this machine's name (socket.gethostname)
	LANIP       func() string
	ResetCmd    string // the page's reset command (spec 9.12)
	Live        *config.Live
	Log         *config.Log
	Desk        *desk.Desk
	Gate        *gate.Gate
	Clock       clock.Clock
	// MintFile is the seats.env minting reads, fresh for each call.
	MintFile func() (*seats.File, error)
	Mint     MintFunc // nil: mintkey.Exec
	Static   fs.FS    // nil: the embedded page
}

// App is the Handler: every request goes through ServeHTTP.
type App struct {
	o          Options
	static     map[string]staticFile
	localHosts map[string]bool
	names      map[string]bool
	cookieName string
	work       sync.Mutex // one mint or revoke at a time
}

// New makes the App.
func New(o Options) (*App, error) {
	if o.Clock == nil {
		o.Clock = clock.Real()
	}
	if o.LANIP == nil {
		o.LANIP = LANIP
	}
	if o.Mint == nil {
		o.Mint = mintkey.Exec
	}
	st, err := loadStatic(o.Static)
	if err != nil {
		return nil, err
	}
	a := &App{o: o, static: st,
		// One name per port, so two desks on one machine don't mix.
		cookieName: fmt.Sprintf("fd_%d", o.PublicPort),
		names:      names(o.Docker, o.Hostname, o.Hosts),
		localHosts: map[string]bool{}}
	for _, h := range []string{"127.0.0.1", "localhost", "[::1]"} {
		a.localHosts[fmt.Sprintf("%s:%d", h, o.Port)] = true
	}
	return a, nil
}

// Server is the http.Server for the App: Python's request limits,
// the 10 s timeouts, no log, OPTIONS * to our handler.
func (a *App) Server() *http.Server {
	return &http.Server{
		Handler:                      a,
		ReadHeaderTimeout:            ReadTimeout,
		ReadTimeout:                  ReadTimeout,
		IdleTimeout:                  ReadTimeout,
		MaxHeaderBytes:               1 << 20,
		ErrorLog:                     log.New(io.Discard, "", 0),
		DisableGeneralOptionsHandler: true,
	}
}

// SecurityHeaders go on every answer.
var SecurityHeaders = [][2]string{
	{"Content-Security-Policy", "default-src 'self'; img-src 'self' " +
		"data:; style-src 'self'; script-src 'self'; connect-src " +
		"'self'; base-uri 'none'; form-action 'self'; " +
		"frame-ancestors 'none'"},
	{"X-Content-Type-Options", "nosniff"},
	{"X-Frame-Options", "DENY"},
	{"Referrer-Policy", "no-referrer"},
}

// reply is one answer, as Handler.send took it.
type reply struct {
	code    int
	body    []byte
	ctype   string
	gz      []byte // nil: no zipped copy
	cache   string
	headers [][2]string
	close   bool // send_error, or the request asked to close
}

// errGone is a viewer that hung up or was too slow: no answer, no log.
var errGone = errors.New("gone")

// send writes r as Handler.send did: explicit Content-Length, never
// chunked; gzip when Accept-Encoding holds "gzip" (case matters).
func (a *App) send(w http.ResponseWriter, req *http.Request, r reply) {
	if r.ctype == "" {
		r.ctype = "application/json"
	}
	if r.cache == "" {
		r.cache = "no-store"
	}
	zipped := r.gz != nil &&
		strings.Contains(req.Header.Get("Accept-Encoding"), "gzip")
	data := r.body
	if zipped {
		data = r.gz
	}
	h := w.Header()
	h.Set("Server", "FrontDesk")
	h.Set("Content-Type", r.ctype)
	h.Set("Content-Length", itoa(len(data)))
	h.Set("Cache-Control", r.cache)
	if r.gz != nil {
		h.Set("Vary", "Accept-Encoding")
	}
	if zipped {
		h.Set("Content-Encoding", "gzip")
	}
	for _, kv := range SecurityHeaders {
		h.Set(kv[0], kv[1])
	}
	for _, kv := range r.headers {
		h.Set(kv[0], kv[1])
	}
	if req.Method == http.MethodPost || r.close || req.Close {
		// A refused POST may leave its body unread: don't reuse.
		h.Set("Connection", "close")
	}
	// The time to write starts now, not when the request came: a
	// mint may take 30 s, and its answer is the only place its key
	// is ever shown. As Python's timeout, it counts per write.
	http.NewResponseController(w).SetWriteDeadline(
		time.Now().Add(writeWait))
	w.WriteHeader(r.code)
	if req.Method != http.MethodHead {
		w.Write(data)
	}
}

// fail is an error answer: {"error": msg}, and the form field to fix.
func (a *App) fail(w http.ResponseWriter, req *http.Request, code int,
	msg, field string, headers ...[2]string) {
	o := pyjson.O("error", msg)
	if field != "" {
		o = append(o, pyjson.KV{K: "field", V: field})
	}
	a.send(w, req, reply{code: code, body: pyjson.Marshal(o),
		headers: headers})
}

// sendError is Handler.send_error: the status phrase, as JSON, and
// the connection closed.
func (a *App) sendError(w http.ResponseWriter, req *http.Request,
	code int) {
	phrase := phrases[code]
	if phrase == "" {
		phrase = "Error"
	}
	a.send(w, req, reply{code: code, close: true,
		body: pyjson.Marshal(pyjson.O("error", phrase+"."))})
}

// phrases are Python 3.13's HTTPStatus phrases for send_error.
var phrases = map[int]string{
	400: "Bad Request", 414: "URI Too Long",
	431: "Request Header Fields Too Large", 501: "Not Implemented",
}

// ServeHTTP is every request: Python's own limits, then the method.
func (a *App) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer func() {
		r := recover()
		if r == nil || r == http.ErrAbortHandler {
			if r != nil {
				panic(r)
			}
			return
		}
		if e, ok := r.(error); ok && errors.Is(e, errGone) {
			return
		}
		a.o.Log.Line("error: " + pyfmt.Cut(a.o.Live.Scrub(
			fmt.Sprint(r)), 200))
		msg := "Something went wrong at the desk."
		if req.Method == http.MethodPost {
			if a.o.Docker {
				msg += " See its log: docker compose logs " +
					"frontdesk"
			} else {
				msg += " See the terminal where Front Desk " +
					"runs."
			}
		}
		a.fail(w, req, 500, msg, "")
	}()
	line := len(req.Method) + 1 + len(req.RequestURI) + 1 +
		len(req.Proto) + 2
	if line > maxLine {
		a.sendError(w, req, 414)
		return
	}
	n := 0
	if req.Host != "" || len(req.Header["Host"]) > 0 {
		n++
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			n++
			if len(k)+len(v)+4 > maxLine {
				a.sendError(w, req, 431)
				return
			}
		}
	}
	// Python counts the blank line that ends them too.
	if n+1 > maxHeaders {
		a.sendError(w, req, 431)
		return
	}
	// As http.server (Python 3.12 and later): a path that starts
	// with // is cut to one /, so it is never read as a host.
	target := req.RequestURI
	if strings.HasPrefix(target, "//") {
		target = "/" + strings.TrimLeft(target, "/")
	}
	path, _, _ := strings.Cut(target, "?")
	switch req.Method {
	case http.MethodGet:
		a.get(w, req, path)
	case http.MethodHead:
		// Headers only, for the page's own files.
		if _, ok := a.static[path]; ok {
			a.get(w, req, path)
			return
		}
		a.fail(w, req, 405, "Use GET.", "")
	case http.MethodPost:
		a.post(w, req, path)
	default:
		a.sendError(w, req, 501)
	}
}

// Shutdown ends the server: used on Ctrl+C.
func Shutdown(s *http.Server) { s.Close() }
