package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"os/signal"

	"github.com/peterh/liner"
	"github.com/raff/meshgore"
)

func init() {
	register(&command{"repl", "Connection", "[-repeater NAME [-password P]]", "an interactive session: run commands one after another on one connection", cmdREPL})
}

// A repl runs commands read from a prompt against one session, so the connection
// (and a repeater login) is made once. While it waits at the prompt, new messages
// are printed as they arrive.
type repl struct {
	s    *session
	line *liner.State
	base context.Context

	mu       sync.Mutex // guards what follows, and serialises output with the prompt
	atPrompt bool
	prompt   string
	busy     bool // a command is running
	missed   bool // a message push arrived while it ran
	cancel   context.CancelFunc

	rep *repeaterMode // logged in to a repeater: lines go to it
}

// repeaterMode is a session logged in to a repeater.
type repeaterMode struct {
	ct      *meshgore.Contact
	timeout time.Duration
}

func cmdREPL(s *session, args []string) int {
	fs := newFlags("repl", "[-repeater NAME [-password P] [-timeout D]]")
	repeater := fs.String("repeater", os.Getenv("MC_REPEATER"), "log in to this repeater (through the companion) as soon as the session starts (or MC_REPEATER)")
	password := fs.String("password", "", "the repeater's admin password (or MC_PASSWORD; an empty one is a password too)")
	timeout := fs.Duration("timeout", s.g.cmdTimeout, "with -repeater: how long to wait for each command's reply")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) > 0 {
		return usageError(fs, "takes no arguments, only options")
	}
	if *repeater != "" && s.g.console {
		return usageError(fs, "-repeater needs a companion node, but the session is on a repeater console")
	}
	passwordGiven := false
	fs.Visit(func(f *flag.Flag) { passwordGiven = passwordGiven || f.Name == "password" })
	// Connect now, so the first prompt appears on a working connection and live
	// messages can start.
	if s.g.console {
		if s.console() == nil {
			return exitFail
		}
	} else {
		// Until the prompt is up Ctrl-C has nothing else to stop: let it end the
		// scan and the connection attempts.
		base := s.ctx
		var stop context.CancelFunc
		s.ctx, stop = signal.NotifyContext(base, os.Interrupt)
		l := s.companion()
		stop()
		s.ctx = base
		if l == nil {
			return exitFail
		}
	}

	r := &repl{s: s, base: s.ctx}
	r.line = liner.NewLiner()
	defer r.line.Close()
	r.line.SetCtrlCAborts(true)
	r.line.SetCompleter(r.complete)
	hist := historyPath()
	if hist != "" {
		if f, err := os.Open(hist); err == nil {
			r.line.ReadHistory(f)
			f.Close()
		}
		defer r.saveHistory(hist)
	}

	// Ctrl-C stops the command that is running. At the prompt the terminal is in raw
	// mode, so liner sees it as a keystroke instead.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt)
	defer signal.Stop(sigc)
	go func() {
		for range sigc {
			r.mu.Lock()
			if r.cancel != nil {
				r.cancel()
			}
			r.mu.Unlock()
		}
	}()

	ctx, stop := context.WithCancel(r.base)
	defer stop()
	if !s.g.console {
		go r.background(ctx)
	}

	if *repeater != "" {
		// asked for on the command line, so a failure to log in ends the session
		a := []string{"-timeout=" + timeout.String(), *repeater}
		if passwordGiven {
			a = append([]string{"-password=" + *password}, a...)
		}
		if _, ok := r.enterRepeater(a); !ok {
			return exitFail
		}
	}
	fmt.Println("Type help for the commands, exit to leave.")
	for {
		r.setPrompt()
		r.mu.Lock()
		r.atPrompt = true
		r.mu.Unlock()
		text, err := r.line.Prompt(r.prompt)
		r.mu.Lock()
		r.atPrompt = false
		r.mu.Unlock()
		switch err {
		case nil:
		case liner.ErrPromptAborted:
			continue
		case io.EOF:
			fmt.Println()
			r.leaveRepeater()
			return exitOK
		default:
			fmt.Println("error:", err)
			return exitFail
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		r.line.AppendHistory(text)
		if r.exec(text) {
			r.leaveRepeater()
			return exitOK
		}
	}
}

func historyPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "mc", "history")
}

func (r *repl) saveHistory(path string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if f, err := os.Create(path); err == nil {
		r.line.WriteHistory(f)
		f.Close()
	}
}

// setPrompt names what lines go to: the repeater, the console, or the companion.
func (r *repl) setPrompt() {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.rep != nil:
		r.prompt = r.rep.ct.Name + "# "
	case r.s.g.console:
		r.prompt = "mc console> "
	default:
		unsafe := ""
		if !r.s.g.safe {
			unsafe = " (unsafe)"
		}
		r.prompt = "mc " + r.s.node() + unsafe + "> "
	}
}

// say prints a line of output that arrives on its own (a message, a notice). At the
// prompt it clears the line first and draws the prompt again, so it lands above it.
// What was being typed is still in the editor, and shows again with the next key.
func (r *repl) say(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.atPrompt {
		fmt.Print("\r\x1b[2K")
	}
	fmt.Println(text)
	if r.atPrompt {
		fmt.Print(r.prompt)
	}
}

// background prints new messages as they arrive, and reconnects if the link drops.
// Messages are left alone while a command runs or a repeater session is open,
// because those read replies from the same queue.
func (r *repl) background(ctx context.Context) {
	l := r.s.link
	sub := l.Subscribe(meshgore.PushMsgWaiting)
	defer sub.Close()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-sub.C:
			r.mu.Lock()
			hold := r.busy || r.rep != nil
			if hold {
				r.missed = true
			}
			r.mu.Unlock()
			if !hold {
				r.showMessages()
			}
		case <-tick.C:
			r.mu.Lock()
			busy := r.busy
			r.mu.Unlock()
			if busy || l.Alive() {
				continue
			}
			r.say("(connection lost; reconnecting...)")
			if err := l.Reconnect(ctx); err != nil {
				return
			}
			r.say("(reconnected)")
			r.showMessages()
		case <-ctx.Done():
			return
		}
	}
}

func (r *repl) showMessages() {
	messagePrinter(r.s, r.s.link, r.say)()
}

// catchUp shows messages that arrived while a command ran.
func (r *repl) catchUp() {
	r.mu.Lock()
	missed := r.missed && r.rep == nil
	if missed {
		r.missed = false
	}
	r.mu.Unlock()
	if missed && !r.s.g.console {
		r.showMessages()
	}
}

// exec runs one line and reports whether to leave.
func (r *repl) exec(text string) (quit bool) {
	args, err := splitArgs(text)
	if err != nil {
		fmt.Println("error:", err)
		return false
	}
	if len(args) == 0 {
		return false
	}
	if r.rep != nil {
		return r.execRepeater(args, text)
	}
	switch args[0] {
	case "exit", "quit":
		return true
	case "safe":
		r.safe(args[1:])
		return false
	case "verbose":
		r.verbose(args[1:])
		return false
	case "repl":
		fmt.Println("already in a session")
		return false
	case "repeater":
		if handled, _ := r.enterRepeater(args[1:]); handled {
			return false
		}
	}

	ctx, cancel := context.WithCancel(r.base)
	r.mu.Lock()
	r.cancel, r.busy = cancel, true
	r.mu.Unlock()
	r.s.ctx = ctx
	code := runLine(r.s, args)
	cancel()
	r.mu.Lock()
	r.cancel, r.busy = nil, false
	r.mu.Unlock()
	r.s.ctx = r.base

	if args[0] == "help" && len(args) == 1 {
		fmt.Println("In this session: exit | safe [on|off] | repeater NAME (log in and type its commands)")
	}
	if code != exitOK {
		fmt.Printf("[exit %d]\n", code)
	}
	r.catchUp()
	return false
}

