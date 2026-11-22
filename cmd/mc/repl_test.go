package main

import (
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestCompleteLine(t *testing.T) {
	for _, c := range []struct {
		line string
		want []string
	}{
		{"stat", []string{"status"}},
		{"gps ", []string{"gps off", "gps on"}},
		{"gps o", []string{"gps off", "gps on"}},
		{"gps of", []string{"gps off"}},
		{"channel re", []string{"channel remove"}},
		{"help adv", []string{"help advert", "help adverts"}},
		{"auto-add rep", []string{"auto-add repeater"}},
		{"  adv", []string{"  advert", "  adverts"}}, // leading spaces stay
		{"safe ", []string{"safe off", "safe on"}},
		{"nosuchword", nil},
		{"clock sync ", nil}, // nothing more to complete
		{"STAT", []string{"status"}},
	} {
		got := completeLine(c.line, "")
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.line, got, c.want)
		}
	}
	// with nothing typed, every command is offered, plus the session's own words
	all := completeLine("", "")
	for _, want := range []string{"status", "dm", "repeater", "exit", "safe", "help"} {
		found := false
		for _, g := range all {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("an empty line does not offer %q", want)
		}
	}
}

func newTestREPL() *repl {
	s := newSession(context.Background(), globalOpts{safe: true}, io.Discard)
	return &repl{s: s, base: context.Background()}
}

func TestREPLBuiltins(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()

	r := newTestREPL()
	for _, line := range []string{"exit", "quit"} {
		if !r.exec(line) {
			t.Errorf("%q did not leave", line)
		}
	}
	for _, line := range []string{"", "   ", "help", "nosuchcommand", `dm "unterminated`, "repl", "safe maybe", "advert bogus"} {
		if r.exec(line) {
			t.Errorf("%q left the session", line)
		}
	}
	if !r.s.g.safe {
		t.Error("safe mode changed")
	}
	r.exec("safe off")
	if r.s.g.safe {
		t.Error("safe off did not turn it off")
	}
	r.exec("safe on")
	if !r.s.g.safe {
		t.Error("safe on did not turn it on")
	}
	if r.busy || r.cancel != nil {
		t.Error("a finished command left the session busy")
	}
}

func TestEnterRepeaterLeavesOtherFormsToTheCommand(t *testing.T) {
	r := newTestREPL()
	for _, args := range [][]string{
		nil,                               // no name
		{"-c", "ver", "Name"},             // options the session mode does not take
		{"Name", "status"},                // an action after the name
		{"-timeout=soon", "Name"},         // not a duration
		{"-password", "x", "Name", "cmd"}, // an action again
	} {
		if handled, _ := r.enterRepeater(args); handled {
			t.Errorf("%q was handled as a session", args)
		}
	}
	r.s.g.console = true
	if handled, _ := r.enterRepeater([]string{"Name"}); handled {
		t.Error("a console session tried to log in to a repeater")
	}
}

func TestREPLOptions(t *testing.T) {
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	defer devnull.Close()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()

	// the options are checked before anything connects
	for name, g := range map[string]globalOpts{
		"-repeater on a console": {console: true, serial: "/dev/none"},
	} {
		s := newSession(context.Background(), g, io.Discard)
		if code := cmdREPL(s, []string{"-repeater", "Name"}); code != exitUsage {
			t.Errorf("%s: exit %d, want %d", name, code, exitUsage)
		}
	}
	s := newSession(context.Background(), globalOpts{}, io.Discard)
	for _, args := range [][]string{{"extra"}, {"-nope"}, {"-timeout", "soon"}} {
		if code := cmdREPL(s, args); code != exitUsage {
			t.Errorf("repl %q: exit %d, want %d", args, code, exitUsage)
		}
	}
}

func TestBareRepeaterBecomesREPL(t *testing.T) {
	for _, tc := range []struct{ in, want []string }{
		{[]string{"repeater", "Rep"}, []string{"repl", "-repeater", "Rep"}},
		{[]string{"repeater", "-password", "pw", "Rep"}, []string{"repl", "-password=pw", "-repeater", "Rep"}},
		{[]string{"repeater", "Rep", "status"}, nil},
		{[]string{"repeater", "-c", "ver", "Rep"}, nil},
		{[]string{"status"}, nil},
	} {
		want := tc.want
		if want == nil {
			want = tc.in
		}
		if got := bareRepeaterToREPL(tc.in); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%v: got %v, want %v", tc.in, got, want)
		}
	}
}

func TestCompleteRepeaterLine(t *testing.T) {
	for _, c := range []struct {
		line string
		want []string
	}{
		{"sta", []string{"start", "status"}},
		{"nei", []string{"neighbor.remove", "neighbors"}},
		{"get r", []string{"get radio", "get repeat", "get role"}},
		{"clock ", []string{"clock sync"}},
		{"help rebo", []string{"help reboot"}},
		{"safe o", []string{"safe off", "safe on"}},
		{"get radio x", nil},
	} {
		if got := completeRepeaterLine(c.line); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.line, got, c.want)
		}
	}
}
