package game

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/kjFriday/local-verse-proxy/internal/gamestate"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
	"github.com/kjFriday/local-verse-proxy/internal/pyrand"
	"github.com/kjFriday/local-verse-proxy/internal/rules"
)

// bot is office.py's Bot: one player working through the day's
// quests with its persona's habits, on its own dice.
type bot struct {
	o        *office
	ctx      context.Context
	cancel   context.CancelFunc
	day      int
	tag      string
	deadline float64
	url      string
	seat     string
	persona  rules.Persona
	rng      *pyrand.Rand
	key      string

	ok, refused int64
	code        int64
	kind        string
	outAt       any // nil (None) or the time it was refused
	stopped     bool

	// mu and abandoned: once the day gives up on the bot (ledger
	// O-4) it writes nothing more.
	mu        sync.Mutex
	abandoned bool
	done      chan struct{}
}

func newBot(ctx context.Context, o *office, player gamestate.Row,
	day int, tag string, deadline float64, url string) (*bot, error) {
	r := o.rules
	persona, ok := r.Personas[player.Str("persona")]
	if !ok {
		return nil, die("seat " + player.Str("name") + ": no persona " +
			pyfmt.ReprString(player.Str("persona")))
	}
	seat := player.Str("seat")
	path := filepath.Join(o.g.keys, seat+".key")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, die(pyfmt.OSErrorText(err, path))
	}
	bctx, cancel := context.WithCancel(ctx)
	return &bot{o: o, ctx: bctx, cancel: cancel, day: day, tag: tag,
		deadline: deadline, url: url, seat: seat, persona: persona,
		rng: pyrand.New(fmt.Sprintf("%s-%d-%s", r.Sim.Seed.Text, day,
			player.Str("name"))),
		key: pyfmt.Strip(string(b)), done: make(chan struct{})}, nil
}

// abandon stops the bot; it writes nothing after this returns.
func (b *bot) abandon() {
	b.mu.Lock()
	b.abandoned = true
	b.mu.Unlock()
	b.cancel()
}

// write runs one statement, unless the bot was abandoned. A failed
// write stops the bot.
func (b *bot) write(sql string, args ...any) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.abandoned {
		return false
	}
	if _, err := b.o.q(sql, args...); err != nil {
		if err != errStopped {
			fmt.Fprintf(b.o.g.err, "bot %s: %v\n", b.seat, err)
		}
		b.stopped = true
		return false
	}
	return true
}

// claim is Office.claim, unless the bot was abandoned.
func (b *bot) claim() (gamestate.Row, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.abandoned {
		return gamestate.Row{}, false
	}
	q, ok, err := b.o.db.Claim(b.seat, b.day, b.rng.Choice)
	if err != nil {
		fmt.Fprintf(b.o.g.err, "bot %s: %v\n", b.seat, dbError(err))
		return gamestate.Row{}, false
	}
	return q, ok
}

// run is Bot.run: quests until the persona's most, the day's end or
// a refusal; then the bot's daylog row.
func (b *bot) run() {
	defer close(b.done)
	defer func() {
		b.write("INSERT OR REPLACE INTO daylog VALUES (?,?,?,?,?,?,?)",
			int64(b.day), b.seat, b.ok, b.refused, b.code, b.kind,
			b.outAt)
	}()
	done := 0
	for done < b.persona.Quests && !b.stopped {
		q, ok := b.claim()
		if !ok {
			break // no quests left today
		}
		b.work(q)
		done++
	}
}