// safe shows or sets the read-only check for CLI commands.
func (r *repl) safe(args []string) {
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "on":
		r.s.g.safe = true
	case len(args) == 1 && args[0] == "off":
		r.s.g.safe = false
	default:
		fmt.Println("usage: safe [on|off]")
		return
	}
	fmt.Printf("safe mode is %s\n", map[bool]string{true: "on: only read-only CLI commands run", false: "off: any CLI command runs"}[r.s.g.safe])
}

// verbose shows or sets the logging of every raw protocol frame (the -v option).
func (r *repl) verbose(args []string) {
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "on":
		meshgore.Debug = true
	case len(args) == 1 && args[0] == "off":
		meshgore.Debug = false
	default:
		fmt.Println("usage: verbose [on|off]")
		return
	}
	fmt.Printf("verbose is %s\n", map[bool]string{true: "on: every raw frame is logged", false: "off"}[meshgore.Debug])
}

// enterRepeater handles "repeater NAME" typed with nothing after the name: it logs in
// and the following lines go to the repeater until "exit". handled says whether the
// line was one it takes (anything with more to it is left to the repeater command), and
// ok whether the login worked.
func (r *repl) enterRepeater(args []string) (handled, ok bool) {
	var name, password string
	havePassword := false // given on the line, even if empty
	timeout := r.s.g.cmdTimeout
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-password" && i+1 < len(args):
			i++
			password, havePassword = args[i], true
		case strings.HasPrefix(a, "-password="):
			password, havePassword = strings.TrimPrefix(a, "-password="), true
		case strings.HasPrefix(a, "-timeout="):
			d, err := time.ParseDuration(strings.TrimPrefix(a, "-timeout="))
			if err != nil {
				return false, false
			}
			timeout = d
		case strings.HasPrefix(a, "-"), name != "":
			return false, false // other options, or actions after the name: the command handles it
		default:
			name = a
		}
	}
	if name == "" || r.s.g.console {
		return false, false
	}
	l := r.s.link
	ct, err := resolveContact(l.Client, r.s.node(), name)
	if err != nil {
		fmt.Println("error:", err)
		return true, false
	}
	fmt.Printf("Repeater: %s (%s, key %x)\n", ct.Name, ct.TypeName(), ct.PublicKey[:6])
	if !havePassword {
		if password = os.Getenv("MC_PASSWORD"); password == "" {
			if password, err = r.line.PasswordPrompt("password (empty for none): "); err != nil {
				return true, false
			}
		}
	}
	// Ctrl-C stops the login, as it stops a command.
	ctx, cancel := context.WithCancel(r.base)
	r.mu.Lock()
	r.cancel, r.busy = cancel, true
	r.mu.Unlock()
	err = l.Client.Login(ctx, ct.PublicKey, password)
	cancel()
	r.mu.Lock()
	r.cancel, r.busy = nil, false
	r.mu.Unlock()
	if err != nil {
		fmt.Println("error:", err)
		return true, false
	}
	fmt.Println("Logged in. Type its commands; status, telemetry, safe, exit.")
	r.mu.Lock()
	r.rep = &repeaterMode{ct: ct, timeout: timeout}
	r.mu.Unlock()
	return true, true
}

