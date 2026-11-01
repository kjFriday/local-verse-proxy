package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/litellm"
	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// chatWait is how long check 1 waits: Ollama may be loading the
// model (README Step 6's -m 120).
var chatWait = 120 * time.Second

// Test is `lvp test`: README Step 6's three checks, on a throwaway
// seat that is always revoked.
func (s *System) Test(ctx context.Context, argv []string,
	std args.Std) int {
	cmd := s.Prog + " test"
	fl := args.New(cmd, cmd)
	pos, err := fl.Parse(argv)
	if err == nil && len(pos) > 0 {
		err = extra(cmd, cmd, pos)
	}
	if err != nil {
		if errors.Is(err, args.ErrHelp) {
			fmt.Fprint(std.Out, fl.Help()+testHelp)
			return 0
		}
		return args.Exit(std, fl, err)
	}
	file, err := s.seatsFile()
	if err != nil {
		return fail(std, err.Error())
	}
	tag, err := s.randHex(3)
	if err != nil {
		return fail(std, err.Error())
	}
	seat := "test-" + tag
	r := mintkey.Exec(ctx, file, []string{seat, "--models", "local",
		"--budget", "1"}, mintTimeout)
	if r.Code != 0 {
		fmt.Fprint(std.Err, fold(r.Stderr))
		return fail(std, "FAIL  could not mint a test key, so no "+
			"check ran. The reason is above (README.md, "+
			"Troubleshooting: minting).")
	}
	key := strings.TrimSpace(r.Stdout)
	say(std.Out, "Minted a test key for seat "+seat+": model local "+
		"only, 1 dollar. It is revoked at the end.")
	base := file.Get("PROXY_URL", defaultProxy)
	revoked := false
	defer func() {
		if revoked {
			return
		}
		// A fresh context: after Ctrl+C the key must still go.
		c, cancel := context.WithTimeout(context.Background(),
			30*time.Second)
		defer cancel()
		back := mintkey.Exec(c, file, []string{"--revoke-seat", seat},
			mintTimeout)
		if back.Code == 0 {
			say(std.Out, "Revoked the test key.")
			return
		}
		fmt.Fprint(std.Err, wrap("", "Could not revoke the test key. "+
			"Run "+s.Prog+" revoke "+seat+" (an error with 404 "+
			"means it is gone already)."))
	}()
	passed := 0
	ok, why := chatCheck(ctx, base, key, "local", true)
	passed += report(std, ok, "1. the key works", why)
	ok, why = chatCheck(ctx, base, key, "claude", false)
	passed += report(std, ok, "2. the wrong model is refused", why)
	ok, why = s.revokeCheck(ctx, file, base, key, &revoked)
	passed += report(std, ok, "3. a revoked key is refused", why)
	if ctx.Err() != nil {
		return fail(std, "Stopped.")
	}
	if passed == 3 {
		say(std.Out, "All three checks pass: your proxy works, and "+
			"the test key is gone.")
		return 0
	}
	fmt.Fprint(std.Err, wrap("", fmt.Sprintf("%d of 3 checks "+
		"failed. Fix the cause with README.md's Troubleshooting "+
		"sections, then run %s test again.", 3-passed, s.Prog)))
	return 1
}

const testHelp = `
README.md Step 6's three checks: mints a throwaway key (seat
test-<6 letters and digits>, model local only, 1 dollar), then 1. it
works with local, 2. it is refused for claude, 3. once revoked, it is
refused. The key is revoked at the end, whatever happens.
`

// report prints PASS or FAIL for one check; 1 if it passed.
func report(std args.Std, ok bool, what, why string) int {
	if ok {
		fmt.Fprint(std.Out, wrap("PASS  ", what+": "+why))
		return 1
	}
	fmt.Fprint(std.Out, wrap("FAIL  ", what+": "+why))
	return 0
}

// chatCheck asks model for "hi" with key. want: an answer (check 1),
// or a refusal at the door (check 2).
func chatCheck(ctx context.Context, base, key, model string,
	want bool) (bool, string) {
	wait := chatWait
	if !want {
		wait = 30 * time.Second
	}
	c, err := litellm.NewWith(litellm.Options{BaseURL: base,
		MasterKey: key, UserAgent: "local-verse-proxy",
		Timeout: wait, EnvProxy: true})
	if err != nil {
		return false, "PROXY_URL in seats.env " + err.Error()
	}
	in := map[string]any{"model": model, "messages": []any{
		map[string]any{"role": "user", "content": "hi"}}}
	var out struct {
		Choices []json.RawMessage `json:"choices"`
	}
	err = c.Post(ctx, "/v1/chat/completions", in, &out)
	var se *litellm.StatusError
	var ce *litellm.ConnError
	switch {
	case want && err == nil && len(out.Choices) > 0:
		return true, model + " answered."
	case want && err == nil:
		return false, "the answer has no \"choices\" in it."
	case want && errors.As(err, &ce) && ce.Late:
		return false, "no answer within " + wait.String() + ": the " +
			"proxy may not reach Ollama (README.md, " +
			"Troubleshooting: the proxy)."
	case want && errors.As(err, &se):
		return false, se.Status() + ". " + se.Message
	case want:
		return false, err.Error()
	case err == nil:
		return false, model + " answered: the key was not stopped " +
			"at the door."
	case errors.As(err, &se) && (se.Code == 403 || se.Code == 401):
		return true, fmt.Sprintf("%s got %d, not available for this "+
			"key.", model, se.Code)
	case errors.As(err, &se):
		return false, se.Status() + ", not 403. " + se.Message
	}
	return false, err.Error()
}

// revokeCheck revokes key, then knocks with it: 401 passes.
func (s *System) revokeCheck(ctx context.Context, file *seats.File,
	base, key string, revoked *bool) (bool, string) {
	r := mintkey.Exec(ctx, file, []string{"--revoke", key}, mintTimeout)
	if r.Code != 0 {
		return false, "revoking it failed: " +
			strings.Join(strings.Fields(r.Stderr), " ")
	}
	*revoked = true
	c, err := litellm.NewWith(litellm.Options{BaseURL: base,
		MasterKey: key, UserAgent: "local-verse-proxy",
		Timeout: 30 * time.Second, EnvProxy: true})
	if err != nil {
		return false, err.Error()
	}
	code, _, err := c.Raw(ctx, "/v1/models", true)
	switch {
	case err != nil:
		return false, "no answer after revoking: " + err.Error()
	case code == 401:
		return true, "revoked, then 401."
	}
	return false, fmt.Sprintf("revoked, but the key still got %d, "+
		"not 401.", code)
}
