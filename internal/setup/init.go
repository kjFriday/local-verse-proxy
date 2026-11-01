package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/fsx"
	"github.com/kjFriday/local-verse-proxy/internal/seats"
)

// Volume is where Postgres keeps its data, and the password it was
// first started with (compose.yaml names the project).
const Volume = "local-verse-proxy_pgdata"

// exampleOllama is the template's made-up Ollama address.
const exampleOllama = "http://192.168.1.50:11434"

// ollamaWait is how long init and check give Ollama to answer.
const ollamaWait = 5 * time.Second

// Init is `lvp init [--ollama URL] [--desk-password] [--force]`.
func (s *System) Init(ctx context.Context, argv []string,
	std args.Std) int {
	cmd := s.Prog + " init"
	usage := cmd + " [--ollama URL] [--desk-password] [--force]"
	fl := args.New(cmd, usage)
	ollama := fl.String("ollama", "", "URL", "",
		"your Ollama's address, like http://192.168.1.50:11434; "+
			"init tries it")
	desk := fl.Bool("desk-password", "",
		"add a random FRONTDESK_PASSWORD, if there is none")
	force := fl.Bool("force", "", "set a new database password even "+
		"though Postgres already has one")
	pos, err := fl.Parse(argv)
	if err == nil && len(pos) > 0 {
		err = extra(cmd, usage, pos)
	}
	if err != nil {
		if errors.Is(err, args.ErrHelp) {
			fmt.Fprint(std.Out, fl.Help()+initHelp)
			return 0
		}
		return args.Exit(std, fl, err)
	}
	var base *url.URL
	if *ollama != "" {
		if base, err = ollamaURL(*ollama); err != nil {
			return usageError(std, cmd, usage,
				"argument --ollama: "+err.Error())
		}
	}
	dir, err := s.folder()
	if err != nil {
		return fail(std, err.Error())
	}
	path := filepath.Join(dir, seats.Name)
	b, err := os.ReadFile(path)
	made := false
	switch {
	case errors.Is(err, fs.ErrNotExist):
		b, err = os.ReadFile(filepath.Join(dir, "seats.env.example"))
		if err != nil {
			return fail(std, "cannot read seats.env.example "+
				"in "+dir+": "+plainErr(err)+". It comes "+
				"with the download: unpack it again, or get "+
				"it from the repo.")
		}
		made = true
	case err != nil:
		return fail(std, "cannot read "+path+": "+plainErr(err))
	}
	if _, ok := fromUTF16(b); ok {
		return fail(std, path+" seems to be saved as UTF-16. Save "+
			"it again as UTF-8 (in Notepad: File, Save as, then "+
			"Encoding: UTF-8), then run init again.")
	}
	t := readText(b)
	r, err := s.fill(t)
	if err != nil {
		return fail(std, err.Error())
	}
	if r.dbPassword && !*force {
		if code := s.volumeCheck(ctx, std, dir); code != 0 {
			return code
		}
	}
	desked := false
	if *desk && len(t.find("FRONTDESK_PASSWORD")) == 0 {
		pw, err := s.randHex(12)
		if err != nil {
			return fail(std, err.Error())
		}
		t.add("FRONTDESK_PASSWORD=" + pw)
		desked = true
	}
	if base != nil {
		setOllama(t, base.String())
	}
	out := std.Out
	changed := made || string(t.bytes()) != string(b)
	if changed {
		// Private from the start: the temporary file is too.
		if err := fsx.WriteFile(path, t.bytes(), true); err != nil {
			return fail(std, "cannot write "+path+": "+
				plainErr(err))
		}
	} else if ok, _, err := fsx.IsPrivate(path); err == nil && !ok {
		if err := fsx.Private(path); err == nil {
			say(out, "seats.env could be opened by others; now "+
				"only you may.")
		}
	}
	if made {
		say(out, "Made seats.env from seats.env.example. Only you "+
			"may open it.")
	}
	if len(r.names) > 0 {
		say(out, "Wrote new random values in "+
			strings.Join(r.names, ", ")+". They are not shown "+
			"here; they stay in seats.env (README.md, 3a. The "+
			"three secrets).")
	}
	if *force && r.dbPassword {
		say(out, "--force: if Postgres was started before, it "+
			"keeps its old password, and the proxy cannot reach "+
			"it until you run docker compose down -v (which "+
			"deletes every key).")
	}
	switch {
	case desked:
		say(out, "Added FRONTDESK_PASSWORD: 24 random letters and "+
			"digits. Open seats.env to see it.")
	case *desk:
		say(out, "FRONTDESK_PASSWORD is already in seats.env; kept "+
			"it.")
	}
	if base != nil {
		say(out, "Set OLLAMA_API_BASE="+base.Redacted()+".")
		s.tryOllama(ctx, std, base)
	}
	if !changed {
		say(out, "seats.env is filled in already; nothing changed.")
	}
	next := "docker compose up -d, then " + s.Prog + " wait"
	if at := t.find("OLLAMA_API_BASE"); len(at) > 0 &&
		value(t.lines[at[len(at)-1]]) == exampleOllama {
		next = s.Prog + " init --ollama http://<ollama-ip>:11434 " +
			"(README.md, Step 1 says how to find it), then " + next
	}
	say(out, "Next: "+next)
	return 0
}

