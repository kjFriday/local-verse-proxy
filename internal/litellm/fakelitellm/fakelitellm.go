// Package fakelitellm is a LiteLLM v1.106.6 stand-in for tests: the
// routes Front Desk, mint-key and the game use, answering with the
// bodies, status codes and error types the real proxy gave (recorded
// in testdata/captures.json). Only tests import it.
package fakelitellm

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options is what the fake proxy knows.
type Options struct {
	MasterKey string
	Models    []string // /v1/models ids
	Version   string   // litellm_version in readiness details

	// CostPerToken is the spend a chat call adds per token; 0 is
	// 0.000001 dollars.
	CostPerToken float64
	// Now is the clock for times; nil is time.Now.
	Now func() time.Time
}

// Key is one virtual key, as /key/list shows it.
type Key struct {
	Token, Key, Alias, Name string
	Models                  []string
	MaxBudget               *float64
	Spend                   float64
	BudgetDuration          string
	BudgetResetAt, Expires  string
	Created, LastActive     string
	Blocked                 bool
}

// Request is one request the fake got.
type Request struct {
	Method, Path string
	Header       http.Header
	Body         []byte
}

// Server is a running fake proxy.
type Server struct {
	URL string

	o    Options
	addr string
	srv  *http.Server

	mu    sync.Mutex
	ln    net.Listener
	keys  []*Key // newest last
	reqs  []Request
	fails map[string]failure
	delay map[string]time.Duration
	usage []call
}

type failure struct {
	status int
	body   string
}

// call is one answered chat, for /user/daily/activity.
type call struct {
	day, model, token, alias string
	prompt, completion       int
	spend                    float64
	ok                       bool
}

//go:embed shapes.json
var shapesJSON []byte

var shapes struct {
	Key        map[string]any `json:"key"`
	Generate   map[string]any `json:"generate"`
	Readiness  map[string]any `json:"readiness"`
	Metrics    map[string]any `json:"metrics"`
	Breakdowns []string       `json:"day_breakdown_keys"`
	Activity   map[string]any `json:"activity_metadata"`
}

func init() {
	if err := json.Unmarshal(shapesJSON, &shapes); err != nil {
		panic("fakelitellm: shapes.json: " + err.Error())
	}
}

// Start runs a fake proxy on a free loopback port.
func Start(o Options) *Server {
	if o.Version == "" {
		o.Version = "1.106.6"
	}
	if o.CostPerToken == 0 {
		o.CostPerToken = 0.000001
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		panic("fakelitellm: " + err.Error())
	}
	s := &Server{o: o, ln: ln, addr: ln.Addr().String(),
		fails: map[string]failure{}, delay: map[string]time.Duration{}}
	s.URL = "http://" + s.addr
	s.srv = &http.Server{Handler: http.HandlerFunc(s.serve)}
	go s.srv.Serve(ln)
	return s
}

// Close stops the fake.
func (s *Server) Close() {
	s.srv.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		s.ln.Close()
	}
}

// Down(true) refuses every connection, as a stopped proxy does;
// Down(false) listens again on the same address.
func (s *Server) Down(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		if s.ln != nil {
			s.ln.Close()
			s.ln = nil
		}
		s.srv.SetKeepAlivesEnabled(false)
		s.srv.SetKeepAlivesEnabled(true)
		return
	}
	if s.ln != nil {
		return
	}
	var err error
	for i := 0; i < 50; i++ {
		if s.ln, err = net.Listen("tcp4", s.addr); err == nil {
			go s.srv.Serve(s.ln)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	panic("fakelitellm: cannot listen again: " + err.Error())
}

// Fail makes every request to path (without its query) get status
// and body; status 0 ends that.
func (s *Server) Fail(path string, status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == 0 {
		delete(s.fails, path)
		return
	}
	s.fails[path] = failure{status, body}
}

// Delay makes every request to path wait d before its answer.
func (s *Server) Delay(path string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay[path] = d
}

// Requests are the requests so far, oldest first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

// AddKey adds a key as it is; empty fields get what /key/generate
// would give. It returns the token.
func (s *Server) AddKey(k Key) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k.Key == "" {
		k.Key = newKey()
	}
	if k.Token == "" {
		k.Token = hash(k.Key)
	}
	if k.Name == "" {
		k.Name = "sk-..." + k.Key[max(0, len(k.Key)-4):]
	}
	if k.Created == "" {
		k.Created = s.stamp(s.o.Now())
	}
	if k.Models == nil {
		k.Models = []string{}
	}
	s.keys = append(s.keys, &k)
	return k.Token
}

