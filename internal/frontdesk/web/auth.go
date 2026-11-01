package web

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/gate"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
)

func (a *App) login(w http.ResponseWriter, req *http.Request,
	data map[string]any) {
	g := a.o.Gate
	if !g.HasPassword() {
		a.send(w, req, reply{code: 200, body: pyjson.Marshal(pyjson.O(
			"csrf", g.OpenCSRF(), "seat_url",
			a.SeatURL(req.Host)))})
		return
	}
	pw, ok := data["password"].(string)
	if !ok || utf8.RuneCountInString(pw) > gate.MaxPassword {
		a.fail(w, req, 400, "Type the password.", "")
		return
	}
	cookie, csrf, err := g.Login(addr(req), pw)
	var refused *gate.Refused
	var wrong *gate.WrongPassword
	switch {
	case errors.As(err, &refused):
		// Not logged: a locked device could flood the terminal. The
		// lock was logged once, as it began.
		a.fail(w, req, 429, refused.Msg, "",
			[2]string{"Retry-After", itoa(refused.Wait)})
		return
	case errors.As(err, &wrong):
		line := "wrong password from " + addr(req)
		if wrong.Locked {
			line += ": locked for 10 minutes"
		}
		a.o.Log.Line(line)
		a.fail(w, req, 401, wrong.Msg, "")
		return
	case err != nil:
		panic(err)
	}
	a.o.Log.Line("logged in: " + addr(req))
	line := a.cookieName + "=" + cookie + "; Max-Age=" +
		itoa(gate.SessionHours*3600) + "; Path=/; HttpOnly; " +
		"SameSite=Strict"
	a.send(w, req, reply{code: 200, body: pyjson.Marshal(pyjson.O(
		"csrf", csrf, "seat_url", a.SeatURL(req.Host))),
		headers: [][2]string{{"Set-Cookie", line}}})
}
