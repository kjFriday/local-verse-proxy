package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// readJSON is Handler.read_json: the body as a JSON object, or the
// message for the 400. A body too slow to come is a hang-up.
func readJSON(req *http.Request) (map[string]any, string) {
	ct, _, _ := strings.Cut(req.Header.Get("Content-Type"), ";")
	if ct != "application/json" {
		return nil, "Send JSON."
	}
	// net/http has checked Content-Length (ledger H-1); a chunked
	// body (-1) is not read, as Python read none.
	n := req.ContentLength
	if n < 0 {
		n = 0
	}
	if n > maxBody {
		return nil, "Too much data."
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, n))
	if err != nil {
		panic(errGone)
	}
	return parseJSON(raw)
}

// parseJSON reads a POST body: empty is {}; a UTF-8 byte order mark
// is dropped; not UTF-8, UTF-16 and UTF-32, NaN and Infinity are
// "Send valid JSON." (ledger H-6); numbers stay json.Number.
func parseJSON(raw []byte) (map[string]any, string) {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(raw) {
		return nil, "Send valid JSON."
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, "Send valid JSON."
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, "Send valid JSON."
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, "Send a JSON object."
	}
	return m, ""
}