// volumeCheck refuses, exit 1, if Postgres has its volume already: it
// keeps the password of its first start.
func (s *System) volumeCheck(ctx context.Context, std args.Std,
	dir string) int {
	has, err := s.hasVolume(ctx, dir)
	if err != nil {
		fmt.Fprint(std.Err, wrap("", "warning: could not ask Docker "+
			"whether Postgres already has a password ("+
			err.Error()+"). If the proxy ran from this folder "+
			"before, see README.md, Troubleshooting: minting."))
	}
	if !has {
		return 0
	}
	return fail(std, "seats.env still has the template's database "+
		"password, but Postgres already saved one, in the Docker "+
		"volume "+Volume+", when the proxy first started. A new "+
		"password here would lock the proxy out of its database, "+
		"so nothing was changed. Put the old password back in "+
		"POSTGRES_PASSWORD and DATABASE_URL; or, if no keys are "+
		"worth keeping, run docker compose down -v (it deletes "+
		"every key), then "+s.Prog+" init again. "+s.Prog+
		" init --force sets a new password anyway.")
}

const initHelp = `
Makes seats.env from seats.env.example if it is missing, and fills
in the template's placeholders: the master key, the salt key and the
database password. A second run changes nothing. No secret is shown.
`

// filled is what fill changed: the names of the settings, and
// whether the database password was one.
type filled struct {
	names      []string
	dbPassword bool
}

// fill fills the template's placeholders, as README 3a's sed lines
// did, in their order: an empty LITELLM_MASTER_KEY= gets sk- and 48
// hex digits; LITELLM_SALT_KEY=sk-change-me the same; every other
// change-me, one password of 32 hex digits, the same everywhere.
func (s *System) fill(t *textFile) (filled, error) {
	var r filled
	pw := ""
	for i, l := range t.lines {
		var err error
		switch {
		case l == "LITELLM_MASTER_KEY=":
			var k string
			k, err = s.randHex(24)
			t.lines[i] = l + "sk-" + k
		case l == "LITELLM_SALT_KEY=sk-change-me":
			var k string
			k, err = s.randHex(24)
			t.lines[i] = "LITELLM_SALT_KEY=sk-" + k
		case strings.Contains(l, "change-me"):
			if pw == "" {
				pw, err = s.randHex(16)
			}
			t.lines[i] = strings.ReplaceAll(l, "change-me", pw)
			switch name(l) {
			case "POSTGRES_PASSWORD", "DATABASE_URL":
				r.dbPassword = true
			}
		default:
			continue
		}
		if err != nil {
			return r, err
		}
		n := name(l)
		if n == "" {
			n = fmt.Sprintf("line %d", i+1)
		}
		r.names = append(r.names, n)
	}
	return r, nil
}

