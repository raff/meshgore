// Command mc talks to a MeshCore node: a companion over Bluetooth or USB serial, or
// a repeater through a companion or on its USB console.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/raff/meshgore"
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage: mc [options] <command> [arguments]
       mc [options]            (with -name or -serial: an interactive session)

Options choose the node and how to behave:
`)
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\nCommands:")
	printCommands(os.Stderr)
}

func main() {
	var g globalOpts
	flag.StringVar(&g.name, "name", "", "the companion's Bluetooth name: what follows the first '-' (MeshCore-MyNode is MyNode), or the full name")
	flag.StringVar(&g.serial, "serial", "", "connect over USB serial instead: the port, e.g. /dev/cu.usbmodem14301 (see \"mc ports\")")
	flag.IntVar(&g.baud, "baud", meshgore.DefaultBaud, "with -serial: baud rate (a native USB port ignores it)")
	flag.BoolVar(&g.console, "console", false, "with -serial: the port is a repeater or room server's text console, not a companion")
	flag.BoolVar(&g.json, "json", false, "print the result as JSON on stdout; progress goes to stderr")
	flag.BoolVar(&g.safe, "safe", true, "only run CLI commands known to be read-only; -safe=false allows anything")
	flag.BoolVar(&g.verbose, "v", false, "log every raw protocol frame")
	flag.DurationVar(&g.reconnectWait, "reconnect-wait", 2*time.Minute, "after a reboot, how long to wait for the node to come back (0 = don't)")
	flag.DurationVar(&g.cmdTimeout, "cmd-timeout", 30*time.Second, "how long to wait for each CLI command's reply")
	flag.IntVar(&meshgore.DialAttempts, "retries", meshgore.DialAttempts, "how many times to try the Bluetooth connection before giving up")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 && (g.name != "" || g.serial != "") {
		args = []string{"repl"} // a node and no command: an interactive session
	}
	if len(args) == 0 {
		usage()
		os.Exit(exitUsage)
	}
	if lookup(args[0]) == nil {
		fmt.Fprintf(os.Stderr, "mc: unknown command %q\n\n", args[0])
		usage()
		os.Exit(exitUsage)
	}

	// In JSON mode stdout is reserved for the result, so progress text (which is
	// printed with fmt.Print*) is redirected to stderr.
	realStdout := os.Stdout
	if g.json {
		os.Stdout = os.Stderr
	}
	// Ctrl-C ends a one-shot command. In a session it stops the running command instead,
	// which the session handles itself, so only a termination signal ends that one.
	sigs := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if args[0] == "repl" {
		sigs = []os.Signal{syscall.SIGTERM}
	}
	ctx, stop := signal.NotifyContext(context.Background(), sigs...)
	defer stop()

	meshgore.Debug = g.verbose
	meshgore.Logf = func(format string, a ...any) { fmt.Printf(format+"\n", a...) }

	s := newSession(ctx, g, realStdout)
	code := runLine(s, args)
	s.close()
	os.Exit(code)
}
