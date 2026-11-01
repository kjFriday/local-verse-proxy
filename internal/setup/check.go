package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/fsx"
	"github.com/kjFriday/local-verse-proxy/internal/litellm"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// checker prints one line per check, and counts the failures.
type checker struct {
	std   args.Std
	fails int
}

func (c *checker) ok(text string) {
	fmt.Fprint(c.std.Out, wrap("ok    ", text))
}

func (c *checker) warn(text, fix string) {
	fmt.Fprint(c.std.Out, wrap("warn  ", text))
	if fix != "" {
		fmt.Fprint(c.std.Out, wrap("      ", "Fix: "+fix))
	}
}

func (c *checker) fail(text, fix string) {
	c.fails++
	fmt.Fprint(c.std.Out, wrap("FAIL  ", text))
	if fix != "" {
		fmt.Fprint(c.std.Out, wrap("      ", "Fix: "+fix))
	}
}

func (c *checker) skip(text string) {
	fmt.Fprint(c.std.Out, wrap("skip  ", text))
}

// Check is `lvp check`: the setup, step by step, each problem with
// its fix and the README section; exit 1 if any check fails.
func (s *System) Check(ctx context.Context, argv []string,
	std args.Std) int {
	cmd := s.Prog + " check"
	fl := args.New(cmd, cmd)
	pos, err := fl.Parse(argv)
	if err == nil && len(pos) > 0 {
		err = extra(cmd, cmd, pos)
	}
	if err != nil {
		if errors.Is(err, args.ErrHelp) {
			fmt.Fprint(std.Out, fl.Help()+checkHelp)
			return 0
		}
		return args.Exit(std, fl, err)
	}
	c := &checker{std: std}
	dir, err := s.folder()
	if err != nil {
		c.fail(err.Error(), "cd into the folder with compose.yaml "+
			"in it (README.md, Step 2).")
		return 1
	}
	c.ok("proxy folder: " + dir)
	file := s.checkSeats(c, dir)
	docker := s.checkDocker(ctx, c, dir)
	if docker {
		s.checkCompose(ctx, c, dir)
	} else {
		c.skip("docker compose ps: Docker does not run.")
	}
	s.checkProxy(ctx, c, file)
	s.checkOllama(ctx, c, dir, file)
	if c.fails > 0 {
		fmt.Fprint(std.Out, wrap("", fmt.Sprintf("%d check(s) "+
			"failed. Fix the first one first: later ones often "+
			"follow from it.", c.fails)))
		return 1
	}
	say(std.Out, "Everything checks out.")
	return 0
}

const checkHelp = `
Checks, in order: the proxy folder; seats.env (there, filled in,
only yours); Docker; docker compose ps (db healthy, proxy running);
the proxy answers, and takes the master key; Ollama answers (only a
warning, and skipped in practice mode). Exit 1 if any check fails.
`

// checkSeats checks seats.env in dir: there, filled in (the grep
// rule of README 3c), readable, private. The file, or nil.
func (s *System) checkSeats(c *checker, dir string) *seats.File {
	path := filepath.Join(dir, seats.Name)
	b, err := os.ReadFile(path)
	if err != nil {
		c.fail("cannot read seats.env: "+plainErr(err)+".",
			s.Prog+" init makes it (README.md, Step 3).")
		return nil
	}
	if _, ok := fromUTF16(b); ok {
		c.fail("seats.env seems to be saved as UTF-16.", "save it "+
			"again as UTF-8 (in Notepad: File, Save as, then "+
			"Encoding: UTF-8).")
		return nil
	}
	var left []string
	t := readText(b)
	for i, l := range t.lines {
		if strings.Contains(l, "change-me") ||
			l == "LITELLM_MASTER_KEY=" {
			n := name(l)
			if n == "" {
				n = "a comment"
			}
			left = append(left, fmt.Sprintf("line %d (%s)", i+1, n))
		}
	}
	if len(left) > 0 {
		c.fail("seats.env still has placeholders: "+
			strings.Join(left, ", ")+".", s.Prog+" init fills "+
			"them (README.md, 3a. The three secrets).")
	} else {
		c.ok("seats.env is filled in.")
	}
	ok, why, err := fsx.IsPrivate(path)
	switch {
	case err != nil:
		c.fail("cannot tell who may open seats.env: "+plainErr(err),
			"")
	case !ok:
		fix := s.Prog + " init makes it private."
		if s.GOOS != "windows" {
			fix = "chmod 600 seats.env, or " + fix
		}
		c.fail("others may open seats.env: "+why+".", fix)
	default:
		c.ok("seats.env is yours only.")
	}
	f, err := seats.Load(path, s.Env)
	if err != nil {
		c.fail(err.Error(), "make that line a plain NAME=value line "+
			"(README.md, Troubleshooting: minting).")
		return nil
	}
	return f
}

// checkDocker checks that docker runs and may be used; true if so.
func (s *System) checkDocker(ctx context.Context, c *checker,
	dir string) bool {
	var out, errb bytes.Buffer
	err := s.Docker(ctx, dir, &out, &errb, "version", "--format",
		"{{.Server.Version}}")
	if err != nil {
		msg := dockerError(err, errb.String())
		c.fail("Docker: "+msg, dockerHint(msg))
		return false
	}
	c.ok("Docker runs (version " + strings.TrimSpace(out.String()) +
		").")
	return true
}

// psRow is one container, as docker compose ps --format json shows
// it.
type psRow struct {
	Service, State, Health, Status string
}

