// Package live holds the real-stack checks of spec 16.5 that compare
// two things side by side: LiteLLM's own answers against the fake the
// unit tests use (fake_test.go, every CI run, from the answers
// recorded here), and both Front Desks on one real proxy
// (twin_test.go, tag e2e). capture_test.go (tag e2e) records the
// answers from a live stack into testdata/golden/captures.json.
package live

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Step is one request to LiteLLM and what came back.
type Step struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
	// Auth: "master", "seat" (the key the steps made), "wrong", "".
	Auth   string          `json:"auth"`
	Body   json.RawMessage `json:"body,omitempty"`
	Status int             `json:"status"`
	// ErrorType is error.type of an error answer, if any.
	ErrorType string `json:"error_type,omitempty"`
	// Fields are the answer's top-level field names, sorted (for a
	// list, the first item's, with "[]" in front).
	Fields []string `json:"fields"`
}

// Shape gives an answer's error type and field names.
func Shape(body []byte) (errType string, fields []string) {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return "", []string{"<not json>"}
	}
	prefix := ""
	if l, ok := v.([]any); ok {
		if len(l) == 0 {
			return "", []string{"[]"}
		}
		v, prefix = l[0], "[]"
	}
	m, ok := v.(map[string]any)
	if !ok {
		return "", []string{prefix + "<value>"}
	}
	for k := range m {
		fields = append(fields, prefix+k)
	}
	sort.Strings(fields)
	if e, ok := m["error"].(map[string]any); ok {
		errType, _ = e["type"].(string)
	}
	return errType, fields
}

var secretRE = regexp.MustCompile(`sk-[A-Za-z0-9_-]+|[0-9a-f]{32,}`)

// Mask hides keys and hashes in a request body before it is kept.
func Mask(b []byte) []byte {
	return secretRE.ReplaceAll(b, []byte("<masked>"))
}

// Play sends the steps to the proxy at base, in order, and fills in
// what came back. The seat key "generate" makes is used by the steps
// with Auth "seat".
func Play(base, master, seat string, steps []Step) []Step {
	out := make([]Step, len(steps))
	seatKey := ""
	c := &http.Client{Timeout: 60 * time.Second}
	for i, s := range steps {
		path := strings.ReplaceAll(s.Path, "{seat}", seat)
		body := bytes.ReplaceAll(s.Body, []byte("{seat}"), []byte(seat))
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, _ := http.NewRequest(s.Method, base+path, rd)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		switch s.Auth {
		case "master":
			req.Header.Set("Authorization", "Bearer "+master)
		case "seat":
			req.Header.Set("Authorization", "Bearer "+seatKey)
		case "wrong":
			req.Header.Set("Authorization", "Bearer sk-wrong-1")
		}
		s.Body = Mask(s.Body)
		resp, err := c.Do(req)
		if err != nil {
			s.Status = -1
			s.Fields = []string{"<no answer>"}
			out[i] = s
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		s.Status = resp.StatusCode
		s.ErrorType, s.Fields = Shape(b)
		if s.Name == "generate" {
			var j struct{ Key string }
			json.Unmarshal(b, &j)
			seatKey = j.Key
		}
		out[i] = s
	}
	return out
}
