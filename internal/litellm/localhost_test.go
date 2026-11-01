package litellm

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeDial answers each address as told: a delay, then a connection
// or an error; a delay of -1 waits for the context. It records the
// order of the dials.
type fakeDial struct {
	mu    sync.Mutex
	order []string
	plan  map[string]struct {
		wait time.Duration
		fail bool
	}
}

func (f *fakeDial) dial(ctx context.Context, _, addr string) (net.Conn,
	error) {
	f.mu.Lock()
	f.order = append(f.order, addr)
	p := f.plan[addr]
	f.mu.Unlock()
	if p.wait < 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	select {
	case <-time.After(p.wait):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p.fail {
		return nil, errors.New("refused " + addr)
	}
	a, b := net.Pipe()
	b.Close()
	return conn{a, addr}, nil
}

// conn is a connection that says which address it went to.
type conn struct {
	net.Conn
	to string
}

func TestLocalhostFirst(t *testing.T) {
	defer func(d time.Duration) { v4After = d }(v4After)
	v4After = 50 * time.Millisecond
	type p = struct {
		wait time.Duration
		fail bool
	}
	v6, v4 := "[::1]:4000", "127.0.0.1:4000"
	for _, c := range []struct {
		name    string
		plan    map[string]p
		addr    string
		want    string
		fast    bool // done well before v4After
		wantErr bool
	}{
		{"v6 answers", map[string]p{v6: {}, v4: {}}, "localhost:4000",
			v6, true, false},
		{"v6 refused", map[string]p{v6: {fail: true}, v4: {}},
			"localhost:4000", v4, true, false},
		{"v6 hangs", map[string]p{v6: {wait: -1}, v4: {}},
			"LocalHost:4000", v4, false, false},
		{"both fail", map[string]p{v6: {fail: true},
			v4: {fail: true}}, "localhost:4000", "", true, true},
		{"other host", map[string]p{"10.0.0.2:4000": {}},
			"10.0.0.2:4000", "10.0.0.2:4000", true, false},
		{"loopback as typed", map[string]p{v4: {}},
			"127.0.0.1:4000", v4, true, false},
	} {
		f := &fakeDial{plan: c.plan}
		start := time.Now()
		got, err := localhostFirst(f.dial)(context.Background(), "tcp",
			c.addr)
		took := time.Since(start)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: no error", c.name)
			}
			continue
		}
		if err != nil || got.(conn).to != c.want {
			t.Errorf("%s: %v %v (dials %v)", c.name, got, err,
				f.order)
			continue
		}
		got.Close()
		if c.fast && took >= v4After {
			t.Errorf("%s: took %v", c.name, took)
		}
		if !c.fast && took < v4After {
			t.Errorf("%s: 127.0.0.1 tried before %v", c.name,
				v4After)
		}
	}
}

// TestLocalhostReal: on this machine, localhost reaches a server that
// listens on 127.0.0.1 only (::1 refused or missing).
func TestLocalhostReal(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	d := localhostFirst((&net.Dialer{Timeout: 5 * time.Second}).
		DialContext)
	c, err := d(context.Background(), "tcp", "localhost:"+port)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}