// parsePS reads docker compose ps --format json: one JSON object a
// line (Compose 2.21 and later), or one array (before).
func parsePS(b []byte) ([]psRow, error) {
	b = bytes.TrimSpace(b)
	var rows []psRow
	if len(b) == 0 {
		return nil, nil
	}
	if b[0] == '[' {
		err := json.Unmarshal(b, &rows)
		return rows, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var r psRow
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// checkCompose checks that db is healthy and proxy runs.
func (s *System) checkCompose(ctx context.Context, c *checker,
	dir string) {
	var out, errb bytes.Buffer
	err := s.Docker(ctx, dir, &out, &errb, "compose", "ps", "--all",
		"--format", "json")
	if err != nil {
		msg := dockerError(err, errb.String())
		fix := dockerHint(msg)
		if strings.Contains(msg, "seats.env") {
			fix = s.Prog + " init (README.md, Step 3)."
		}
		c.fail("docker compose ps: "+msg, fix)
		return
	}
	rows, err := parsePS(out.Bytes())
	if err != nil {
		c.fail("docker compose ps: its answer is not what was "+
			"expected.", "run docker compose ps yourself, and see "+
			"README.md, Is it up?")
		return
	}
	state := map[string]psRow{}
	for _, r := range rows {
		state[r.Service] = r
	}
	up := "docker compose up -d (README.md, Step 4)."
	loop := "docker compose logs proxy shows why (README.md, " +
		"Troubleshooting: the proxy)."
	db, ok := state["db"]
	switch {
	case !ok || db.State != "running":
		c.fail("db (Postgres) is not running.", up)
	case db.Health == "healthy":
		c.ok("db (Postgres) runs, and is healthy.")
	case db.Health == "starting":
		c.fail("db (Postgres) is still starting.", "wait a minute, "+
			"then check again.")
	default:
		c.fail("db (Postgres) runs, but is "+db.Health+".",
			"docker compose logs db shows why (README.md, "+
				"Troubleshooting: the proxy).")
	}
	p, ok := state["proxy"]
	switch {
	case !ok:
		c.fail("proxy (LiteLLM) is not running.", up)
	case p.State == "restarting":
		c.fail("proxy (LiteLLM) keeps restarting.", loop)
	case p.State != "running":
		c.fail("proxy (LiteLLM) is "+p.State+".", up)
	default:
		c.ok("proxy (LiteLLM) runs.")
	}
}

// checkProxy checks that the proxy answers, and takes the master key.
func (s *System) checkProxy(ctx context.Context, c *checker,
	file *seats.File) {
	base := defaultProxy
	if file != nil {
		base = file.Get("PROXY_URL", defaultProxy)
	}
	u, err := litellm.ParseBase(base)
	if err != nil {
		c.fail("PROXY_URL in seats.env "+err.Error(), "")
		return
	}
	addr := u.Redacted()
	if why := knock(ctx, addr); why != "" {
		c.fail("the proxy at "+addr+" does not answer ("+why+").",
			s.Prog+" wait, if it just started (README.md, Is it "+
				"up?).")
		c.skip("the master key: the proxy does not answer.")
		return
	}
	c.ok("the proxy answers at " + addr + ".")
	if file == nil {
		c.skip("the master key: seats.env is not ready.")
		return
	}
	mk, err := file.MasterKey()
	if err != nil {
		c.skip("the master key: " + err.Error())
		return
	}
	cl, err := litellm.New(addr, mk, 0)
	if err != nil {
		c.fail("the master key: "+err.Error(), "")
		return
	}
	var out json.RawMessage
	err = cl.Get(ctx, "/key/list?page=1&size=1", &out)
	var se *litellm.StatusError
	switch {
	case err == nil:
		c.ok("the proxy takes the master key in seats.env.")
	case errors.As(err, &se) && (se.Code == 401 || se.Code == 403):
		c.fail("the proxy does not take the master key in seats.env: "+
			"seats.env changed after the proxy started.",
			"docker compose up -d --force-recreate proxy "+
				"(README.md, Troubleshooting: minting).")
	default:
		c.fail("asking the proxy with the master key: "+err.Error(),
			"README.md, Troubleshooting: the proxy.")
	}
}

// checkOllama warns unless OLLAMA_API_BASE answers; practice mode
// has its own model server, so then it is skipped.
func (s *System) checkOllama(ctx context.Context, c *checker,
	dir string, file *seats.File) {
	if d, err := readDotEnv(dir); err == nil && practiceOn(d) {
		c.skip("Ollama: practice mode is on (" + s.Prog +
			" game practice off ends it).")
		return
	}
	if v, _ := s.Env("COMPOSE_FILE"); strings.Contains(v,
		"compose.game.yaml") {
		c.skip("Ollama: practice mode is on, in COMPOSE_FILE.")
		return
	}
	if file == nil {
		c.skip("Ollama: seats.env is not ready.")
		return
	}
	base := file.Get("OLLAMA_API_BASE", "")
	fix := s.Prog + " init --ollama http://<ollama-ip>:11434 sets " +
		"it and tries it (README.md, Step 1)."
	if base == "" {
		c.warn("OLLAMA_API_BASE is empty in seats.env: model local "+
			"cannot work.", fix)
		return
	}
	u, err := ollamaURL(base)
	if err != nil {
		c.warn("OLLAMA_API_BASE in seats.env "+err.Error()+".", fix)
		return
	}
	if why := askOllama(ctx, u.String()); why != "" {
		c.warn("Ollama does not answer at "+u.Redacted()+" ("+why+
			"). Only model local needs it.", fix)
		return
	}
	c.ok("Ollama answers at " + u.Redacted() + ".")
}
