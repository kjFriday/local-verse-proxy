package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/desk"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// events is Server-Sent Events: a short "changed" on every new
// state, a tick each PingEvery seconds. The page then fetches
// /api/state, gzipped once for every viewer, 304 when nothing is new.
// The stream is close-delimited (Transfer-Encoding: identity and
// Connection: close): no chunks, as Python sent it.
func (a *App) events(w http.ResponseWriter, req *http.Request) {
	d := a.o.Desk
	if !d.AddViewer(MaxStreams) {
		a.fail(w, req, 503, "Too many open pages.", "")
		return
	}
	defer d.DropViewer()
	d.Fresh()
	h := w.Header()
	h.Set("Server", "FrontDesk")
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "close")
	h.Set("Transfer-Encoding", "identity")
	for _, kv := range SecurityHeaders {
		h.Set(kv[0], kv[1])
	}
	rc := http.NewResponseController(w)
	write := func(b string) bool {
		rc.SetWriteDeadline(time.Now().Add(writeWait))
		if _, err := w.Write([]byte(b)); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	w.WriteHeader(200)
	if !write("retry: 3000\n\n") {
		return
	}
	clk := a.o.Clock
	sent, wrote := -1, clk.Mono()
	cookie, from := a.cookie(req), addr(req)
	ctx := req.Context()
	for {
		if _, ok := a.o.Gate.CSRFFor(cookie, from); !ok {
			return
		}
		// Wake each second to notice a closed page quickly, not
		// only at the next write.
		v, checked := d.Wait(ctx, sent)
		if ctx.Err() != nil {
			return
		}
		var msg string
		if v != sent {
			sent = v
			msg = fmt.Sprintf(
				"event: changed\ndata: {\"v\":%d}\n\n", v)
		} else if clk.Mono()-wrote >= desk.PingEvery {
			age := max(0.0, clk.Wall()-checked)
			msg = "event: tick\ndata: {\"age\":" +
				pyfmt.Format(age, ".1f") + "}\n\n"
		} else {
			continue
		}
		if !write(msg) {
			return
		}
		wrote = clk.Mono()
		d.Seen()
	}
}