// execRepeater runs a line typed inside a repeater session.
func (r *repl) execRepeater(args []string, text string) bool {
	rep, client := r.rep, r.s.link.Client
	switch args[0] {
	case "exit", "quit", "logout":
		r.leaveRepeater()
		return false
	case "safe":
		r.safe(args[1:])
		return false
	case "verbose":
		r.verbose(args[1:])
		return false
	case "status":
		st, err := client.RequestStatus(rep.ct.PublicKey)
		if err != nil {
			fmt.Println("error:", err)
			return false
		}
		fmt.Print(st)
		return false
	case "telemetry":
		rs, err := client.RequestTelemetry(rep.ct.PublicKey)
		if err != nil {
			fmt.Println("error:", err)
			return false
		}
		for _, x := range rs {
			fmt.Printf("  %-12s %s\n", x.Name, x.Value)
		}
		return false
	}
	if r.s.g.safe && !meshgore.IsReadOnlyCommand(text) {
		fmt.Printf("error: %q is not a known read-only command; type \"safe off\" to allow it (see CLI.md)\n", text)
		return false
	}
	if err := client.SendCLI(rep.ct.PublicKey, text); err != nil {
		fmt.Println("error:", err)
		return false
	}
	replies := client.CollectReplies([6]byte(rep.ct.PublicKey[:6]), rep.timeout, 3*time.Second, func(m *meshgore.Message) {
		fmt.Printf("(other message: %q)\n", m.Text)
	})
	if len(replies) == 0 {
		fmt.Printf("error: no reply to %q within %s\n", text, rep.timeout)
	}
	for _, line := range replies {
		fmt.Println(line)
	}
	return false
}

// leaveRepeater logs out of the repeater, if logged in.
func (r *repl) leaveRepeater() {
	r.mu.Lock()
	rep := r.rep
	r.rep = nil
	r.mu.Unlock()
	if rep == nil {
		return
	}
	if l := r.s.link; l != nil && l.Alive() {
		l.Client.Logout(rep.ct.PublicKey)
	}
	fmt.Println("Logged out.")
	r.catchUp()
}

// Completion: command names, then the usual words after some of them, then the
// contacts and channels the command takes.
var subWords = map[string][]string{
	"channel":  {"add", "remove", "list"},
	"contact":  {"remove"},
	"set":      {"name", "position", "share-location", "multi-acks"},
	"clock":    {"sync"},
	"radio":    {"set"},
	"gps":      {"on", "off"},
	"advert":   {"flood", "zerohop"},
	"auto-add": {"show", "all", "none", "chat", "repeater", "room", "sensor"},
	"discover": {"all", "chat", "repeater", "room", "sensor"},
	"safe":     {"on", "off"},
	"verbose":  {"on", "off"},
	"help":     nil, // filled with the command names below
}

// complete returns the lines that complete what is typed, for the line editor.
func (r *repl) complete(line string) []string {
	r.mu.Lock()
	inRepeater := r.rep != nil
	r.mu.Unlock()
	if inRepeater {
		return nil
	}
	return completeLine(line, r.s.node())
}

func completeLine(line, node string) []string {
	trimmed := strings.TrimLeft(line, " ")
	words := strings.Fields(trimmed)
	endsSpace := strings.HasSuffix(line, " ") || len(words) == 0
	var head, typed string // what stays, and the word being completed
	var cmd string
	index := len(words) // which argument is being completed: 0 is the command, 1 its first argument
	if endsSpace {
		head, typed = line, ""
		if len(words) > 0 {
			cmd = words[0]
		}
	} else {
		head = line[:len(line)-len(words[len(words)-1])]
		typed = words[len(words)-1]
		index = len(words) - 1
		if len(words) > 1 {
			cmd = words[0]
		}
	}

	var cands []string
	switch {
	case index == 0:
		for _, c := range commands {
			cands = append(cands, c.name)
		}
		cands = append(cands, "exit", "safe", "verbose")
	case index != 1:
		// only the first word after the command has a fixed set of choices
	case cmd == "help":
		for _, c := range commands {
			cands = append(cands, c.name)
		}
	case cmd == "dm" || cmd == "repeater" || cmd == "contact":
		if cached, _ := loadContactCache(node); cached != nil {
			for _, ct := range cached {
				if strings.ContainsAny(ct.Name, " \t") {
					cands = append(cands, `"`+ct.Name+`"`)
				} else {
					cands = append(cands, ct.Name)
				}
			}
		}
		cands = append(cands, subWords[cmd]...)
	default:
		cands = subWords[cmd]
	}

	var out []string
	for _, c := range cands {
		if strings.HasPrefix(strings.ToLower(c), strings.ToLower(typed)) {
			out = append(out, head+c)
		}
	}
	sort.Strings(out)
	return out
}
