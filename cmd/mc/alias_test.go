package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSplitCommands(t *testing.T) {
	got := splitCommands(`send #a "x; y" ; messages -listen 5s`)
	want := []string{`send #a "x; y" `, ` messages -listen 5s`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandAlias(t *testing.T) {
	for _, tc := range []struct {
		def  string
		args []string
		want [][]string
		err  string
	}{
		{"send #test test", nil, [][]string{{"send", "#test", "test"}}, ""},
		{"send #test", []string{"hi", "there"}, [][]string{{"send", "#test", "hi", "there"}}, ""}, // no placeholder: appended
		{"send -wait 60s -first -match $1 #healthcheck $1", []string{"MHC-1"},
			[][]string{{"send", "-wait", "60s", "-first", "-match", "MHC-1", "#healthcheck", "MHC-1"}}, ""},
		{"send #test $*", []string{"a", "b"}, [][]string{{"send", "#test", "a", "b"}}, ""},
		{"send $1 pre-$2-post", []string{"#c", "x"}, [][]string{{"send", "#c", "pre-x-post"}}, ""},
		{"send $1 $*", []string{"#c", "a", "b"}, [][]string{{"send", "#c", "a", "b"}}, ""},
		{"send #a x; messages", nil, [][]string{{"send", "#a", "x"}, {"messages"}}, ""},
		{`send #a "two words" ; contacts`, nil, [][]string{{"send", "#a", "two words"}, {"contacts"}}, ""},
		{"send -match $node #test hi", nil, [][]string{{"send", "-match", "KO6IYC-SC", "#test", "hi"}}, ""},
		{"send -match $nodename #test $1", []string{"x"}, [][]string{{"send", "-match", "KO6IYC-SC", "#test", "x"}}, ""},
		{"send #test $node", []string{"extra"}, [][]string{{"send", "#test", "KO6IYC-SC", "extra"}}, ""}, // $node is not a placeholder for arguments
		{"send #test $1", []string{"$node"}, [][]string{{"send", "#test", "$node"}}, ""},                 // typed text is not expanded
		{"send #a $1", nil, nil, "needs 1 argument"},
		{"send #a $1", []string{"x", "y"}, nil, "takes 1 argument"},
		{"  ;  ", nil, nil, "empty"},
		{`send "unterminated`, nil, nil, "quote"},
	} {
		got, err := expandAlias("x", tc.def, tc.args, func() (string, error) { return "KO6IYC-SC", nil })
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q %q: error %v, want one with %q", tc.def, tc.args, err, tc.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q %q: got %q, %v; want %q", tc.def, tc.args, got, err, tc.want)
		}
	}
}

func TestExpandAliasNodeUnavailable(t *testing.T) {
	failing := func() (string, error) { return "", errors.New("no connection") }
	if _, err := expandAlias("x", "send -match $node #t hi", nil, failing); err == nil || !strings.Contains(err.Error(), "no connection") {
		t.Errorf("error %v", err)
	}
	called := false
	probe := func() (string, error) { called = true; return "n", nil }
	if _, err := expandAlias("x", "send #t hi", nil, probe); err != nil || called {
		t.Errorf("node looked up without being used: %v, called=%v", err, called)
	}
}

func TestQuoteWordRoundTrip(t *testing.T) {
	for _, words := range [][]string{{"send", "#a", "two words"}, {"a", `it's`}, {"a", `say "hi"`}, {"a", ""}, {"a", "$1", "$*"}} {
		var q []string
		for _, w := range words {
			q = append(q, quoteWord(w))
		}
		back, err := splitArgs(strings.Join(q, " "))
		if err != nil || !reflect.DeepEqual(back, words) {
			t.Errorf("%q -> %q -> %q, %v", words, q, back, err)
		}
	}
}

func TestAliasCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")

	if code := cmdAlias(nil, []string{"test", "=", "send", "-wait", "20s", "-only", "#test", "test"}); code != exitOK {
		t.Fatalf("define: exit %d", code)
	}
	if code := cmdAlias(nil, []string{"hc", `send -wait 60s -first -match $1 #healthcheck $1`}); code != exitOK {
		t.Fatalf("define one word: exit %d", code)
	}
	got := loadAliases()
	if got["test"] != "send -wait 20s -only #test test" || got["hc"] != "send -wait 60s -first -match $1 #healthcheck $1" {
		t.Errorf("saved %q", got)
	}
	for _, bad := range [][]string{
		{"send", "messages"},        // a command
		{"1x", "messages"},          // not a name
		{"nope", "frobnicate"},      // not a command
		{"chain", "messages; test"}, // another alias
		{"empty"},                   // nothing to run
		{"semi", ";"},               // no command
	} {
		if code := cmdAlias(nil, bad); code != exitUsage {
			t.Errorf("alias %q: exit %d, want usage error", bad, code)
		}
	}
	if code := cmdAlias(nil, []string{"-d", "hc"}); code != exitOK {
		t.Errorf("remove: exit %d", code)
	}
	if _, found := loadAliases()["hc"]; found {
		t.Error("hc not removed")
	}
	if code := cmdAlias(nil, []string{"-d", "hc"}); code == exitOK {
		t.Error("removing a missing alias should fail")
	}
}
