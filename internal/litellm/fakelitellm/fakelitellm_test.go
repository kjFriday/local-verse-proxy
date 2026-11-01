package fakelitellm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const master = "sk-fake-master-1234"

// captures is LiteLLM v1.106.6's own answers, recorded on a real
// stack (secrets masked): testdata/captures.json.
type capture struct {
	Status      int             `json:"status"`
	ContentType string          `json:"content_type"`
	JSON        json.RawMessage `json:"json"`
	Text        *string         `json:"text"`
}

func captures(t *testing.T) map[string]capture {
	b, err := os.ReadFile("testdata/captures.json")
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]capture
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

type answer struct {
	status int
	ctype  string
	body   []byte
}

func do(t *testing.T, s *Server, method, path, key string,
	body any) answer {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.URL+path, rd)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return answer{resp.StatusCode, resp.Header.Get("Content-Type"), b}
}

func keys(raw []byte) []string {
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func field(raw []byte, path ...string) json.RawMessage {
	v := json.RawMessage(raw)
	for _, p := range path {
		var m map[string]json.RawMessage
		if json.Unmarshal(v, &m) != nil {
			var l []json.RawMessage
			json.Unmarshal(v, &l)
			if len(l) == 0 {
				return nil
			}
			v = l[0]
			json.Unmarshal(v, &m)
		}
		v = m[p]
	}
	return v
}

func str(raw json.RawMessage) string {
	var s string
	json.Unmarshal(raw, &s)
	return s
}

// like checks an answer against a capture: status, the error type,
// and the same field names.
func like(t *testing.T, name string, got answer, want capture) {
	t.Helper()
	if got.status != want.Status {
		t.Errorf("%s: status %d, LiteLLM %d (%s)", name, got.status,
			want.Status, got.body)
	}
	if !strings.HasPrefix(got.ctype, strings.Split(want.ContentType,
		";")[0]) {
		t.Errorf("%s: type %q, LiteLLM %q", name, got.ctype,
			want.ContentType)
	}
	if want.JSON == nil || string(want.JSON) == "null" {
		return
	}
	if e := field(want.JSON, "error", "type"); e != nil {
		if g := field(got.body, "error",
			"type"); string(g) != string(e) {
			t.Errorf("%s: error.type %s, LiteLLM %s", name, g, e)
		}
		return
	}
	if g, w := keys(got.body), keys(want.JSON); !reflect.DeepEqual(g,
		w) && len(w) > 0 {
		t.Errorf("%s: fields\n %q\nLiteLLM\n %q", name, g, w)
	}
}

func TestRoutesLikeLiteLLM(t *testing.T) {
	c := captures(t)
	s := Start(Options{MasterKey: master, Models: []string{"local",
		"claude"}})
	defer s.Close()
	like(t, "liveliness", do(t, s, "GET", "/health/liveliness", "",
		nil), c["liveliness"])
	like(t, "readiness_details_nokey", do(t, s, "GET",
		"/health/readiness/details", "", nil),
		c["readiness_details_nokey"])
	ready := do(t, s, "GET", "/health/readiness/details", master, nil)
	like(t, "readiness_details", ready, c["readiness_details"])
	if str(field(ready.body, "litellm_version")) != "1.106.6" {
		t.Errorf("version: %s", ready.body)
	}
	models := do(t, s, "GET", "/v1/models", master, nil)
	like(t, "models", models, c["models"])
	gen := map[string]any{"key_alias": "cap-a", "models": []string{"local"},
		"max_budget": 0.5, "duration": "1h",
		"metadata":        map[string]any{"seat": "cap-a"},
		"budget_duration": "1d"}
	g := do(t, s, "POST", "/key/generate", master, gen)
	like(t, "generate", g, c["generate"])
	keyA := str(field(g.body, "key"))
	taken := do(t, s, "POST", "/key/generate", master, gen)
	like(t, "generate_taken", taken, c["generate_taken"])
	if str(field(taken.body, "error", "message")) != str(field(
		c["generate_taken"].JSON, "error", "message")) {
		t.Errorf("taken: %s", taken.body)
	}
	zero := map[string]any{"key_alias": "cap-b",
		"models":     []string{"local"},
		"max_budget": 0, "duration": "1h"}
	keyB := str(field(do(t, s, "POST", "/key/generate", master,
		zero).body, "key"))
	like(t, "models_seat", do(t, s, "GET", "/v1/models", keyA, nil),
		c["models_seat"])
	chat := map[string]any{"model": "local", "max_tokens": 20,
		"messages": []any{map[string]any{"role": "user",
			"content": "hello there"}}}
	like(t, "chat", do(t, s, "POST", "/v1/chat/completions", keyA,
		chat), c["chat"])
	st := do(t, s, "POST", "/v1/chat/completions", keyA,
		map[string]any{"model": "local", "stream": true,
			"messages": chat["messages"]})
	like(t, "chat_stream", st, c["chat_stream"])
	if !strings.HasSuffix(string(st.body), "data: [DONE]\n\n") ||
		strings.Count(string(st.body), "data: ") != 4 {
		t.Errorf("stream: %s", st.body)
	}
	refused := map[string]any{"model": "pro", "messages": chat["messages"]}
	like(t, "chat_model_refused", do(t, s, "POST",
		"/v1/chat/completions", keyA, refused), c["chat_model_refused"])
	over := do(t, s, "POST", "/v1/chat/completions", keyB, chat)
	like(t, "chat_over_budget", over, c["chat_over_budget"])
	if !strings.HasPrefix(str(field(over.body, "error", "message")),
		"Budget has been exceeded! Key=cap-b (sk-...") {
		t.Errorf("over budget: %s", over.body)
	}
	like(t, "models_over_budget", do(t, s, "GET", "/v1/models", keyB,
		nil), c["models_over_budget"])
	like(t, "chat_bad_key", do(t, s, "POST", "/v1/chat/completions",
		"sk-not-a-key", chat), c["chat_bad_key"])
	page1 := do(t, s, "GET",
		"/key/list?return_full_object=true&size=100&page=1", master,
		nil)
	like(t, "key_list_page1", page1, c["key_list_page1"])
	kobj := field(page1.body, "keys")
	if g, w := keys(kobj), keys(field(c["key_list_page1"].JSON,
		"keys")); !reflect.DeepEqual(g, w) {
		t.Errorf("key object fields:\n %q\nLiteLLM\n %q", g, w)
	}
	var page struct {
		Keys        []map[string]any `json:"keys"`
		TotalCount  int              `json:"total_count"`
		TotalPages  int              `json:"total_pages"`
		CurrentPage int              `json:"current_page"`
	}
	json.Unmarshal(page1.body, &page)
	if len(page.Keys) != 2 || page.Keys[0]["key_alias"] != "cap-b" {
		t.Errorf("newest first: %v", page.Keys)
	}
	p2 := do(t, s, "GET", "/key/list?return_full_object=true&size=1"+
		"&page=2", master, nil)
	json.Unmarshal(p2.body, &page)
	if page.TotalPages != 2 || page.CurrentPage != 2 ||
		page.Keys[0]["key_alias"] != "cap-a" {
		t.Errorf("page 2: %s", p2.body)
	}
	like(t, "key_list_alias", do(t, s, "GET",
		"/key/list?return_full_object=true&key_alias=cap-a", master,
		nil),
		c["key_list_alias"])
	none := do(t, s, "GET", "/key/list?return_full_object=true"+
		"&key_alias=cap-z", master, nil)
	if string(none.body) != `{"current_page":1,"keys":[],`+
		`"total_count":0,"total_pages":0}` {
		t.Errorf("none: %s", none.body)
	}
	like(t, "key_list_plain", do(t, s, "GET", "/key/list", master, nil),
		c["key_list_plain"])
	like(t, "daily_activity", do(t, s, "GET", "/user/daily/activity?"+
		"start_date=2000-01-01&end_date=2100-01-01&page_size=1000",
		master, nil), c["daily_activity"])
	day := do(t, s, "GET", "/user/daily/activity", master, nil)
	r0 := field(day.body, "results")
	if g, w := keys(field(r0, "breakdown")), keys(field(
		c["daily_activity"].JSON, "results",
		"breakdown")); !reflect.DeepEqual(g, w) {
		t.Errorf("breakdown %q, LiteLLM %q", g, w)
	}
	mg := field(r0, "breakdown", "model_groups", "local")
	if g, w := keys(mg), keys(field(c["daily_activity"].JSON, "results",
		"breakdown", "model_groups", "local")); !reflect.DeepEqual(g,
		w) {
		t.Errorf("model group %q, LiteLLM %q", g, w)
	}
	if g, w := keys(field(mg, "metrics")), keys(field(
		c["daily_activity"].JSON, "results",
		"metrics")); !reflect.DeepEqual(g, w) {
		t.Errorf("metrics %q, LiteLLM %q", g, w)
	}
	like(t, "key_list_wrong_master", do(t, s, "GET", "/key/list",
		"sk-wrong-master", nil), c["key_list_wrong_master"])
	like(t, "generate_wrong_master", do(t, s, "POST", "/key/generate",
		"sk-wrong-master", gen), c["generate_wrong_master"])
	like(t, "key_list_seat_key", do(t, s, "GET", "/key/list", keyA,
		nil), c["key_list_seat_key"])
	like(t, "generate_seat_key", do(t, s, "POST", "/key/generate", keyA,
		gen), c["generate_seat_key"])
	for _, n := range []string{"delete_alias_none", "delete_keys_none"} {
		body := map[string]any{"key_aliases": []string{"cap-z"}}
		if n == "delete_keys_none" {
			body = map[string]any{"keys": []string{"sk-gone-key"}}
		}
		got := do(t, s, "POST", "/key/delete", master, body)
		like(t, n, got, c[n])
		var a, b any
		json.Unmarshal(got.body, &a)
		json.Unmarshal(c[n].JSON, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: %s, LiteLLM %s", n, got.body, c[n].JSON)
		}
	}
	del := do(t, s, "POST", "/key/delete", master,
		map[string]any{"key_aliases": []string{"cap-b"}})
	like(t, "delete_alias", del, c["delete_alias"])
	if string(del.body) != `{"deleted_keys":["cap-b"]}` {
		t.Errorf("delete alias: %s", del.body)
	}
	like(t, "delete_key", do(t, s, "POST", "/key/delete", master,
		map[string]any{"keys": []string{keyA}}), c["delete_key"])
	like(t, "unknown_path", do(t, s, "GET", "/no/such/path", "", nil),
		c["unknown_path"])
	if len(s.Keys()) != 0 {
		t.Errorf("keys left: %v", s.Keys())
	}
}

func TestAddKeyAndStates(t *testing.T) {
	c := captures(t)
	now := time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := Start(Options{MasterKey: master, Models: []string{"local"},
		Now: clock, CostPerToken: 0.01})
	defer s.Close()
	one := 1.0
	tok := s.AddKey(Key{Key: "sk-blocked-key-1", Alias: "b",
		Models: []string{"local"}, Blocked: true})
	if len(tok) != 64 {
		t.Fatalf("token %q", tok)
	}
	s.AddKey(Key{Key: "sk-expired-key-1", Alias: "e",
		Expires: "2026-10-31T20:00:00.000000Z"})
	s.AddKey(Key{Key: "sk-refill-key-1", Alias: "r", MaxBudget: &one,
		Spend: 5, BudgetDuration: "2m",
		BudgetResetAt: "2026-10-31T21:02:00Z"})
	chat := map[string]any{"model": "local", "messages": []any{
		map[string]any{"role": "user", "content": "hi"}}}
	like(t, "chat_blocked", do(t, s, "POST", "/v1/chat/completions",
		"sk-blocked-key-1", chat), c["chat_blocked"])
	like(t, "chat_expired", do(t, s, "POST", "/v1/chat/completions",
		"sk-expired-key-1", chat), c["chat_expired"])
	if got := do(t, s, "POST", "/v1/chat/completions",
		"sk-refill-key-1", chat); got.status != 422 {
		t.Fatalf("over budget before the refill: %d", got.status)
	}
	now = now.Add(2 * time.Minute) // the reset job's turn
	if got := do(t, s, "POST", "/v1/chat/completions",
		"sk-refill-key-1", chat); got.status != 200 {
		t.Fatalf("after the refill: %d %s", got.status, got.body)
	}
	for _, k := range s.Keys() {
		if k.Alias == "r" && (k.Spend <= 0 || k.BudgetResetAt !=
			"2026-10-31T21:04:00Z" || k.LastActive == "") {
			t.Errorf("refilled key: %+v", k)
		}
	}
}

func TestFailDelayDown(t *testing.T) {
	s := Start(Options{MasterKey: master})
	defer s.Close()
	s.Fail("/health/liveliness", 503, `{"x":1}`)
	if a := do(t, s, "GET", "/health/liveliness?x=1", "", nil); a.status !=
		503 || string(a.body) != `{"x":1}` {
		t.Fatalf("Fail: %+v", a)
	}
	s.Fail("/health/liveliness", 0, "")
	s.Delay("/health/liveliness", 150*time.Millisecond)
	start := time.Now()
	do(t, s, "GET", "/health/liveliness", "", nil)
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("Delay")
	}
	s.Delay("/health/liveliness", 0)
	s.Down(true)
	if _, err := http.Get(s.URL + "/health/liveliness"); err == nil {
		t.Fatal("down, yet it answered")
	}
	s.Down(false)
	if a := do(t, s, "GET", "/health/liveliness", "",
		nil); a.status != 200 {
		t.Fatalf("up again: %+v", a)
	}
	reqs := s.Requests()
	if len(reqs) != 3 || reqs[0].Path != "/health/liveliness?x=1" ||
		reqs[0].Method != "GET" {
		t.Fatalf("requests: %+v", reqs)
	}
}
