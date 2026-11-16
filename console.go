package meshgore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"go.bug.st/serial"
)

// A repeater or room server does not speak the companion protocol on its USB
// port. It has a text console: a line ending in CR runs as a command, the node
// echoes what it reads, and the reply follows as "  -> <text>". There is no
// prompt, no password and no reply terminator, so a reply is taken to end when
// the node has been quiet for a moment. Source: examples/simple_repeater/main.cpp.
const (
	consoleQuiet   = 600 * time.Millisecond // silence that ends a reply
	consoleNoReply = 3 * time.Second        // how long to wait for a command that may not answer
	consolePrefix  = "  -> "
)

// Console is a text console on a serial port.
type Console struct {
	path string
	baud int
	port io.ReadWriteCloser
	in   chan []byte
	dead atomic.Bool
}

// OpenConsole opens the port and clears anything the node was printing, such as
// its boot banner.
func OpenConsole(path string, baud int) (*Console, error) {
	if baud == 0 {
		baud = DefaultBaud
	}
	p, err := serial.Open(path, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	_ = p.SetReadTimeout(200 * time.Millisecond)
	_ = p.ResetInputBuffer()
	logf("Opened %s at %d baud", path, baud)
	c := newConsole(p)
	c.path, c.baud = path, baud
	c.settle()
	return c, nil
}

func newConsole(rwc io.ReadWriteCloser) *Console {
	c := &Console{port: rwc, in: make(chan []byte, 256)}
	go func() {
		defer close(c.in)
		buf := make([]byte, 1024)
		for {
			n, err := rwc.Read(buf)
			if n > 0 {
				c.in <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				c.dead.Store(true)
				return
			}
			if n == 0 {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	return c
}

// settle discards what the node is printing, then sends an empty line so a
// half-typed command left in its buffer cannot join the first one.
func (c *Console) settle() {
	c.drain(300 * time.Millisecond)
	_, _ = c.port.Write([]byte("\r"))
	c.drain(400 * time.Millisecond)
}

// drain reads and discards until the port has been quiet for d.
func (c *Console) drain(d time.Duration) {
	for {
		select {
		case _, ok := <-c.in:
			if !ok {
				return
			}
		case <-time.After(d):
			return
		}
	}
}

// Close closes the port.
func (c *Console) Close() error { c.dead.Store(true); return c.port.Close() }

// Alive reports whether the port is still open.
func (c *Console) Alive() bool { return !c.dead.Load() }

// Run sends one command and returns its reply, one string per line, or nil if the
// node printed nothing (a command such as reboot has no reply). timeout bounds
// the whole exchange, which matters for a long dump like "log".
func (c *Console) Run(command string, timeout time.Duration) ([]string, error) {
	if strings.ContainsAny(command, "\r\n") || len(command) > 158 {
		return nil, errors.New("a console command must be a single line of at most 158 characters")
	}
	if _, err := c.port.Write([]byte(command + "\r")); err != nil {
		c.dead.Store(true)
		return nil, fmt.Errorf("serial write: %w", err)
	}
	var raw []byte
	var echoEnd, last time.Time // when the echoed line ended, and when data last arrived
	deadline := time.After(timeout)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case b, ok := <-c.in:
			if !ok {
				return parseConsoleReply(command, raw), nil // port closed: it was a reboot
			}
			raw = append(raw, b...)
			last = time.Now()
			if echoEnd.IsZero() && strings.Contains(string(raw), "\n") {
				echoEnd = last
			}
		case now := <-tick.C:
			switch {
			case echoEnd.IsZero():
				// the echo has not finished: keep waiting
			case last.After(echoEnd) && now.Sub(last) > consoleQuiet:
				return parseConsoleReply(command, raw), nil
			case !last.After(echoEnd) && now.Sub(echoEnd) > consoleNoReply:
				return parseConsoleReply(command, raw), nil
			}
		case <-deadline:
			if len(raw) == 0 {
				return nil, fmt.Errorf("no reply to %q within %s", command, timeout)
			}
			return parseConsoleReply(command, raw), nil
		}
	}
}

// parseConsoleReply removes the echo of the command and the "  -> " prefix.
func parseConsoleReply(command string, raw []byte) []string {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "")
	lines := strings.Split(text, "\n")
	// The echo is the first line, and the node may echo a bare line too.
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == strings.TrimSpace(command) {
		lines = lines[1:]
	}
	var out []string
	for i, l := range lines {
		if i == 0 {
			l = strings.TrimPrefix(l, consolePrefix)
		}
		out = append(out, l)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Reopen waits for the port to come back after a reboot and returns a new
// console on it, trying until ctx is cancelled. The old console is closed.
func (c *Console) Reopen(ctx context.Context) (*Console, error) {
	_ = c.Close()
	for {
		n, err := OpenConsole(c.path, c.baud)
		if err == nil {
			// OpenConsole settled before the node finished booting; wait out its banner
			n.settle()
			logf("Reconnected")
			return n, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
