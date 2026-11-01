package setup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/litellm"
)

// waitGap is the pause between knocks; tryFor, how long one may take.
const (
	waitGap = 3 * time.Second
	tryFor  = 3 * time.Second
)

// Wait is `lvp wait [--timeout 3m]`.
func (s *System) Wait(ctx context.Context, argv []string,
	std args.Std) int {
	cmd := s.Prog + " wait"
	usage := cmd + " [--timeout 3m]"
	fl := args.New(cmd, usage)
	text := fl.String("timeout", "", "TIME", "3m",
		"how long to wait, like 90s, 3m or 5m (default 3m)")
	pos, err := fl.Parse(argv)
	if err == nil && len(pos) > 0 {
		err = extra(cmd, usage, pos)
	}
	if err != nil {
		if errors.Is(err, args.ErrHelp) {
			fmt.Fprint(std.Out, fl.Help()+waitHelp)
			return 0
		}
		return args.Exit(std, fl, err)
	}
	limit, err := duration(*text)
	if err != nil {
		return usageError(std, cmd, usage, "argument --timeout: "+
			err.Error())
	}
	if s.waitUp(ctx, std, limit, *text) {
		return 0
	}
	return 1
}

const waitHelp = `
Knocks on the proxy every 3 seconds, a dot each time, until it
answers. Its address is PROXY_URL in seats.env, or
http://localhost:4000.
`

// duration reads 90s, 3m, 1h30m, or a plain number of seconds.
func duration(text string) (time.Duration, error) {
	t := strings.TrimSpace(text)
	d, err := time.ParseDuration(t)
	if err != nil {
		n, e := strconv.Atoi(t)
		if e != nil {
			return 0, fmt.Errorf("%q is not a time: type it like "+
				"90s, 3m or 5m", text)
		}
		d = time.Duration(n) * time.Second
	}
	if d <= 0 {
		return 0, errors.New("it must be more than 0")
	}
	return d, nil
}

// waitUp knocks until the proxy answers or limit passes; true if it
// answered. The advice of README's "Is it up?" if not.
func (s *System) waitUp(ctx context.Context, std args.Std,
	limit time.Duration, text string) bool {
	base := s.proxyURL()
	u, err := litellm.ParseBase(base)
	if err != nil {
		fmt.Fprint(std.Err, wrap("", "PROXY_URL in seats.env "+
			err.Error()))
		return false
	}
	addr := u.Redacted()
	end := s.Now().Add(limit)
	dots := false
	for {
		if knock(ctx, addr) == "" {
			if dots {
				fmt.Fprintln(std.Out)
			}
			fmt.Fprintln(std.Out, "The proxy is up.")
			return true
		}
		if ctx.Err() != nil || !s.Now().Before(end) {
			break
		}
		if !dots {
			say(std.Out, "Waiting for the proxy at "+addr+
				", up to "+text+". A dot every 3 seconds:")
			dots = true
		}
		fmt.Fprint(std.Out, ".")
		if s.Sleep(ctx, waitGap) != nil {
			break
		}
	}
	if dots {
		fmt.Fprintln(std.Out)
	}
	fmt.Fprint(std.Err, wrap("", "The proxy at "+addr+" did not "+
		"answer within "+text+"."))
	fmt.Fprint(std.Err, notUp)
	return false
}

const notUp = `Run: docker compose ps
db and proxy should both say Up, and db healthy. proxy Restarting, or
Up with a few seconds that keep going back to 0, is a restart loop:
run docker compose logs proxy, and see README.md, Troubleshooting:
the proxy. Neither there? Run docker compose up -d, in this folder.
`

// knock is "" if the proxy at base says it is alive, else why not.
// It asks through the litellm client, so localhost is dialled as
// mint-key dials it (::1 first, then 127.0.0.1); no key is sent.
func knock(ctx context.Context, base string) string {
	c, err := litellm.NewWith(litellm.Options{BaseURL: base,
		MasterKey: "unused", UserAgent: "local-verse-proxy",
		Timeout: tryFor, EnvProxy: true})
	if err != nil {
		return "not a web address"
	}
	code, _, err := c.Raw(ctx, "/health/liveliness", false)
	if err != nil {
		return netReason(err)
	}
	if code != http.StatusOK {
		return "it answered " + strconv.Itoa(code)
	}
	return ""
}
