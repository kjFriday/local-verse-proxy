package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/desk"
	"github.com/kjFriday/local-verse-proxy/internal/frontdesk/pyjson"
	"github.com/kjFriday/local-verse-proxy/internal/mintkey"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// The form's rules, as frontdesk.py's (fullmatch).
var (
	SeatRE   = regexp.MustCompile(`^(?:[A-Za-z0-9][A-Za-z0-9._-]{0,39})\z`)
	BudgetRE = regexp.MustCompile(`^(?:[0-9]{1,6}([.][0-9]{1,2})?)\z`)
	// What mint-key takes; "m" is minutes and "mo" months.
	DurationRE = regexp.MustCompile(`^(?:[1-9][0-9]{0,3}(mo|[smhdw]))\z`)
	KeyRE      = regexp.MustCompile(`^(?:sk-[A-Za-z0-9_-]{8,200})\z`)
	// Key-like text in an error message: keys, key ends, hashes.
	SecretRE = regexp.MustCompile(`sk-[^` + pyfmt.SpaceClass() +
		`]*|[0-9A-Fa-f]{32,}`)
)

// Refills are how often a budget refills: "" is never.
var Refills = []string{"", "1d", "1w", "1mo"}

// The waits around one mint-key run: the whole call, and mint-key's
// own request (under it, so a slow proxy gets a message).
const (
	MintWait    = 30 * time.Second
	MintTimeout = 25 * time.Second
)

// mintWait is MintWait; tests shorten it.
var mintWait = MintWait

// errMintLate is mint-key not done in MintWait: the 500 path, as
// Python's TimeoutExpired.
type errMintLate struct{}

func (errMintLate) Error() string {
	return "mint-key did not finish in 30 seconds"
}

// Plain is plain_error: mint-key's complaint in plain words, minus
// anything that looks like a key or a hash. It logs mint-key's last
// line.
func (a *App) Plain(text string) string {
	t := SecretRE.ReplaceAllString(a.o.Live.Scrub(text), "[hidden]")
	var last string
	has := false
	for _, x := range pyfmt.SplitLines(pyfmt.Strip(t)) {
		if pyfmt.Strip(x) != "" {
			last, has = x, true
		}
	}
	said := "nothing"
	if has {
		said = pyfmt.Cut(last, 300)
	}
	a.o.Log.Line("mint-key said: " + said)
	in := func(s ...string) bool {
		for _, x := range s {
			if strings.Contains(t, x) {
				return true
			}
		}
		return false
	}
	switch {
	case in("already exists"):
		return "That seat name is taken. Cancel its card first, or " +
			"pick a new name, like laptop-2."
	case in("Authentication Error", "error: 401"):
		if a.o.Docker {
			return desk.MasterRefusedDocker
		}
		return desk.MasterRefused
	case in("No keys found", "error: 404"):
		return "No such key card. It may be cancelled already."
	// The Go mint-key: the proxy got the request, but was too slow.
	case in("may have made the key anyway"):
		return "The proxy took too long to answer, and may have " +
			"made the card anyway. If it shows up in a few " +
			"seconds, cancel it and make it again: its key was " +
			"never shown."
	case in("may have revoked it anyway"):
		return "The proxy took too long to answer, and may have " +
			"cancelled the card anyway. Check again in a few " +
			"seconds."
	case in("Failed to connect", "(7)", "(52)"):
		return "The proxy is not answering. Is it running? Check " +
			"with: docker compose ps"
	case in("change-me"):
		return "seats.env still has change-me values. Do Step 3a."
	}
	if has {
		return "mint-key said: " + said
	}
	return "mint-key said: nothing (it failed)"
}

// runMint runs mint-key's rules with args, one run at a time. ctx
// must not end when the viewer hangs up: as Python's subprocess.run,
// a mint or revoke once started is finished, and only MintWait ends
// it.
func (a *App) runMint(ctx context.Context, args []string) mintkey.Result {
	a.work.Lock()
	defer a.work.Unlock()
	file, err := a.o.MintFile()
	if err != nil {
		return mintkey.Result{Stderr: err.Error() + "\n", Code: 1}
	}
	ctx, cancel := context.WithTimeout(ctx, mintWait)
	defer cancel()
	r := a.o.Mint(ctx, file, args, MintTimeout)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		panic(errMintLate{})
	}
	return r
}

// str is str(data.get(k, "")).strip().
func str(data map[string]any, k string) string {
	v, ok := data[k]
	if !ok {
		return ""
	}
	return pyfmt.Strip(pyfmt.Str(v))
}

