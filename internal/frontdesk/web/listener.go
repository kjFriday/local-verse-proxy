package web

import (
	"net"
	"sync"
	"time"
)

// The limits, as frontdesk.py.
const (
	MaxStreams     = 100
	MaxConnections = 256 // open connections in all
	MaxPerDevice   = 32  // with --lan or --docker: per remote address
	ReadTimeout    = 10 * time.Second
	maxBody        = 8192
	maxLine        = 65536 // a request line or one header line
	maxHeaders     = 100
)

// writeWait is how long one answer's write may take (ReadTimeout);
// tests shorten it.
var writeWait = ReadTimeout

// Listen is a tcp4 listener on bind:port with the caps: MaxConnections
// in all, and perDevice (0: no such cap) per remote address. Past a
// cap a new connection is closed at once, and Accept goes on. A
// connection holds its slot for its whole life, streams too, and
// gives it back exactly once, on close.
func Listen(bind string, port, perDevice int) (net.Listener, error) {
	l, err := net.Listen("tcp4", net.JoinHostPort(bind,
		itoa(port)))
	if err != nil {
		return nil, err
	}
	return Cap(l, MaxConnections, perDevice), nil
}

// Cap wraps l with the caps; Listen uses it, tests too.
func Cap(l net.Listener, all, perDevice int) net.Listener {
	return &capped{Listener: l, all: all, perDevice: perDevice,
		open: map[string]int{}}
}

type capped struct {
	net.Listener
	all, perDevice int
	mu             sync.Mutex
	n              int
	open           map[string]int
}

// Open is how many connections hold a slot, for tests.
func Open(l net.Listener) int {
	c := l.(*capped)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *capped) take(addr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n >= c.all {
		return false
	}
	if c.perDevice > 0 {
		if c.open[addr] >= c.perDevice {
			return false
		}
		c.open[addr]++
	}
	c.n++
	return true
}

func (c *capped) giveBack(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.perDevice > 0 {
		if c.open[addr]--; c.open[addr] <= 0 {
			delete(c.open, addr)
		}
	}
	c.n--
}

func (c *capped) Accept() (net.Conn, error) {
	for {
		conn, err := c.Listener.Accept()
		if err != nil {
			return nil, err
		}
		addr := hostOf(conn.RemoteAddr().String())
		if !c.take(addr) {
			conn.Close()
			continue
		}
		return &slotConn{Conn: conn, back: func() {
			c.giveBack(addr)
		}}, nil
	}
}

type slotConn struct {
	net.Conn
	once sync.Once
	back func()
}

func (s *slotConn) Close() error {
	err := s.Conn.Close()
	s.once.Do(s.back)
	return err
}

func hostOf(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}
