package meshgore

import (
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseConsoleReply(t *testing.T) {
	for _, c := range []struct {
		cmd, raw string
		want     []string
	}{
		{"ver", "ver\r\n  -> v1.12.0 (Build: x)\r\n", []string{"v1.12.0 (Build: x)"}},
		{"neighbors", "neighbors\r\n  -> 4a5e:100:20\r\n9c1f:200:8\r\n", []string{"4a5e:100:20", "9c1f:200:8"}},
		{"log", "log\r\nline one\r\nline two\r\n  ->    EOF\r\n", []string{"line one", "line two", "  ->    EOF"}},
		{"reboot", "reboot\r\n", nil},
		{"ver", "  -> v1\r\n", []string{"v1"}}, // no echo
	} {
		if got := parseConsoleReply(c.cmd, []byte(c.raw)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.cmd, got, c.want)
		}
	}
}

// fakeConsole echoes each CR-terminated line the way the firmware does and
// answers a few commands.
func fakeConsole(conn net.Conn) {
	var line []byte
	buf := make([]byte, 64)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		for _, b := range buf[:n] {
			if b != '\r' {
				line = append(line, b)
				conn.Write([]byte{b})
				continue
			}
			conn.Write([]byte("\r\n"))
			switch cmd := string(line); cmd {
			case "ver":
				conn.Write([]byte("  -> v1.12.0\r\n"))
			case "log":
				conn.Write([]byte("a\r\n"))
				time.Sleep(100 * time.Millisecond)
				conn.Write([]byte("b\r\n  ->    EOF\r\n"))
			case "":
				conn.Write([]byte("  -> Unknown command\r\n"))
			}
			line = line[:0]
		}
	}
}

func TestConsoleRun(t *testing.T) {
	app, node := net.Pipe()
	defer node.Close()
	go fakeConsole(node)
	c := newConsole(app)
	defer c.Close()

	got, err := c.Run("ver", 5*time.Second)
	if err != nil || !reflect.DeepEqual(got, []string{"v1.12.0"}) {
		t.Errorf("ver: %q, %v", got, err)
	}
	got, err = c.Run("log", 5*time.Second)
	if err != nil || strings.Join(got, "|") != "a|b|  ->    EOF" {
		t.Errorf("log: %q, %v", got, err)
	}
	start := time.Now()
	got, err = c.Run("quiet", 10*time.Second) // no reply at all
	if err != nil || len(got) != 0 || time.Since(start) < consoleNoReply {
		t.Errorf("quiet: %q, %v after %s", got, err, time.Since(start))
	}
	if _, err := c.Run("a\nb", time.Second); err == nil {
		t.Error("multi-line command accepted")
	}
}
