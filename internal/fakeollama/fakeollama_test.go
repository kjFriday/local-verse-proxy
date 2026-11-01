package fakeollama

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
)

type golden struct {
	Python     string
	Tokens     [][2]any
	PromptText [][2]string `json:"prompt_text"`
	Answers    []struct {
		Method, Path string
		Request      *string
		Status       int
		Type         *string
		Length       *string
		Body         string
		Crash        bool
	}
}

func readGolden(t *testing.T) golden {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden",
		"fakeollama.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

// pinned are the golden's dice: choice walks the list in turn, randint
// gives its top, uniform its bottom.
type pinned struct{ n int }

func (p *pinned) IntN(n int) int {
	x := p.n % n
	p.n++
	return x
}
func (p *pinned) Int64N(n int64) int64 { return n - 1 }
func (p *pinned) Float64() float64     { return 0 }

func pinnedServer() *server {
	return &server{rnd: &pinned{}, sleep: func(float64) {},
		now: func() time.Time { return time.Unix(1700000000, 0) }}
}

func TestTokensAndPromptGolden(t *testing.T) {
	g := readGolden(t)
	for _, c := range g.Tokens {
		text := c[0].(string)
		if got := tokens(text); float64(got) != c[1].(float64) {
			t.Errorf("tokens(%q) = %d, want %v", text, got, c[1])
		}
	}
	for _, c := range g.PromptText {
		v, err := loads([]byte(c[0]))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := promptText(v)
		if !ok || got != c[1] {
			t.Errorf("prompt_text(%s) = %q %v, want %q", c[0], got,
				ok,
				c[1])
		}
	}
}

// TestAnswersGolden asks the handler each request the golden asked
// Python's, over real sockets, and wants the same status, type,
// length and body, byte for byte. Where Python's handler crashed (no
// answer), Go answers 400 bad json (ledger P-2).
func TestAnswersGolden(t *testing.T) {
	g := readGolden(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: pinnedServer()}
	go srv.Serve(ln)
	defer srv.Close()
	base := "http://" + ln.Addr().String()
	for i, a := range g.Answers {
		var body io.Reader
		var raw []byte
		if a.Request != nil {
			raw, _ = base64.StdEncoding.DecodeString(*a.Request)
			body = bytes.NewReader(raw)
		}
		req, _ := http.NewRequest(a.Method, base+a.Path, body)
		if a.Request != nil {
			req.ContentLength = int64(len(raw))
			if len(raw) == 0 {
				req.Body = http.NoBody
				req.Header.Set("Content-Length", "0")
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%d %s %s: %v", i, a.Method, a.Path, err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		label := strconv.Itoa(i) + " " + a.Method + " " + a.Path
		if a.Crash {
			if resp.StatusCode != 400 || string(got) !=
				`{"error": "bad json"}` {
				t.Errorf("%s: %d %s, want 400 bad json", label,
					resp.StatusCode, got)
			}
			continue
		}
		want, _ := base64.StdEncoding.DecodeString(a.Body)
		if bytes.Contains(raw, []byte(`\ud800`)) {
			// A lone surrogate reads as U+FFFD (ledger P-3);
			// both escapes are six bytes, so the length holds.
			want = bytes.ReplaceAll(want, []byte(`\ud800`),
				[]byte(`\ufffd`))
		}
		if resp.StatusCode != a.Status {
			t.Errorf("%s: status %d, want %d", label,
				resp.StatusCode,
				a.Status)
		}
		if a.Type != nil && resp.Header.Get("Content-Type") != *a.Type {
			t.Errorf("%s: type %q, want %q", label,
				resp.Header.Get("Content-Type"), *a.Type)
		}
		if a.Length != nil && resp.Header.Get("Content-Length") !=
			*a.Length {
			t.Errorf("%s: length %q, want %q", label,
				resp.Header.Get("Content-Length"), *a.Length)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", label, got, want)
		}
	}
}

// TestShapes checks the real dice: the reply's size follows
// num_predict, eval_count counts it, a stream has 8 words a line.
func TestShapes(t *testing.T) {
	h := Handler().(*server)
	h.sleep = func(float64) {}
	for _, limit := range []int{1, 2, 7, 128, 4096} {
		for _, stream := range []bool{false, true} {
			body := `{"model": "office-pro", "stream": ` +
				strconv.FormatBool(stream) + `, "options": ` +
				`{"num_predict": ` + strconv.Itoa(limit) + `}}`
			w := &recorder{h: http.Header{}}
			req, _ := http.NewRequest("POST", "/api/chat",
				strings.NewReader(body))
			req.RequestURI = "/api/chat"
			req.Header.Set("Content-Length", strconv.Itoa(len(
				body)))
			h.ServeHTTP(w, req)
			lines := strings.Split(strings.TrimSuffix(
				w.body.String(),
				"\n"), "\n")
			var last map[string]any
			if err := json.Unmarshal([]byte(lines[len(lines)-1]),
				&last); err != nil {
				t.Fatal(err)
			}
			reply := ""
			if stream {
				for _, l := range lines[:len(lines)-1] {
					var m map[string]any
					json.Unmarshal([]byte(l), &m)
					msg := m["message"].(map[string]any)
					c := msg["content"].(string)
					if n := len(strings.Fields(c)); n > 8 {
						t.Errorf("%d words in a line",
							n)
					}
					reply += c
				}
				reply = strings.TrimSuffix(reply, " ")
			} else {
				msg := last["message"].(map[string]any)
				reply = msg["content"].(string)
			}
			n := int(last["eval_count"].(float64))
			if int64(n) != tokens(reply) {
				t.Errorf("eval_count %d for %q", n, reply)
			}
			lo := max(1, limit/2)
			if len(reply) < lo*4 || n > limit+3 {
				t.Errorf("limit %d: reply of %d tokens", limit,
					n)
			}
		}
	}
}

type recorder struct {
	h    http.Header
	code int
	body bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.h }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(code int)        { r.code = code }

func TestMainFlags(t *testing.T) {
	var out, errb bytes.Buffer
	std := args.Std{Out: &out, Err: &errb}
	if code := Main([]string{"--help"}, std); code != 0 ||
		!strings.Contains(out.String(), "practice") {
		t.Errorf("--help: %d %q", code, out.String())
	}
	out.Reset()
	if code := Main([]string{"extra"}, std); code != 2 ||
		!strings.Contains(errb.String(), "unrecognized arguments: "+
			"extra") {
		t.Errorf("extra: %d %q", code, errb.String())
	}
}

// TestServe runs the server on a socket, as Main does on its port.
func TestServe(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	done := make(chan int)
	go func() { done <- serve(ln, args.Std{Out: &out, Err: &out}) }()
	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ln.Close()
	<-done
	if string(b) != "Ollama is running" {
		t.Errorf("got %q", b)
	}
}