// Keys are the keys there now, oldest first.
func (s *Server) Keys() []Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Key, len(s.keys))
	for i, k := range s.keys {
		out[i] = *k
		out[i].Models = append([]string(nil), k.Models...)
	}
	return out
}

func newKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "sk-" + base64.RawURLEncoding.EncodeToString(b)
}

func hash(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// stamp is how LiteLLM's key list prints a time: milliseconds, Z.
func (s *Server) stamp(t time.Time) string {
	return t.UTC().Truncate(time.Millisecond).
		Format("2006-01-02T15:04:05.000000Z")
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, Request{Method: r.Method,
		Path: r.URL.RequestURI(), Header: r.Header.Clone(),
		Body: body})
	f, failing := s.fails[r.URL.Path]
	d := s.delay[r.URL.Path]
	s.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return
		}
	}
	if failing {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		io.WriteString(w, f.body)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refill()
	switch {
	case r.URL.Path == "/health/liveliness" && r.Method == "GET":
		writeJSON(w, 200, "I'm alive!")
	case r.URL.Path == "/health/readiness/details" && r.Method == "GET":
		if s.admin(w, r) {
			out := copyMap(shapes.Readiness)
			out["litellm_version"] = s.o.Version
			writeJSON(w, 200, out)
		}
	case (r.URL.Path == "/v1/models" || r.URL.Path == "/models") &&
		r.Method == "GET":
		s.models(w, r)
	case r.URL.Path == "/key/list" && r.Method == "GET":
		if s.admin(w, r) {
			s.list(w, r.URL.Query())
		}
	case r.URL.Path == "/key/generate" && r.Method == "POST":
		if s.admin(w, r) {
			s.generate(w, body)
		}
	case r.URL.Path == "/key/delete" && r.Method == "POST":
		if s.admin(w, r) {
			s.delete(w, body)
		}
	case (r.URL.Path == "/v1/chat/completions" ||
		r.URL.Path == "/chat/completions") && r.Method == "POST":
		s.chat(w, r, body)
	case r.URL.Path == "/user/daily/activity" && r.Method == "GET":
		if s.admin(w, r) {
			s.activity(w, r.URL.Query())
		}
	default:
		writeJSON(w, 404, map[string]any{"detail": "Not Found"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	w.Write(b)
}

// liteErr is LiteLLM's error body.
func liteErr(w http.ResponseWriter, status int, msg, typ string,
	param any) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"message": msg, "type": typ, "param": param,
		"code": strconv.Itoa(status)}})
}

func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	return strings.TrimPrefix(h, "Bearer "), true
}

func tokenNotFound(w http.ResponseWriter, key string) {
	liteErr(w, 401, "Authentication Error, Invalid proxy server "+
		"token passed. Received API Key = sk-..."+
		key[max(0, len(key)-4):]+", Key Hash (Token) ="+hash(key)+
		". Unable to find token in cache or "+
		"`LiteLLM_VerificationTokenTable`", "token_not_found_in_db",
		"key")
}

// admin answers 401 (or 403) unless the master key came; true if it
// did.
func (s *Server) admin(w http.ResponseWriter, r *http.Request) bool {
	key, ok := bearer(r)
	switch {
	case !ok:
		liteErr(w, 401, "Authentication Error, No api key passed in.",
			"auth_error", "None")
		return false
	case key == s.o.MasterKey:
		return true
	}
	k := s.find(key)
	switch {
	case k == nil:
		tokenNotFound(w, key)
	case r.Method == "POST":
		liteErr(w, 401, "Authentication Error, Only proxy admin can "+
			"be used to generate, delete, update info for new "+
			"keys/users/teams. Route="+r.URL.Path+". Your "+
			"role=unknown. Your user_id=*******", "auth_error",
			"None")
	default:
		liteErr(w, 403, "You are not authorized to access this "+
			"endpoint. No 'user_id' is associated with your API "+
			"key.",
			"bad_request_error", "user_id")
	}
	return false
}

// find is the key given as itself or by its hash.
func (s *Server) find(key string) *Key {
	for _, k := range s.keys {
		if k.Key == key || k.Token == key {
			return k
		}
	}
	return nil
}

