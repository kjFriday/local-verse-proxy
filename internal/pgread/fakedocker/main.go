// Command fakedocker stands in for `docker compose exec -T db psql
// ...` in tests, on every system, with no Docker and no Postgres. It
// speaks both of the ways pgread runs psql: a long-lived one fed
// questions on its input, each ended by an \echo of a mark (Front
// Desk's, with VERBOSITY=terse), and one psql per question that reads
// one script and exits (the game's, with ON_ERROR_STOP=1).
//
// What it answers comes from the file FAKEDOCKER_SCRIPT, one step per
// question, used in order (the last one again when they run out):
//
//	ok <json>         the answer; a literal \n in it starts a new line
//	error <text>      an error line (persistent) or psql's error on
//	                  stderr and exit 3 (one-shot)
//	fatal <text>      a FATAL line, then the mark
//	noanswer          only the mark
//	stall             no answer at all (one-shot: sleep 60 s)
//	die <text>        <text> on its output, then exit 1
//	startfail <text>  at start, before any question: <text>, exit 1
//
// FAKEDOCKER_STATE (default: the script's path plus ".n") counts the
// steps used, across processes. If FAKEDOCKER_LOG names a folder, each
// run writes there <n>.json (its arguments, folder and environment
// names) and <n>.stdin (every byte it was given).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	script := os.Getenv("FAKEDOCKER_SCRIPT")
	state := os.Getenv("FAKEDOCKER_STATE")
	if state == "" {
		state = script + ".n"
	}
	var stdinLog io.Writer = io.Discard
	if dir := os.Getenv("FAKEDOCKER_LOG"); dir != "" {
		stdinLog = startLog(dir)
	}
	once := false
	for _, a := range os.Args {
		if a == "ON_ERROR_STOP=1" {
			once = true
		}
	}
	s := &steps{script: script, state: state}
	if kind, text := s.peek(); kind == "startfail" {
		s.next()
		fmt.Fprintln(os.Stderr, text)
		os.Exit(1)
	}
	in := io.TeeReader(os.Stdin, stdinLog)
	if once {
		oneShot(in, s)
		return
	}
	persistent(in, s)
}

func startLog(dir string) io.Writer {
	os.MkdirAll(dir, 0o755)
	n := 0
	for {
		p := filepath.Join(dir, strconv.Itoa(n)+".json")
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			0o644)
		if err != nil {
			n++
			continue
		}
		wd, _ := os.Getwd()
		var names []string
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			names = append(names, k)
		}
		json.NewEncoder(f).Encode(map[string]any{"argv": os.Args[1:],
			"dir": wd, "env": names})
		f.Close()
		w, _ := os.Create(filepath.Join(dir, strconv.Itoa(n)+".stdin"))
		return w
	}
}

type steps struct{ script, state string }

func (s *steps) lines() []string {
	b, _ := os.ReadFile(s.script)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (s *steps) count() int {
	b, _ := os.ReadFile(s.state)
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func (s *steps) peek() (string, string) {
	ls := s.lines()
	if len(ls) == 0 {
		return "ok", "{}"
	}
	n := s.count()
	if n >= len(ls) {
		n = len(ls) - 1
	}
	kind, text, _ := strings.Cut(ls[n], " ")
	return kind, strings.ReplaceAll(text, `\n`, "\n")
}

func (s *steps) next() (string, string) {
	kind, text := s.peek()
	os.WriteFile(s.state, []byte(strconv.Itoa(s.count()+1)), 0o644)
	return kind, text
}

func persistent(in io.Reader, s *steps) {
	r := bufio.NewReader(in)
	out := bufio.NewWriter(os.Stdout)
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if mark, ok := strings.CutPrefix(line, `\echo `); ok {
			kind, text := s.next()
			switch kind {
			case "ok", "error", "fatal":
				fmt.Fprintln(out, text)
				fmt.Fprintln(out, mark)
			case "noanswer":
				fmt.Fprintln(out, mark)
			case "die":
				fmt.Fprintln(out, text)
				out.Flush()
				os.Exit(1)
			case "stall":
			}
			out.Flush()
		}
		if err != nil {
			out.Flush()
			return
		}
	}
}

func oneShot(in io.Reader, s *steps) {
	io.ReadAll(in)
	kind, text := s.next()
	switch kind {
	case "ok":
		fmt.Println(text)
	case "error", "fatal":
		fmt.Fprintln(os.Stderr, "psql:<stdin>:2: "+text)
		os.Exit(3)
	case "noanswer":
	case "stall":
		time.Sleep(60 * time.Second)
	default:
		fmt.Fprintln(os.Stderr, text)
		os.Exit(1)
	}
}