// work is Bot.work: one quest, step by step, handed in and maybe
// sent back.
func (b *bot) work(q gamestate.Row) {
	ps, rng, r := b.persona, b.rng, b.o.rules
	model := "local"
	if rng.Random() < ps.Pro {
		model = "pro"
	}
	steps, perStep, retry := int(q.Int("calls")), ps.PerStep, ps.Retry
	if rng.Random() < ps.Bluff {
		steps, perStep, retry = 1, 1, 0.0 // hand in after one call
	}
	chance := ps.Skill
	if model == "pro" {
		chance += r.Sim.ProBoost
	}
	attempts, calls := 0, 0
	approved := false
	for {
		attempts++
		for step := 0; step < steps; step++ {
			n := perStep
			if rng.Random() < retry {
				n++
			}
			for k := 0; k < n; k++ {
				if !b.call(q, model, step) {
					b.write("UPDATE quests SET state = "+
						"'unfinished' WHERE id = ?",
						q.Get("id"))
					return
				}
				calls++
			}
		}
		approved = rng.Random() < pyMin(0.98, chance)
		if approved || attempts >= r.Quests.MaxAttempts {
			break
		}
		steps = int(math.Ceil(float64(float64(q.Int("calls")) *
			r.Quests.RedoShare)))
		perStep, retry = ps.PerStep, ps.Retry
	}
	if !b.write("UPDATE quests SET state = 'done' WHERE id = ?",
		q.Get("id")) {
		return
	}
	ok := int64(0)
	if approved {
		ok = 1
	}
	b.write("INSERT INTO completions VALUES (?,?,?,?,?,?)", q.Get("id"),
		b.seat, int64(b.day), int64(attempts), ok, int64(calls))
}

// pyMin is Python's min(a, b): b only if it is smaller.
func pyMin(a, b float64) float64 {
	if b < a {
		return b
	}
	return a
}

// pyMax is Python's max(a, b): b only if it is larger.
func pyMax(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}

// call is Bot.call: a pause, then one chat call, tried up to three
// times. false: the bot is done for the day.
func (b *bot) call(q gamestate.Row, model string, step int) bool {
	ps, clk := b.persona, b.o.g.clk
	pause := b.rng.Uniform(ps.Pause[0], ps.Pause[1])
	if b.stopped || clk.Wall()+pause >= b.deadline {
		b.stopped = true
		return false
	}
	if clk.Sleep(b.ctx, pause) != nil {
		b.stopped = true
		return false
	}
	size := b.rng.RandInt(ps.Chars[0], ps.Chars[1])
	qid := q.Str("id")
	text := fmt.Sprintf("Quest %s, step %d: ", qid, step+1)
	for utf8.RuneCountInString(text) < size {
		text += words[b.rng.Choice(len(words))] + " "
	}
	body := b.body(qid, model, text)
	status, kind := 0, ""
	for tries := 0; tries < 3; tries++ {
		status, kind = b.o.g.chat(b.ctx, b.url, b.key, body)
		if b.ctx.Err() != nil {
			b.stopped = true
			return false
		}
		if status == 200 {
			b.ok++
			return true
		}
		if status == 422 || kind == "budget_exceeded" {
			break // the proxy said no; asking again will not help
		}
		if clk.Sleep(b.ctx, float64(1+tries)) != nil {
			b.stopped = true
			return false
		}
	}
	b.refused++
	b.code, b.kind = int64(status), kind
	b.outAt = clk.Wall()
	b.stopped = true // idle for the rest of the day
	return false
}

// body is the call's JSON, as Python's json.dumps writes the dict.
// Tags reach LiteLLM_SpendLogs.request_tags.
func (b *bot) body(qid, model, text string) []byte {
	tags := jsonList([]string{"lvg:" + b.o.idText(), "day:" + b.tag,
		"seat:" + b.seat, "quest:" + qid})
	var s strings.Builder
	s.WriteString(`{"model": ` + jsonString(model) +
		fmt.Sprintf(`, "max_tokens": %d`, b.persona.Reply) +
		`, "messages": [{"role": "user", "content": ` +
		jsonString(text) + `}], "metadata": {"tags": ` + tags + `}`)
	if b.persona.Stream {
		s.WriteString(`, "stream": true, "stream_options": ` +
			`{"include_usage": true}`)
	}
	s.WriteString("}")
	return []byte(s.String())
}