// seat checks a seat key as LiteLLM does for a model call: known,
// not blocked, not expired, under budget. nil after an answer.
func (s *Server) seat(w http.ResponseWriter, r *http.Request) *Key {
	key, ok := bearer(r)
	if !ok {
		liteErr(w, 401, "Authentication Error, No api key passed in.",
			"auth_error", "None")
		return nil
	}
	if key == s.o.MasterKey {
		return &Key{Models: append([]string(nil), s.o.Models...)}
	}
	k := s.find(key)
	if k == nil {
		tokenNotFound(w, key)
		return nil
	}
	if k.Blocked {
		liteErr(w, 401, "Authentication Error, Key is blocked. Update "+
			"via `/key/unblock` if you're an admin.", "auth_error",
			"None")
		return nil
	}
	now := s.o.Now()
	if exp, err := time.Parse(time.RFC3339Nano, k.Expires); err == nil &&
		!exp.After(now) {
		liteErr(w, 401, "Authentication Error - Expired Key. Key "+
			"Expiry time "+pyTime(exp)+" and current time "+
			pyTime(now), "expired_key", k.Name)
		return nil
	}
	if k.MaxBudget != nil && k.Spend >= *k.MaxBudget {
		liteErr(w, 422, fmt.Sprintf("Budget has been exceeded! Key=%s "+
			"(%s) Current cost: %s, Max budget: %s", k.Alias,
			k.Name,
			pyFloat(k.Spend), pyFloat(*k.MaxBudget)),
			"budget_exceeded",
			nil)
		return nil
	}
	return k
}

func pyTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000000") + "+00:00"
}

func pyFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") && !math.IsInf(f, 0) {
		s += ".0"
	}
	return s
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	k := s.seat(w, r)
	if k == nil {
		return
	}
	data := []any{}
	for _, m := range s.o.Models {
		if len(k.Models) > 0 && !contains(k.Models, m) {
			continue
		}
		data = append(data, map[string]any{"created": 1677610602,
			"id": m, "object": "model", "owned_by": "openai"})
	}
	writeJSON(w, 200, map[string]any{"data": data, "object": "list"})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// object is a key as /key/list?return_full_object=true gives it.
func (s *Server) object(k *Key) map[string]any {
	o := copyMap(shapes.Key)
	var budget any
	if k.MaxBudget != nil {
		budget = *k.MaxBudget
	}
	var blocked any
	if k.Blocked {
		blocked = true
	}
	models := append([]string{}, k.Models...)
	o["token"], o["key_name"], o["key_alias"] = k.Token, k.Name,
		orNil(k.Alias)
	o["models"], o["max_budget"], o["spend"] = models, budget, k.Spend
	o["total_spend"] = k.Spend
	o["budget_duration"] = orNil(k.BudgetDuration)
	o["budget_reset_at"] = orNil(k.BudgetResetAt)
	o["expires"], o["created_at"] = orNil(k.Expires), orNil(k.Created)
	o["updated_at"] = orNil(k.Created)
	o["last_active"] = orNil(k.LastActive)
	o["blocked"] = blocked
	md := map[string]any{}
	if k.Alias != "" {
		md["seat"] = k.Alias
	}
	o["metadata"] = md
	return o
}

