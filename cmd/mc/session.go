package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/raff/meshgore"
	"tinygo.org/x/bluetooth"
)

// globalOpts are the options that choose what to connect to and how to behave.
type globalOpts struct {
	name          string // Bluetooth name of the companion
	serial        string // serial port, instead of Bluetooth
	baud          int
	console       bool // the serial port is a repeater or room server's text console
	json          bool
	safe          bool // refuse CLI commands that are not known to be read-only
	verbose       bool
	reconnectWait time.Duration
	cmdTimeout    time.Duration
}

// session is one connection to a node and the settings that go with it. A command
// connects through it on first use, and it is closed once at the end, so a
// command never closes the connection and several commands can share one.
type session struct {
	ctx context.Context
	g   globalOpts
	out io.Writer // where JSON results go; progress text goes to os.Stdout

	link *meshgore.Link
	con  *consoleSource

	bleOnce sync.Once
	bleErr  error
}

func newSession(ctx context.Context, g globalOpts, out io.Writer) *session {
	return &session{ctx: ctx, g: g, out: out}
}

// errUsage is returned for a command the target cannot run.
var errNeedsCompanion = errors.New("this command needs a companion node; the session is on a repeater console (drop -console)")

// companion returns the connection to the companion node, connecting on first
// use. On failure it prints why and returns nil.
func (s *session) companion() *meshgore.Link {
	l, err := s.connectCompanion()
	if err != nil {
		fmt.Println("error:", err)
		return nil
	}
	return l
}

func (s *session) connectCompanion() (*meshgore.Link, error) {
	if s.g.console {
		return nil, errNeedsCompanion
	}
	if s.link != nil {
		return s.link, nil
	}
	var l *meshgore.Link
	var err error
	switch {
	case s.g.serial != "":
		l, err = meshgore.DialSerial(s.g.serial, s.g.baud)
	case s.g.name != "":
		s.bleOnce.Do(func() { s.bleErr = meshgore.Enable() })
		if s.bleErr != nil {
			return nil, fmt.Errorf("failed to enable the Bluetooth adapter: %w", s.bleErr)
		}
		var addr bluetooth.Address
		if addr, err = meshgore.FindCompanion(s.ctx, s.g.name); err == nil {
			l, err = meshgore.Dial(s.ctx, addr)
		}
	default:
		return nil, errors.New("no node chosen: use -name <bluetooth name> or -serial <port> (see \"mc scan\" and \"mc ports\")")
	}
	if err != nil {
		return nil, err
	}
	s.link = l
	printNodeInfo(l)
	return l, nil
}

// console returns the repeater console the session is on, opening it on first use.
func (s *session) console() *consoleSource {
	if !s.g.console {
		fmt.Println("error: this command needs a repeater console; use -serial <port> -console")
		return nil
	}
	if s.con != nil {
		return s.con
	}
	if s.g.serial == "" {
		fmt.Println("error: -console needs -serial <port>")
		return nil
	}
	c, err := meshgore.OpenConsole(s.g.serial, s.g.baud)
	if err != nil {
		fmt.Println("error:", err)
		return nil
	}
	s.con = &consoleSource{c: c, timeout: s.g.cmdTimeout}
	return s.con
}

// node is the name that identifies the companion, for the contact cache.
func (s *session) node() string {
	switch {
	case s.g.name != "":
		return s.g.name
	case s.link != nil:
		return s.link.Self.Name
	}
	return ""
}

func (s *session) close() {
	if s.link != nil {
		s.link.Close()
		s.link = nil
	}
	if s.con != nil {
		s.con.c.Close()
		s.con = nil
	}
}

// stats returns the source of the node's own statistics, whichever the target is.
func (s *session) stats() statsSource {
	if s.g.console {
		if c := s.console(); c != nil {
			return c
		}
		return nil
	}
	if l := s.companion(); l != nil {
		return linkSource{l}
	}
	return nil
}

// thresholds are the alert limits some commands take.
type thresholds struct{ minBattery, maxNoise float64 }

func (t thresholds) limits() meshgore.Thresholds {
	return meshgore.Thresholds{MinBatteryV: t.minBattery, MaxNoiseFloor: int(t.maxNoise)}
}

// splitArgs splits a command line into words, the way a shell would for plain
// cases: spaces separate words, and single or double quotes group them.
func splitArgs(line string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}
