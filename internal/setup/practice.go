package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kjFriday/local-verse-proxy/internal/args"
)

// The three lines `lvp game practice on` adds to .env, and `off`
// removes: the game's overlay on top of compose.yaml, split by ":"
// on every system (Compose's own split is ";" on Windows).
const (
	practiceMark  = "# lvp game practice on"
	practiceSep   = "COMPOSE_PATH_SEPARATOR=:"
	practiceFiles = "compose.yaml:game/compose.game.yaml"
	practiceFile  = "COMPOSE_FILE=" + practiceFiles
)

// practiceWait is how long `on` waits for the proxy, as `wait`.
const practiceWait = 3 * time.Minute

// Practice is `lvp game practice on|off`.
func (s *System) Practice(ctx context.Context, argv []string,
	std args.Std) int {
	cmd := s.Prog + " game practice"
	usage := cmd + " on | off"
	fl := args.New(cmd, usage)
	pos, err := fl.Parse(argv)
	if err != nil {
		if errors.Is(err, args.ErrHelp) {
			fmt.Fprint(std.Out, fl.Help()+practiceHelp)
			return 0
		}
		return args.Exit(std, fl, err)
	}
	if len(pos) != 1 || pos[0] != "on" && pos[0] != "off" {
		return usageError(std, cmd, usage, "say on or off")
	}
	dir, err := s.folder()
	if err != nil {
		return fail(std, err.Error())
	}
	d, err := readDotEnv(dir)
	if err != nil {
		return fail(std, err.Error())
	}
	if pos[0] == "on" {
		return s.practiceOn(ctx, std, dir, d)
	}
	return s.practiceOff(ctx, std, dir, d)
}

const practiceHelp = `
on: adds three lines to .env, next to compose.yaml, so that Compose
adds game/compose.game.yaml (practice models, a fake model server),
then restarts the proxy, waits for it and shows docker compose ps.
The first time builds the practice server's image (about 130 MB).
off: removes exactly those lines, and restarts the proxy without
the practice server. See game/README.md.
`

// practiceOn reports whether practice mode is on in d.
func practiceOn(d *dotEnv) bool {
	v, ok := d.get("COMPOSE_FILE")
	return ok && strings.Contains(v, "compose.game.yaml")
}

func (s *System) practiceOn(ctx context.Context, std args.Std,
	dir string, d *dotEnv) int {
	if v, ok := s.Env("COMPOSE_FILE"); ok && v != "" {
		return fail(std, "COMPOSE_FILE is set in this terminal (to "+
			v+"), and it beats .env. Close this terminal and use "+
			"a new one, or clear it, then run this again.")
	}
	if v, ok := d.get("COMPOSE_FILE"); ok && v != practiceFiles {
		return fail(std, "COMPOSE_FILE in .env is set already (to "+
			v+"), and practice mode would replace it, so nothing "+
			"was changed. Add the game's file to it by hand, as "+
			"COMPOSE_FILE="+v+":game/compose.game.yaml with "+
			"COMPOSE_PATH_SEPARATOR=: on its own line, or remove "+
			"that line first (game/README.md).")
	}
	if v, ok := d.get("COMPOSE_PATH_SEPARATOR"); ok && v != ":" {
		return fail(std, "COMPOSE_PATH_SEPARATOR in .env is set "+
			"already (to "+v+"); practice mode needs it to be :, "+
			"so nothing was changed. Remove that line first.")
	}
	added := false
	for _, l := range []string{practiceMark, practiceSep,
		practiceFile} {
		if !d.has(l) {
			d.t.add(l)
			added = true
		}
	}
	if added {
		if err := d.write(); err != nil {
			return fail(std, err.Error())
		}
		say(std.Out, "Practice mode is on in .env: Compose now "+
			"adds game/compose.game.yaml. Restarting the proxy "+
			"with the practice models (the first time, Docker "+
			"builds the practice server first: a minute or two, "+
			"about 130 MB):")
	} else {
		say(std.Out, "Practice mode is on in .env already. "+
			"Restarting the proxy:")
	}
	if err := s.Docker(ctx, dir, std.Out, std.Err, "compose", "up",
		"-d", "--force-recreate", "proxy"); err != nil {
		return fail(std, "docker compose up did not work ("+
			dockerError(err, "")+"). Its own message is above. "+
			dockerHint(dockerError(err, "")))
	}
	if !s.waitUp(ctx, std, practiceWait, "3m") {
		return 1
	}
	if err := s.Docker(ctx, dir, std.Out, std.Err, "compose",
		"ps"); err != nil {
		return fail(std, "docker compose ps did not work: "+
			dockerError(err, ""))
	}
	say(std.Out, "db, proxy and fake-ollama should all be Up. Next: "+
		s.Prog+" game setup")
	return 0
}

// has says whether d holds line exactly.
func (d *dotEnv) has(line string) bool {
	for _, l := range d.t.lines {
		if l == line {
			return true
		}
	}
	return false
}

func (s *System) practiceOff(ctx context.Context, std args.Std,
	dir string, d *dotEnv) int {
	gone := 0
	for i := len(d.t.lines) - 1; i >= 0; i-- {
		switch d.t.lines[i] {
		case practiceMark, practiceSep, practiceFile:
			d.t.drop(i)
			gone++
		}
	}
	if gone == 0 {
		if practiceOn(d) {
			return fail(std, "COMPOSE_FILE in .env names the "+
				"game's file, but not as "+s.Prog+" game "+
				"practice on wrote it, so it was left alone. "+
				"Remove game/compose.game.yaml from that line "+
				"by hand.")
		}
		say(std.Out, "Practice mode is not on in .env; nothing to do.")
		return 0
	}
	if err := d.write(); err != nil {
		return fail(std, err.Error())
	}
	if _, err := os.Stat(filepath.Join(dir, "game",
		"office.db")); err == nil {
		say(std.Out, "Note: the game's office.db is still there. "+
			"When its week is over, "+s.Prog+" game reset revokes "+
			"the game seats and wipes it.")
	}
	say(std.Out, "Practice mode is off in .env. Restarting the proxy "+
		"with your own models:")
	if err := s.Docker(ctx, dir, std.Out, std.Err, "compose", "up",
		"-d", "--force-recreate", "--remove-orphans",
		"proxy"); err != nil {
		return fail(std, "docker compose up did not work ("+
			dockerError(err, "")+"). Its own message is above. "+
			dockerHint(dockerError(err, "")))
	}
	say(std.Out, "Done. Next: "+s.Prog+" wait")
	return 0
}