func (s *Server) list(w http.ResponseWriter, q url.Values) {
	full := q.Get("return_full_object") == "true"
	size := atoiOr(q.Get("size"), 10)
	page := atoiOr(q.Get("page"), 1)
	alias, byAlias := q.Get("key_alias"), q.Has("key_alias")
	var picked []*Key
	for i := len(s.keys) - 1; i >= 0; i-- { // newest first
		k := s.keys[i]
		if byAlias && k.Alias != alias {
			continue
		}
		picked = append(picked, k)
	}
	pages := 0
	if len(picked) > 0 && size > 0 {
		pages = (len(picked) + size - 1) / size
	}
	keys := []any{}
	for i := (page - 1) * size; i >= 0 && i < len(picked) &&
		i < page*size; i++ {
		if full {
			keys = append(keys, s.object(picked[i]))
		} else {
			keys = append(keys, picked[i].Token)
		}
	}
	writeJSON(w, 200, map[string]any{"keys": keys,
		"total_count": len(picked), "current_page": page,
		"total_pages": pages})
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

var durRE = regexp.MustCompile(`^([0-9]+)(s|m|h|d|w|mo)$`)

// dur is LiteLLM's duration: s, m (minutes), h, d, w, mo (30 days).
func dur(s string) (time.Duration, bool) {
	m := durRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"s": time.Second,
		"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour,
		"w": 7 * 24 * time.Hour, "mo": 30 * 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit, true
}

// nextReset is when a budget that refills every d next refills:
// LiteLLM lines it up with the clock (2m at :00, :02, ...; 1d at
// midnight UTC).
func nextReset(now time.Time, d time.Duration) time.Time {
	if d <= 0 {
		return now
	}
	return now.UTC().Truncate(d).Add(d)
}

func (s *Server) generate(w http.ResponseWriter, body []byte) {
	var in struct {
		KeyAlias       string         `json:"key_alias"`
		Models         []string       `json:"models"`
		MaxBudget      *float64       `json:"max_budget"`
		Duration       string         `json:"duration"`
		Metadata       map[string]any `json:"metadata"`
		BudgetDuration string         `json:"budget_duration"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, 422, map[string]any{"detail": []any{map[string]any{
			"type": "json_invalid", "loc": []any{"body"},
			"msg": "JSON decode error"}}})
		return
	}
	for _, k := range s.keys {
		if in.KeyAlias != "" && k.Alias == in.KeyAlias {
			msg := "Key with alias '" + in.KeyAlias +
				"' already exists. Unique key aliases " +
				"across all keys are required."
			liteErr(w, 400, msg, "bad_request_error",
				"key_alias")
			return
		}
	}
	now := s.o.Now()
	k := &Key{Key: newKey(), Alias: in.KeyAlias,
		Models:         append([]string{}, in.Models...),
		MaxBudget:      in.MaxBudget,
		BudgetDuration: in.BudgetDuration, Created: s.stamp(now)}
	k.Token = hash(k.Key)
	k.Name = "sk-..." + k.Key[len(k.Key)-4:]
	if d, ok := dur(in.Duration); ok {
		k.Expires = s.stamp(now.Add(d))
	}
	if d, ok := dur(in.BudgetDuration); ok {
		k.BudgetResetAt = nextReset(now, d).Format(time.RFC3339)
	}
	s.keys = append(s.keys, k)
	out := copyMap(shapes.Generate)
	out["key"], out["token"], out["token_id"] = k.Key, k.Token, k.Token
	out["key_alias"], out["key_name"] = orNil(k.Alias), k.Name
	out["models"], out["max_budget"] = k.Models, nil
	if k.MaxBudget != nil {
		out["max_budget"] = *k.MaxBudget
	}
	out["budget_duration"] = orNil(k.BudgetDuration)
	out["duration"] = orNil(in.Duration)
	out["expires"] = orNil(k.Expires)
	out["created_at"], out["updated_at"] = k.Created, k.Created
	md := in.Metadata
	if md == nil {
		md = map[string]any{}
	}
	out["metadata"] = md
	writeJSON(w, 200, out)
}

func (s *Server) delete(w http.ResponseWriter, body []byte) {
	var in struct {
		Keys    []string `json:"keys"`
		Aliases []string `json:"key_aliases"`
	}
	json.Unmarshal(body, &in)
	var gone []string
	keep := s.keys[:0]
	for _, k := range s.keys {
		hit := ""
		for _, x := range in.Keys {
			if x == k.Key || x == k.Token {
				hit = x
			}
		}
		for _, a := range in.Aliases {
			if a == k.Alias {
				hit = a
			}
		}
		if hit == "" {
			keep = append(keep, k)
			continue
		}
		gone = append(gone, hit)
	}
	s.keys = keep
	if len(gone) == 0 {
		liteErr(w, 404, "{'error': 'No keys found'}",
			"internal_server_error", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"deleted_keys": gone})
}

// refill is LiteLLM's reset job: a key whose refill time has come
// starts again at no spend.
func (s *Server) refill() {
	now := s.o.Now()
	for _, k := range s.keys {
		at, err := time.Parse(time.RFC3339, k.BudgetResetAt)
		if err != nil || at.After(now) {
			continue
		}
		k.Spend = 0
		if d, ok := dur(k.BudgetDuration); ok {
			k.BudgetResetAt = nextReset(now, d).Format(time.RFC3339)
		}
	}
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request,
	body []byte) {
	k := s.seat(w, r)
	if k == nil {
		return
	}
	var in struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Stream    bool   `json:"stream"`
		Messages  []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		liteErr(w, 400, "Invalid JSON in request body",
			"bad_request_error", nil)
		return
	}
	if len(k.Models) > 0 && !contains(k.Models, in.Model) ||
		!contains(s.o.Models, in.Model) {
		liteErr(w, 403, "The requested model '"+in.Model+"' is not "+
			"available for this API key, or the model name is "+
			"invalid. Check the models available to you and try "+
			"again.", "key_model_access_denied", "model")
		return
	}
	text := ""
	for _, m := range in.Messages {
		if s, ok := m.Content.(string); ok {
			text += s
		}
	}
	prompt := max(1, len([]rune(text))/4)
	completion := 9
	if in.MaxTokens > 0 && in.MaxTokens < completion {
		completion = in.MaxTokens
	}
	spend := float64(prompt+completion) * s.o.CostPerToken
	now := s.o.Now()
	if k.Token != "" {
		k.Spend += spend
		k.LastActive = s.stamp(now)
	}
	s.usage = append(s.usage, call{day: now.UTC().Format("2006-01-02"),
		model: in.Model, token: k.Token, alias: k.Alias, prompt: prompt,
		completion: completion, spend: spend, ok: true})
	reply := "Hello from the fake proxy."
	id := "chatcmpl-" + hash(fmt.Sprint(now.UnixNano()))[:8]
	usage := map[string]any{"completion_tokens": completion,
		"prompt_tokens": prompt, "total_tokens": prompt + completion}
	if !in.Stream {
		writeJSON(w, 200, map[string]any{"id": id,
			"object": "chat.completion", "created": now.Unix(),
			"model": in.Model, "choices": []any{map[string]any{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant",
					"content": reply}}}, "usage": usage})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.WriteHeader(200)
	chunk := func(v map[string]any) {
		v["id"], v["object"], v["created"], v["model"] = id,
			"chat.completion.chunk", now.Unix(), in.Model
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	chunk(map[string]any{"choices": []any{map[string]any{"index": 0,
		"delta": map[string]any{"role": "assistant",
			"content": reply}}}})
	chunk(map[string]any{"choices": []any{map[string]any{"index": 0,
		"delta": map[string]any{}, "finish_reason": "stop"}}})
	chunk(map[string]any{"choices": []any{map[string]any{"index": 0,
		"delta": map[string]any{}}}, "usage": usage})
	io.WriteString(w, "data: [DONE]\n\n")
}

func metrics(c []call) map[string]any {
	m := copyMap(shapes.Metrics)
	var reqs, okN, prompt, completion int
	spend := 0.0
	for _, x := range c {
		reqs++
		if x.ok {
			okN++
		}
		prompt += x.prompt
		completion += x.completion
		spend += x.spend
	}
	m["api_requests"], m["successful_requests"] = reqs, okN
	m["failed_requests"] = reqs - okN
	m["prompt_tokens"], m["completion_tokens"] = prompt, completion
	m["total_tokens"], m["spend"] = prompt+completion, spend
	return m
}

func (s *Server) activity(w http.ResponseWriter, q url.Values) {
	from, to := q.Get("start_date"), q.Get("end_date")
	byDay := map[string][]call{}
	var all []call
	for _, c := range s.usage {
		if (from == "" || c.day >= from) && (to == "" || c.day <= to) {
			byDay[c.day] = append(byDay[c.day], c)
			all = append(all, c)
		}
	}
	var days []string
	for d := range byDay {
		days = append(days, d)
	}
	sort.Strings(days)
	results := []any{}
	for _, d := range days {
		groups := map[string][]call{}
		for _, c := range byDay[d] {
			groups[c.model] = append(groups[c.model], c)
		}
		mg := map[string]any{}
		for model, cs := range groups {
			perKey := map[string][]call{}
			for _, c := range cs {
				perKey[c.token] = append(perKey[c.token], c)
			}
			kb := map[string]any{}
			for tok, kc := range perKey {
				md := map[string]any{
					"key_alias":  orNil(kc[0].alias),
					"key_exists": s.find(tok) != nil,
					"team_id":    nil,
					"user_email": nil, "user_id": nil}
				kb[tok] = map[string]any{"metadata": md,
					"metrics": metrics(kc)}
			}
			mg[model] = map[string]any{"api_key_breakdown": kb,
				"metadata": map[string]any{},
				"metrics":  metrics(cs)}
		}
		bd := map[string]any{}
		for _, name := range shapes.Breakdowns {
			bd[name] = map[string]any{}
		}
		bd["model_groups"] = mg
		results = append(results, map[string]any{"date": d,
			"metrics": metrics(byDay[d]), "breakdown": bd})
	}
	md := copyMap(shapes.Activity)
	t := metrics(all)
	for _, f := range []string{"api_requests", "successful_requests",
		"failed_requests", "prompt_tokens", "completion_tokens",
		"spend"} {
		md["total_"+f] = t[f]
	}
	md["total_tokens"] = t["total_tokens"]
	md["page"], md["total_pages"], md["has_more"] = 1, 1, false
	writeJSON(w, 200, map[string]any{"results": results, "metadata": md})
}
