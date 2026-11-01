package litellm

import (
	"context"
	"net"
	"strings"
	"time"
)

// v4After is how long a dial to localhost gives ::1 before it also
// tries 127.0.0.1, as curl does (Happy Eyeballs, RFC 8305).
var v4After = 300 * time.Millisecond

type dialFunc func(ctx context.Context, network, addr string) (net.Conn,
	error)

// localhostFirst wraps dial so that "localhost" means ::1 first, then
// 127.0.0.1, as curl (and so mint-key.sh) reads it. Go's resolver
// takes /etc/hosts, where on many Linux systems localhost is
// 127.0.0.1 only; in WSL with Docker, 127.0.0.1:4000 can hang where
// [::1]:4000 answers. Every other host is dialed as it is.
func localhostFirst(dial dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn,
		error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || !strings.EqualFold(host, "localhost") {
			return dial(ctx, network, addr)
		}
		return race(ctx, dial, network,
			net.JoinHostPort("::1", port),
			net.JoinHostPort("127.0.0.1", port))
	}
}

// race dials first, and second after v4After (or at once if first
// fails); the first connection made wins, the other is closed.
func race(ctx context.Context, dial dialFunc, network, first,
	second string) (net.Conn, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		c   net.Conn
		err error
	}
	out := make(chan result, 2)
	try := func(addr string) {
		c, err := dial(ctx, network, addr)
		out <- result{c, err}
	}
	go try(first)
	timer := time.NewTimer(v4After)
	defer timer.Stop()
	started, done := 1, 0
	var firstErr error
	for {
		select {
		case <-timer.C:
			if started == 1 {
				started++
				go try(second)
			}
		case r := <-out:
			done++
			if r.err == nil {
				if started > done {
					// The other dial is cut short by
					// cancel; close it if it wins anyway.
					go func() {
						if l := <-out; l.c != nil {
							l.c.Close()
						}
					}()
				}
				return r.c, nil
			}
			if firstErr == nil {
				firstErr = r.err
			}
			if started == 1 {
				started++
				go try(second)
			} else if done == started {
				return nil, firstErr
			}
		}
	}
}