// hasVolume says whether Docker holds Postgres's volume.
func (s *System) hasVolume(ctx context.Context, dir string) (bool,
	error) {
	var out, errb strings.Builder
	err := s.Docker(ctx, dir, &out, &errb, "volume", "ls", "-q",
		"--filter", "name="+Volume)
	if err != nil {
		return false, errors.New(dockerError(err, errb.String()))
	}
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.TrimSpace(l) == Volume {
			return true, nil
		}
	}
	return false, nil
}

// ollamaURL checks an Ollama address for seats.env: a web address
// with nothing a shell would read as code.
func ollamaURL(s string) (*url.URL, error) {
	raw := strings.TrimRight(strings.TrimSpace(s), "/")
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return nil, errors.New("is not a web address. It should " +
			"look like http://192.168.1.50:11434")
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("must start with http:// or https://")
	case u.Host == "":
		return nil, errors.New("has no host name")
	}
	for _, c := range raw {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' ||
			strings.ContainsRune(":/.-_[]%~+", c)) {
			return nil, fmt.Errorf("may not hold %q: seats.env "+
				"could not keep it as it is", c)
		}
	}
	return u, nil
}

// setOllama puts url in every OLLAMA_API_BASE line, or adds one.
func setOllama(t *textFile, url string) {
	at := t.find("OLLAMA_API_BASE")
	if len(at) == 0 {
		t.add("OLLAMA_API_BASE=" + url)
		return
	}
	for _, i := range at {
		l := t.lines[i]
		t.lines[i] = l[:strings.IndexByte(l, '=')+1] + url
	}
}

// tryOllama warns unless Ollama says it is running within 5 seconds.
func (s *System) tryOllama(ctx context.Context, std args.Std,
	u *url.URL) bool {
	if h := u.Hostname(); h == "localhost" || strings.HasPrefix(h,
		"127.") || h == "::1" {
		fmt.Fprint(std.Err, wrap("", "warning: inside the "+
			"proxy's container, "+h+" means the container "+
			"itself, not this computer. Use this computer's "+
			"network address (README.md, Find the proxy "+
			"address)."))
	}
	why := askOllama(ctx, u.String())
	if why == "" {
		say(std.Out, "Ollama answers there.")
		return true
	}
	fmt.Fprint(std.Err, wrap("", "warning: Ollama did not answer at "+
		u.Redacted()+" within 5 seconds ("+why+"). Check the "+
		"address, and that OLLAMA_HOST=0.0.0.0 is set on that "+
		"machine (README.md, Letting the proxy reach Ollama)."))
	return false
}

// askOllama is "" if Ollama at u says it is running, else why not.
func askOllama(ctx context.Context, u string) string {
	ctx, cancel := context.WithTimeout(ctx, ollamaWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "not a web address"
	}
	resp, err := plainClient.Do(req)
	if err != nil {
		return netReason(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if strings.Contains(string(body), "Ollama is running") {
		return ""
	}
	return fmt.Sprintf("it answered %d, not \"Ollama is running\"",
		resp.StatusCode)
}

// plainClient asks without any key; web proxies from the
// environment are used, but never for this computer.
var plainClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// netReason is a failed request in plain words.
func netReason(err error) string {
	var ne net.Error
	var dns *net.DNSError
	var op *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &ne) && ne.Timeout():
		return "no answer"
	case errors.As(err, &dns):
		return "cannot find the computer named " + dns.Name
	case errors.As(err, &op) && strings.Contains(op.Err.Error(),
		"refused"):
		return "connection refused"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

// plainErr is an OS error without the path, which the message gives.
func plainErr(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return err.Error()
}
