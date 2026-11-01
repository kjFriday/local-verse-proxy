package pgread

import (
	"net"
	"testing"
)

// listenSilent accepts connections and never says a word, like a
// paused database. It gives the address.
func listenSilent(t *testing.T) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		var held []net.Conn
		for {
			c, err := ln.Accept()
			if err != nil {
				for _, h := range held {
					h.Close()
				}
				return
			}
			held = append(held, c)
		}
	}()
	return ln.Addr().String(), nil
}
