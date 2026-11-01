// Owner: C3

// Package fakeollama is the practice model server (lvp fake-ollama):
// a tiny Ollama look-alike for the office game, a line-by-line port
// of game/fake_ollama.py. It does no AI at all. It answers with
// filler text, but it counts tokens like a real model (about 4
// characters per token), so a long prompt really costs more mana.
//
// It speaks just enough of the Ollama API for LiteLLM: /,
// /api/version, /api/tags, /api/show and /api/chat (streamed and
// not). Nothing is logged: prompts never reach a log.
package fakeollama

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
	"github.com/kjFriday/local-verse-proxy/internal/pyfmt"
)

// Port is where the server listens, on every IPv4 address.
const Port = 11434

// Main is `lvp fake-ollama`.
func Main(argv []string, std args.Std) int {
	cmd := args.Prog() + " fake-ollama"
	fs := args.New(cmd, cmd+" [-h]")
	pos, err := fs.Parse(argv)
	if err == nil && len(pos) > 0 {
		err = &args.UsageError{Cmd: cmd, Usage: cmd + " [-h]",
			Msg: "unrecognized arguments: " + join(pos)}
	}
	if err != nil {
		if errors.Is(err, args.ErrHelp) {
			fmt.Fprint(std.Out, "usage: "+cmd+" [-h]\n\n"+
				"The practice model server for the office "+
				"game: answers like\nOllama on port "+
				strconv.Itoa(Port)+", with filler text. It "+
				"runs in Docker (game/README.md).\n")
			return 0
		}
		return args.Exit(std, fs, err)
	}
	fmt.Fprintf(std.Out, "practice model server on :%d\n", Port)
	ln, err := net.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(Port))
	if err != nil {
		fmt.Fprintf(std.Err, "%s: cannot listen on port %d: %s\n", cmd,
			Port, pyfmt.StrError(err))
		return 1
	}
	return serve(ln, std)
}

func serve(ln net.Listener, std args.Std) int {
	srv := &http.Server{
		Handler:           Handler(),
		ReadHeaderTimeout: 60 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	if err := srv.Serve(ln); err != nil {
		fmt.Fprintln(std.Err, "lvp fake-ollama:", err)
		return 1
	}
	return 0
}

func join(a []string) string {
	s := ""
	for i, x := range a {
		if i > 0 {
			s += " "
		}
		s += x
	}
	return s
}
