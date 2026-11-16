package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// A command is one thing mc can do. The command line runs one; a REPL will run
// many against the same session, so a command never exits the process, never
// closes the connection, and reports its outcome as an exit code.
type command struct {
	name    string
	group   string
	usage   string // the arguments, for help
	summary string
	run     func(s *session, args []string) int
}

// Exit codes: 0 ok, 1 failure, 2 a threshold was crossed, 64 wrong usage.
const exitUsage = 64

var commands []*command

func register(cs ...*command) { commands = append(commands, cs...) }

func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

// runLine runs one command line (words already split).
func runLine(s *session, args []string) int {
	if len(args) == 0 {
		return exitOK
	}
	c := lookup(args[0])
	if c == nil {
		fmt.Fprintf(os.Stderr, "mc: unknown command %q (try \"mc help\")\n", args[0])
		return exitUsage
	}
	return c.run(s, args[1:])
}

// newFlags makes the flag set of a command. Its usage line is what "-h" and
// "mc help <command>" print.
func newFlags(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		c := lookup(name)
		fmt.Fprintf(os.Stderr, "usage: mc %s %s\n", name, usage)
		if c != nil {
			fmt.Fprintf(os.Stderr, "  %s\n", c.summary)
		}
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags parses args. ok is false when the command should stop and return
// code now: the flags were wrong, or help was asked for. Flags go before the
// other arguments.
func parseFlags(fs *flag.FlagSet, args []string) (rest []string, code int, ok bool) {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil, exitOK, false
		}
		return nil, exitUsage, false
	}
	return fs.Args(), exitOK, true
}

// usageError reports wrong arguments and returns the usage exit code.
func usageError(fs *flag.FlagSet, format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "mc %s: %s\n", fs.Name(), fmt.Sprintf(format, a...))
	fs.Usage()
	return exitUsage
}

// cmdHelp lists the commands, or describes one.
func cmdHelp(s *session, args []string) int {
	if len(args) > 0 {
		c := lookup(args[0])
		if c == nil {
			fmt.Fprintf(os.Stderr, "mc: unknown command %q\n", args[0])
			return exitUsage
		}
		c.run(s, []string{"-h"})
		return exitOK
	}
	printCommands(os.Stdout)
	return exitOK
}

// groupOrder is the order help lists the groups in; a group not named here
// comes after them.
var groupOrder = []string{"Node", "Messages", "Contacts and channels", "Network", "Connection"}

func printCommands(w *os.File) {
	groups := map[string][]*command{}
	for _, c := range commands {
		groups[c.group] = append(groups[c.group], c)
	}
	order := append([]string(nil), groupOrder...)
	for g := range groups {
		if !contains(order, g) {
			order = append(order, g)
		}
	}
	for _, g := range order {
		cs := groups[g]
		if len(cs) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s:\n", g)
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].name < cs[j].name })
		for _, c := range cs {
			head := strings.TrimSpace(c.name + " " + c.usage)
			if len(head) > 34 { // too long to line up: the summary goes on its own line
				fmt.Fprintf(w, "  %s\n  %-34s %s\n", head, "", c.summary)
			} else {
				fmt.Fprintf(w, "  %-34s %s\n", head, c.summary)
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Run \"mc help <command>\" for its options.")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
