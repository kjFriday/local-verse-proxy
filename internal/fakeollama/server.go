package fakeollama

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// models and words are fake_ollama.py's MODELS and WORDS.
var (
	models = []string{"office-local", "office-pro"}
	words  = strings.Fields("draft report review ticket merge spec " +
		"deck budget memo plan test fix note sync chart email " +
		"summary agenda invoice")
)

// dice is what the server draws from: math/rand/v2, unseeded, as
// Python's module-level random was. Tests pin it.
type dice interface {
	IntN(n int) int       // random.choice of n things
	Int64N(n int64) int64 // randint(a, b) is a + Int64N(b - a + 1)
	Float64() float64     // uniform(a, b) is a + (b - a) * Float64()
}

// server is the handler and what it draws from; tests set them.
type server struct {
	mu    sync.Mutex
	rnd   dice
	sleep func(seconds float64)
	now   func() time.Time
}

// Handler is the practice server's routes.
func Handler() http.Handler {
	return &server{rnd: rand.New(rand.NewPCG(rand.Uint64(),
		rand.Uint64())), sleep: func(s float64) {
		time.Sleep(time.Duration(s * float64(time.Second)))
	}, now: time.Now}
}

// tokens is fake_ollama.py's tokens(): one token is about 4
// characters.
func tokens(text string) int64 {
	n := pyfmt.RoundInt(float64(utf8.RuneCountInString(text)) / 4)
	if n < 1 {
		return 1
	}
	return n
}

// promptText is fake_ollama.py's prompt_text(): the messages' text,
// with OpenAI-style content parts joined. ok false where Python's
// handler crashed on the shape.
func promptText(messages any) (string, bool) {
	if !truthy(messages) {
		return "", true
	}
	list, ok := messages.([]any)
	if !ok {
		return "", false // Python's loop crashed on it
	}
	var parts []string
	for _, m := range list {
		md, ok := m.(*object)
		if !ok {
			return "", false
		}
		c := md.get("content")
		if !truthy(c) {
			c = ""
		}
		if cl, isList := c.([]any); isList {
			var texts []string
			for _, p := range cl {
				pd, isObj := p.(*object)
				if !isObj {
					continue
				}
				t, has := pd.lookup("text")
				if !has {
					t = ""
				}
				ts, isStr := t.(string)
				if !isStr {
					return "", false
				}
				texts = append(texts, ts)
			}
			c = strings.Join(texts, " ")
		}
		parts = append(parts, str(c))
	}
	return strings.Join(parts, "\n"), true
}

// filler is fake_ollama.py's filler(): words until about n tokens.
func (s *server) filler(n int64) string {
	var out []string
	size := int64(0)
	for size < n*4 {
		w := words[s.rnd.IntN(len(words))]
		out = append(out, w)
		size += int64(len(w)) + 1
	}
	text := strings.Join(out, " ")
	return strings.ToUpper(text[:1]) + text[1:] + "."
}

// stamp is fake_ollama.py's now().
func (s *server) stamp() string {
	return s.now().UTC().Format("2006-01-02T15:04:05Z")
}

func (s *server) send(w http.ResponseWriter, code int, body []byte,
	ctype string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(code)
	w.Write(body)
}

func (s *server) sendJSON(w http.ResponseWriter, code int, v any) {
	s.send(w, code, []byte(dumps(v)), "application/json")
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// As http.server (Python 3.12 and later): a path that starts
	// with // is cut to one /. LiteLLM sends //api/chat when its
	// api_base ends in /.
	path := r.RequestURI
	if strings.HasPrefix(path, "//") {
		path = "/" + strings.TrimLeft(path, "/")
	}
	switch r.Method {
	case "GET":
		s.get(w, path)
	case "POST":
		s.post(w, r, path)
	default:
		s.unsupported(w, r.Method)
	}
}

func (s *server) get(w http.ResponseWriter, path string) {
	switch path {
	case "/":
		s.send(w, 200, []byte("Ollama is running"), "text/plain")
	case "/api/version":
		s.sendJSON(w, 200, obj("version", "0.0.0-practice"))
	case "/api/tags":
		var list []any
		for _, m := range models {
			list = append(list, obj("name", m+":latest",
				"model", m+":latest"))
		}
		s.sendJSON(w, 200, obj("models", list))
	default:
		s.sendJSON(w, 404, obj("error", "not found"))
	}
}