func (a *App) mint(w http.ResponseWriter, req *http.Request,
	data map[string]any) {
	d := a.o.Desk
	d.Fresh()
	state := d.Current()
	if state == nil {
		state = &desk.State{}
	}
	seat, _ := data["seat"].(string)
	seat = pyfmt.Strip(seat)
	if !SeatRE.MatchString(seat) {
		a.fail(w, req, 400, "Seat names use letters, digits, dots, "+
			"dashes and underscores, up to 40, with no spaces.",
			"seat")
		return
	}
	// The game mints its own seats; one made here would clash.
	if g := d.Games(); g.Owns(seat) {
		pre := g.Prefix()
		what := "That name is"
		if pre != "" && strings.HasPrefix(pyfmt.Lower(seat), pre) {
			what = "Names that start with " + pre + " are"
		}
		a.fail(w, req, 400, what+" kept for the office game. Pick "+
			"another name.", "seat")
		return
	}
	if !state.Up {
		a.fail(w, req, 503, "The proxy is not answering, so no card "+
			"can be made right now.", "")
		return
	}
	list, ok := data["models"].([]any)
	known := map[string]bool{}
	for _, m := range state.Models {
		known[m] = true
	}
	var models []string
	for _, m := range list {
		s, isStr := m.(string)
		if !isStr || !known[s] {
			ok = false
		}
		models = append(models, s)
	}
	if !ok || len(list) == 0 {
		a.fail(w, req, 400, "Pick at least one model from the list.",
			"models")
		return
	}
	models = sortedUnique(models)
	budget := str(data, "budget")
	if f, err := strconv.ParseFloat(budget, 64); !BudgetRE.MatchString(
		budget) || err != nil || !(f > 0) {
		a.fail(w, req, 400, "Budget is a number of US dollars above "+
			"0, like 5 or 2.50.", "budget")
		return
	}
	refill := ""
	if v, has := data["refill"]; has {
		s, isStr := v.(string)
		if !isStr || !oneOf(s, Refills) {
			a.badRefill(w, req)
			return
		}
		refill = s
	}
	duration := str(data, "duration")
	if !DurationRE.MatchString(duration) {
		a.fail(w, req, 400, "Pick how long the card lasts.", "duration")
		return
	}
	// Sam and sam would split one person's cards and tally.
	for _, c := range state.Cards {
		if pyfmt.Lower(c.Seat) == pyfmt.Lower(seat) {
			a.fail(w, req, 409, c.Seat+" already has a key card. "+
				"Cancel it first, or pick a new name.", "seat")
			return
		}
	}
	args := []string{seat, "--models", strings.Join(models, ","),
		"--budget", budget, "--duration", duration}
	if refill != "" {
		args = append(args, "--budget-duration", refill)
	}
	r := a.runMint(context.WithoutCancel(req.Context()), args)
	key := pyfmt.Strip(r.Stdout)
	if r.Code != 0 || !KeyRE.MatchString(key) {
		a.o.Log.Line("mint failed for seat " + seat)
		msg := a.Plain(r.Stderr)
		field := ""
		if strings.Contains(msg, "taken") {
			field = "seat"
		}
		a.fail(w, req, 400, msg, field)
		return
	}
	a.o.Log.Line("minted a key card for seat " + seat)
	d.ShowChange(context.Background(), seat, false)
	a.send(w, req, reply{code: 200, body: pyjson.Marshal(pyjson.O(
		"seat", seat, "key", key, "models", models, "budget", budget,
		"duration", duration, "refill", refill,
		"base_url", a.SeatURL(req.Host)))})
}

func (a *App) badRefill(w http.ResponseWriter, req *http.Request) {
	a.fail(w, req, 400, "Pick how often the budget refills: never, "+
		"every day, every week or every month.", "refill")
}

func oneOf(s string, list []string) bool {
	for _, x := range list {
		if s == x {
			return true
		}
	}
	return false
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func (a *App) revoke(w http.ResponseWriter, req *http.Request,
	data map[string]any) {
	d := a.o.Desk
	d.Fresh()
	var cards []desk.Card
	if st := d.Current(); st != nil {
		cards = st.Cards
	}
	id, _ := data["id"].(string)
	_, idIsStr := data["id"].(string)
	seat, seatIsStr := data["seat"].(string)
	var card *desk.Card
	for i := range cards {
		if idIsStr && cards[i].ID == id {
			card = &cards[i]
			break
		}
	}
	if card == nil && seatIsStr && seat != "" {
		for i := range cards {
			if cards[i].Seat == seat {
				card = &cards[i]
				break
			}
		}
	}
	if card == nil {
		a.fail(w, req, 404, "No key card like that. It may be "+
			"cancelled already.", "")
		return
	}
	var args []string
	what := card.Seat
	if card.Seat != "" {
		args = []string{"--revoke-seat", card.Seat}
	} else {
		token := d.Token(card.ID)
		if token == "" {
			a.fail(w, req, 404, "That card is gone. Reload.", "")
			return
		}
		args, what = []string{"--revoke", token}, "(no seat name)"
	}
	r := a.runMint(context.WithoutCancel(req.Context()), args)
	if r.Code != 0 {
		a.o.Log.Line("revoke failed for seat " + what)
		a.fail(w, req, 400, a.Plain(r.Stderr), "")
		return
	}
	a.o.Log.Line("cancelled the key card for seat " + what)
	if card.Seat != "" {
		d.ShowChange(context.Background(), card.Seat, true)
	} else {
		d.RefreshNow()
	}
	a.send(w, req, reply{code: 200, body: pyjson.Marshal(pyjson.O(
		"ok", true, "seat", card.Seat))})
}
