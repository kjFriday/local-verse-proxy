package web

import (
	"crypto/subtle"
	"net/http"

	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
)

func addr(req *http.Request) string { return hostOf(req.RemoteAddr) }

// cookie is Front Desk's session cookie, read with Go's parser
// (ledger F-8).
func (a *App) cookie(req *http.Request) string {
	c, err := req.Cookie(a.cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func (a *App) wrongHost(w http.ResponseWriter, req *http.Request) {
	switch {
	case a.o.Docker:
		a.fail(w, req, 421, "Open Front Desk by this computer's IP "+
			"address, like http://192.168.1.20:"+
			itoa(a.o.PublicPort)+", or add the name you used "+
			"to FRONTDESK_HOSTS in .env (gui/README.md).", "")
	case a.o.LAN:
		a.fail(w, req, 421, "Open Front Desk by this computer's "+
			"address, like http://192.168.1.20:"+itoa(a.o.Port)+
			", or start it with --host NAME.", "")
	default:
		a.fail(w, req, 421, "Open Front Desk at http://127.0.0.1:"+
			itoa(a.o.Port), "")
	}
}

// ifNoneMatch is the If-None-Match header, and whether it was sent.
func ifNoneMatch(req *http.Request) (string, bool) {
	v, ok := req.Header["If-None-Match"]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[0], true
}

func (a *App) get(w http.ResponseWriter, req *http.Request, path string) {
	if !a.HostOK(req.Host) {
		a.wrongHost(w, req)
		return
	}
	if f, ok := a.static[path]; ok {
		etag := [2]string{"ETag", f.etag}
		if v, ok := ifNoneMatch(req); ok && v == f.etag {
			a.send(w, req, reply{code: 304, cache: "no-cache",
				headers: [][2]string{etag}})
			return
		}
		a.send(w, req, reply{code: 200, body: f.body, ctype: f.ctype,
			gz: f.gz, cache: "no-cache",
			headers: [][2]string{etag}})
		return
	}
	csrf, in := a.o.Gate.CSRFFor(a.cookie(req), addr(req))
	if path == "/api/session" {
		lan := a.o.LAN || a.o.Docker && OverNetwork(req.Host)
		var c, seat any
		if in {
			c, seat = csrf, a.SeatURL(req.Host)
		}
		a.send(w, req, reply{code: 200, body: pyjson.Marshal(pyjson.O(
			"lan", lan, "docker", a.o.Docker,
			"need_login", a.o.Gate.HasPassword(), "csrf", c,
			"seat_url", seat, "reset_cmd", a.o.ResetCmd))})
		return
	}
	if path != "/api/state" && path != "/api/events" {
		a.fail(w, req, 404, "Nothing here.", "")
		return
	}
	if !in {
		a.fail(w, req, 401, "Log in first.", "")
		return
	}
	if path == "/api/events" {
		a.events(w, req)
		return
	}
	d := a.o.Desk
	d.Fresh()
	etag, body, gz := d.Answer()
	tag := [2]string{"ETag", etag}
	if v, ok := ifNoneMatch(req); ok && v == etag {
		a.send(w, req, reply{code: 304, cache: "no-cache, private",
			headers: [][2]string{tag}})
		return
	}
	if gz == nil {
		gz = []byte{}
	}
	a.send(w, req, reply{code: 200, body: body, gz: gz,
		cache: "no-cache, private", headers: [][2]string{tag}})
}

func (a *App) post(w http.ResponseWriter, req *http.Request, path string) {
	if !a.HostOK(req.Host) {
		a.wrongHost(w, req)
		return
	}
	if o := req.Header.Get("Origin"); o != "" && o != "http://"+req.Host {
		a.fail(w, req, 403, "Requests from other sites are refused.",
			"")
		return
	}
	data, msg := readJSON(req)
	if msg != "" {
		a.fail(w, req, 400, msg, "")
		return
	}
	if path == "/api/login" {
		a.login(w, req, data)
		return
	}
	csrf, in := a.o.Gate.CSRFFor(a.cookie(req), addr(req))
	if !in {
		a.fail(w, req, 401, "Log in first.", "")
		return
	}
	given := req.Header.Get("X-CSRF-Token")
	if subtle.ConstantTimeCompare([]byte(given), []byte(csrf)) != 1 {
		a.fail(w, req, 403, "Reload the page and try again.", "")
		return
	}
	switch path {
	case "/api/logout":
		a.o.Gate.Logout(a.cookie(req))
		a.send(w, req, reply{code: 200,
			body: pyjson.Marshal(pyjson.O("ok", true)),
			headers: [][2]string{{"Set-Cookie", a.cookieName +
				"=; Max-Age=0; Path=/; HttpOnly; " +
				"SameSite=Strict"}}})
	case "/api/mint":
		a.mint(w, req, data)
	case "/api/revoke":
		a.revoke(w, req, data)
	default:
		a.fail(w, req, 404, "Nothing here.", "")
	}
}