func (s *server) post(w http.ResponseWriter, r *http.Request,
	path string) {
	n := int64(0)
	if h := r.Header.Get("Content-Length"); h != "" {
		v, err := strconv.ParseInt(pyfmt.Strip(h), 10, 64)
		if err != nil || v < 0 {
			s.sendJSON(w, 400, obj("error", "bad json"))
			return
		}
		n = v
	}
	data, _ := io.ReadAll(io.LimitReader(r.Body, n))
	if len(data) == 0 {
		data = []byte("{}")
	}
	req, err := loads(data)
	if err != nil {
		s.sendJSON(w, 400, obj("error", "bad json"))
		return
	}
	switch path {
	case "/api/show":
		s.sendJSON(w, 200, obj("modelfile", "", "parameters", "",
			"template", "{{ .Prompt }}",
			"details", obj("family", "llama", "format", "gguf"),
			"model_info", obj("llama.context_length", int64(32768)),
			"capabilities", []any{"completion"}))
	case "/api/chat":
		s.chat(w, req)
	default:
		s.sendJSON(w, 404, obj("error", "not found"))
	}
}

// chat is fake_ollama.py's Handler.chat().
func (s *server) chat(w http.ResponseWriter, v any) {
	req, ok := v.(*object)
	if !ok {
		s.sendJSON(w, 400, obj("error", "bad json"))
		return
	}
	model, has := req.lookup("model")
	if !has {
		model = models[0]
	}
	text, ok := promptText(req.get("messages"))
	limit, ok2 := numPredict(req.get("options"))
	if !ok || !ok2 {
		s.sendJSON(w, 400, obj("error", "bad json"))
		return
	}
	pTok := tokens(text)
	if limit > 4096 {
		limit = 4096
	}
	if limit < 1 {
		limit = 1
	}
	s.mu.Lock()
	lo := max(1, pyFloorDiv(limit, 2))
	reply := s.filler(lo + s.rnd.Int64N(limit-lo+1))
	rTok := tokens(reply)
	// float64(...) rounds the product, so arm64 cannot fuse it with
	// the add.
	think := 0.05 + float64(0.2*s.rnd.Float64())
	think += float64(float64(rTok) * 0.0005)
	s.mu.Unlock()
	// Small random think time, a bit longer for long answers.
	s.sleep(think)
	last := obj("model", model, "created_at", s.stamp(), "done", true,
		"done_reason", "stop", "prompt_eval_count", pTok,
		"eval_count", rTok)
	if !truthy(req.get("stream")) {
		last.set("message", obj("role", "assistant", "content", reply))
		s.sendJSON(w, 200, last)
		return
	}
	// Streamed: one JSON object per line, then a final "done" line.
	var body bytes.Buffer
	ws := strings.Split(reply, " ")
	for i := 0; i < len(ws); i += 8 {
		end := min(i+8, len(ws))
		chunk := strings.Join(ws[i:end], " ") + " "
		body.WriteString(dumps(obj("model", model, "created_at",
			s.stamp(), "done", false, "message", obj("role",
				"assistant", "content", chunk))) + "\n")
	}
	last.set("message", obj("role", "assistant", "content", ""))
	body.WriteString(dumps(last) + "\n")
	s.send(w, 200, body.Bytes(), "application/x-ndjson")
}

func pyFloorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// numPredict is int((options or {}).get("num_predict") or 128).
func numPredict(options any) (int64, bool) {
	if !truthy(options) {
		return 128, true
	}
	o, ok := options.(*object)
	if !ok {
		return 0, false
	}
	v := o.get("num_predict")
	if !truthy(v) {
		return 128, true
	}
	switch x := v.(type) {
	case bool:
		return 1, true // True; False was falsy
	case json.Number:
		return intOf(string(x))
	case string:
		return intText(x)
	}
	return 0, false
}

// unsupported is BaseHTTPRequestHandler's answer to a method it has
// no do_ for.
func (s *server) unsupported(w http.ResponseWriter, method string) {
	msg := "Unsupported method (" + pyfmt.ReprString(method) + ")"
	body := "<!DOCTYPE HTML>\n<html lang=\"en\">\n    <head>\n" +
		"        <meta charset=\"utf-8\">\n" +
		"        <title>Error response</title>\n    </head>\n" +
		"    <body>\n        <h1>Error response</h1>\n" +
		"        <p>Error code: 501</p>\n" +
		"        <p>Message: " + htmlEscape(msg) + ".</p>\n" +
		"        <p>Error code explanation: 501 - Server does not " +
		"support this operation.</p>\n    </body>\n</html>\n"
	w.Header().Set("Connection", "close")
	s.send(w, 501, []byte(body), "text/html;charset=utf-8")
}

// htmlEscape is html.escape(s, quote=False).
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
