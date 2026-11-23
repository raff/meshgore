package main

import (
	"context"
	"io"
	"os"
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	for _, c := range []struct {
		line string
		want []string
	}{
		{"status", []string{"status"}},
		{"dm Bob  hello there", []string{"dm", "Bob", "hello", "there"}},
		{`dm "Bob Smith" 'it is "ok"'`, []string{"dm", "Bob Smith", `it is "ok"`}},
		{`send '#test' ""`, []string{"send", "#test", ""}},
		{"  ", nil},
		{"cli get\ttx", []string{"cli", "get", "tx"}},
	} {
		got, err := splitArgs(c.line)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %q, %v; want %q", c.line, got, err, c.want)
		}
	}
	if _, err := splitArgs(`dm "unclosed`); err == nil {
		t.Error("an unterminated quote was accepted")
	}
}

func TestRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commands {
		if c.name == "" || c.summary == "" || c.group == "" || c.run == nil {
			t.Errorf("incomplete command %+v", c)
		}
		if seen[c.name] {
			t.Errorf("command %q is registered twice", c.name)
		}
		seen[c.name] = true
	}
	for _, name := range []string{"status", "watch", "dm", "repeater", "cli", "help", "discover", "channel"} {
		if lookup(name) == nil {
			t.Errorf("no %q command", name)
		}
	}
	if lookup("nope") != nil {
		t.Error("lookup found a command that does not exist")
	}
}

// A session that never connects: these commands must fail on their arguments
// before they try to.
func TestUsageErrorsNeedNoConnection(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	stderr := os.Stderr
	os.Stderr = devnull // the usage text is noise here
	defer func() { os.Stderr = stderr }()

	s := newSession(context.Background(), globalOpts{safe: true}, io.Discard)
	for _, line := range []string{
		"advert bogus", "watch", "watch soon", "watch 0s", "set", "set name", "set colour red",
		"dm", "dm Bob", "send", "send #x", "path", "path a b", "path -attempts 0 x", "contact", "contact add X",
		"channel", "channel bogus x", "channel add", "channel add a b", "radio set", "radio fm",
		"clock later", "gps maybe", "path-hash x", "repeater", "repeater -nope", "cli", "nosuchcommand",
	} {
		args, _ := splitArgs(line)
		if code := runLine(s, args); code != exitUsage {
			t.Errorf("%q: exit %d, want %d", line, code, exitUsage)
		}
	}
	// and a command that needs a node says so instead of crashing
	for _, line := range []string{"status", "info", "channels", "contacts"} {
		args, _ := splitArgs(line)
		if code := runLine(s, args); code != exitFail {
			t.Errorf("%q with no node chosen: exit %d, want %d", line, code, exitFail)
		}
	}
	if s.link != nil || s.con != nil {
		t.Error("a session with no node chosen connected anyway")
	}
}

func TestHelpDescribesEveryCommand(t *testing.T) {
	s := newSession(context.Background(), globalOpts{}, io.Discard)
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer devnull.Close()
	stderr := os.Stderr
	os.Stderr = devnull
	defer func() { os.Stderr = stderr }()
	for _, c := range commands {
		if c.name == "help" {
			continue
		}
		if code := runLine(s, []string{c.name, "-h"}); code != exitOK {
			t.Errorf("%s -h: exit %d", c.name, code)
		}
	}
}
