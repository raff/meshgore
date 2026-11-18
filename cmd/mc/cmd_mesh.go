package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/raff/meshgore"
)

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ", ") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func init() {
	register(
		&command{"messages", "Messages", "[-listen DURATION]", "print waiting messages, and with -listen the new ones as they arrive", cmdMessages},
		&command{"dm", "Messages", "CONTACT TEXT...", "send a direct message and wait for the acknowledgement", cmdDM},
		&command{"send", "Messages", "CHANNEL TEXT...", "send a message on a channel (name or slot number)", cmdSend},
		&command{"healthcheck", "Messages", "[-wait DURATION] CODE", "send CODE on #healthcheck; with -wait, wait for it to be echoed", cmdHealthcheck},
		&command{"contacts", "Contacts and channels", "[-sort name|advert|type] [-max-age AGE] [-match TEXT] [-full] [TYPES]", "list the contacts: filter by type, name or age, sort them", cmdContacts},
		&command{"contact", "Contacts and channels", "remove NAME", "remove a contact from the node", cmdContact},
		&command{"channels", "Contacts and channels", "", "list the channels", cmdChannels},
		&command{"channel", "Contacts and channels", "add NAME [-secret HEX|random] [-slot N] [-overwrite] | remove NAME|SLOT [-overwrite]", "add or empty a channel", cmdChannel},
		&command{"discover", "Network", "[-wait DURATION] [all|chat,repeater,room,sensor]", "ask nodes in direct radio range to identify themselves", cmdDiscover},
		&command{"path", "Network", "CONTACT", "find the route (the repeaters) to a contact and back", cmdPath},
		&command{"repeater", "Network", "[-password P] [-c CMD]... NAME [status] [telemetry] [watch INTERVAL] [cmd TEXT...]", "log in to a repeater through the companion and run things on it", cmdRepeater},
		&command{"cli", "Network", "[-c CMD]... [TEXT...]", "run a CLI command on the node itself (the companion, or the repeater console)", cmdCLI},
		&command{"scan", "Connection", "", "list the companions in Bluetooth range", cmdScan},
		&command{"ports", "Connection", "", "list the serial ports", cmdPorts},
		&command{"help", "Connection", "[COMMAND]", "list the commands, or describe one", cmdHelp},
	)
}

// messagePrinter returns a function that fetches the waiting messages, prints them
// (through emit, or to stdout if emit is nil) and returns them. Names come from the
// channel list and the contact cache.
func messagePrinter(s *session, l *meshgore.Link, emit func(string)) func() []*meshgore.Message {
	if emit == nil {
		emit = func(line string) { fmt.Println(line) }
	}
	names := map[int]string{} // channel names, looked up the first time each is needed
	channelName := func(idx int) string {
		if n, ok := names[idx]; ok {
			return n
		}
		n := ""
		if ch, err := l.Client.GetChannel(idx); err == nil {
			n = ch.Name
		}
		names[idx] = n
		return n
	}
	contactNames := map[[6]byte]string{}
	cached, _ := loadContactCache(s.node())
	for _, ct := range cached {
		contactNames[[6]byte(ct.PublicKey[:6])] = ct.Name
	}
	return func() []*meshgore.Message {
		msgs, err := l.Client.SyncMessages() // l.Client changes on reconnect
		for _, msg := range msgs {
			from := fmt.Sprintf("%x", msg.From)
			if msg.Channel {
				from = channelName(msg.ChannelIdx)
				if from == "" {
					from = fmt.Sprintf("channel %d", msg.ChannelIdx)
				}
			} else if n := contactNames[msg.From]; n != "" {
				from = n
			}
			emit(fmt.Sprintf("[%s] %s: %s", msg.Time.Format(time.DateTime), from, msg.Text))
		}
		if err != nil {
			emit("message sync: " + err.Error())
		}
		return msgs
	}
}

func cmdMessages(s *session, args []string) int {
	fs := newFlags("messages", "[-listen DURATION]")
	listen := fs.Duration("listen", 0, "after printing what is waiting, keep listening this long for new messages")
	if _, code, ok := parseFlags(fs, args); !ok {
		return code
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	// The subscription outlives a reconnect, so it is made once, before the first sync.
	waiting := l.Subscribe(meshgore.PushMsgWaiting)
	defer waiting.Close()
	printMessages := messagePrinter(s, l, nil)
	printMessages()
	if *listen == 0 {
		return exitOK
	}
	fmt.Printf("Listening for %s...\n", *listen)
	timeout := time.After(*listen)
	check := time.NewTicker(5 * time.Second) // detect a dropped connection
	defer check.Stop()
	for {
		select {
		case <-waiting.C:
			printMessages()
		case <-check.C:
			if l.Alive() {
				continue
			}
			fmt.Println("connection to the companion lost; reconnecting...")
			if err := l.Reconnect(s.ctx); err != nil {
				return exitOK
			}
			printMessages() // catch up on anything missed meanwhile
		case <-timeout:
			return exitOK
		case <-s.ctx.Done():
			return exitOK
		}
	}
}

func cmdDM(s *session, args []string) int {
	fs := newFlags("dm", "CONTACT TEXT...")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) < 2 {
		return usageError(fs, "wants a contact and the text")
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	return runDM(s.ctx, l, s.node(), rest[0], strings.Join(rest[1:], " "), s.g.json, s.out)
}

// findChannel resolves a channel given as a slot number or a name.
func findChannel(l *meshgore.Link, spec string) (*meshgore.Channel, error) {
	chans, err := l.Client.Channels()
	if err != nil {
		return nil, err
	}
	n, numeric := strconv.Atoi(spec)
	for _, ch := range chans {
		if (numeric == nil && ch.Index == n) || strings.EqualFold(ch.Name, spec) {
			return ch, nil
		}
	}
	return nil, fmt.Errorf("no channel %q (see \"mc channels\")", spec)
}

func cmdSend(s *session, args []string) int {
	fs := newFlags("send", "CHANNEL TEXT...")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) < 2 {
		return usageError(fs, "wants a channel and the text")
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	ch, err := findChannel(l, rest[0])
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	if _, _, err := l.Client.SendChannelMessage(ch.Index, strings.Join(rest[1:], " ")); err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	fmt.Printf("Sent on %s [%d]\n", ch.Name, ch.Index)
	return exitOK
}

func cmdHealthcheck(s *session, args []string) int {
	fs := newFlags("healthcheck", "[-wait DURATION] CODE")
	wait := fs.Duration("wait", 0, "after sending, wait this long for the code to appear on #healthcheck; fail if it does not")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		return usageError(fs, "wants one code")
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	chans, err := l.Client.Channels()
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	return runHealthcheck(l, chans, rest[0], *wait, messagePrinter(s, l, nil))
}

func cmdContacts(s *session, args []string) int {
	fs := newFlags("contacts", "[-full] [-sort name|advert|type] [-max-age AGE] [-match TEXT] [TYPES]")
	full := fs.Bool("full", false, "discard the cache and download every contact again")
	sortBy := fs.String("sort", "name", "order: name, advert (most recently heard first) or type")
	maxAge := fs.String("max-age", "", "hide contacts not heard from for longer than this, e.g. 36h or 7d (the times are the nodes' own clocks)")
	match := fs.String("match", "", "show only names containing this text")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	view := contactView{match: *match, sortBy: *sortBy}
	switch *sortBy {
	case "name", "advert", "type":
	default:
		return usageError(fs, "-sort must be name, advert or type, not %q", *sortBy)
	}
	if *maxAge != "" {
		var err error
		if view.maxAge, err = parseAge(*maxAge); err != nil {
			return usageError(fs, "-max-age: %v", err)
		}
	}
	switch len(rest) {
	case 0:
	case 1: // a list of node types: repeater,room
		mask, err := parseTypes(rest[0])
		if err != nil {
			return usageError(fs, "%v (use chat, repeater, room, sensor or all)", err)
		}
		view.types = mask & meshgore.AutoAddAllTypes
	default:
		return usageError(fs, "wants one list of node types")
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	all, changed, err := syncContacts(l.Client, s.node(), *full)
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	now := time.Now()
	shown := view.apply(all, now)
	if s.g.json {
		type entry struct {
			Name       string  `json:"name"`
			Type       string  `json:"type"`
			Key        string  `json:"key"`
			LastAdvert string  `json:"last_advert,omitempty"`
			AgeS       int64   `json:"age_s,omitempty"`
			ClockAhead bool    `json:"clock_ahead,omitempty"`
			Lat        float64 `json:"lat,omitempty"`
			Lon        float64 `json:"lon,omitempty"`
		}
		out := struct {
			Total    int     `json:"total"`
			Contacts []entry `json:"contacts"`
		}{Total: len(all), Contacts: []entry{}}
		for _, ct := range shown {
			e := entry{Name: ct.Name, Type: ct.TypeName(), Key: hex.EncodeToString(ct.PublicKey[:]), Lat: ct.Lat, Lon: ct.Lon}
			if heard(ct) {
				e.LastAdvert = ct.LastAdvert.UTC().Format(time.RFC3339)
				if clockAhead(ct, now) {
					e.ClockAhead = true
				} else {
					e.AgeS = int64(now.Sub(ct.LastAdvert) / time.Second)
				}
			}
			out.Contacts = append(out.Contacts, e)
		}
		enc := json.NewEncoder(s.out)
		enc.SetIndent("", "  ")
		enc.Encode(out)
		return exitOK
	}
	fmt.Printf("Contacts (%d of %d, %d new or changed):\n", len(shown), len(all), changed)
	for _, ct := range shown {
		fmt.Println(contactLine(ct, now))
	}
	return exitOK
}

func cmdContact(s *session, args []string) int {
	fs := newFlags("contact", "remove NAME")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) < 2 || rest[0] != "remove" {
		return usageError(fs, "wants: remove NAME")
	}
	a := newActions(s)
	a.removeContact = strings.Join(rest[1:], " ")
	return withCompanion(s, a)
}

func cmdChannels(s *session, args []string) int {
	fs := newFlags("channels", "")
	if _, code, ok := parseFlags(fs, args); !ok {
		return code
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	chans, err := l.Client.Channels()
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	for _, ch := range chans {
		fmt.Printf("  [%d] %s\n", ch.Index, ch.Name)
	}
	return exitOK
}

func cmdChannel(s *session, args []string) int {
	fs := newFlags("channel", "add NAME [-secret HEX|random] [-slot N] [-overwrite] | remove NAME|SLOT [-overwrite]")
	secret := fs.String("secret", "", "with add: the secret as 32 hex digits, or \"random\"; not needed for #hashtag channels")
	slot := fs.Int("slot", -1, "with add: the slot to use (default: the first empty one)")
	overwrite := fs.Bool("overwrite", false, "with add: replace a channel already in the slot; with remove: allow emptying slot 0 (Public)")
	// the sub-command comes first, then its flags: "channel add NAME -slot 3"
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		_, code, ok := parseFlags(fs, args)
		if !ok {
			return code
		}
		return usageError(fs, "wants add or remove")
	}
	sub, tail := args[0], args[1:]
	if sub == "list" {
		return cmdChannels(s, tail)
	}
	// flags may follow the name, so pull them out first
	var words []string
	for len(tail) > 0 {
		if strings.HasPrefix(tail[0], "-") {
			rest, code, ok := parseFlags(fs, tail)
			if !ok {
				return code
			}
			tail = rest
			continue
		}
		words = append(words, tail[0])
		tail = tail[1:]
	}
	if len(words) != 1 {
		return usageError(fs, "wants one channel name or slot")
	}
	a := newActions(s)
	switch sub {
	case "add":
		a.channel, a.secret, a.slot, a.force = words[0], *secret, *slot, *overwrite
	case "remove":
		a.removeChannel, a.force = words[0], *overwrite
	default:
		return usageError(fs, "unknown action %q (add, remove or list)", sub)
	}
	return withCompanion(s, a)
}

func cmdDiscover(s *session, args []string) int {
	fs := newFlags("discover", "[-wait DURATION] [all|chat,repeater,room,sensor]")
	wait := fs.Duration("wait", 8*time.Second, "how long to collect answers")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	a := newActions(s)
	a.discover, a.discoverWait = "all", *wait
	if len(rest) == 1 {
		a.discover = rest[0]
	} else if len(rest) > 1 {
		return usageError(fs, "wants one list of node types")
	}
	return withCompanion(s, a)
}

func cmdRepeater(s *session, args []string) int {
	fs := newFlags("repeater", "[-password P] [-c CMD]... NAME [status] [telemetry] [watch INTERVAL] [cmd TEXT...]")
	password := fs.String("password", "", "the repeater's admin password (or set MC_PASSWORD)")
	timeout := fs.Duration("timeout", s.g.cmdTimeout, "how long to wait for each command's reply")
	csvPath := fs.String("csv", "", "with watch: append each reading to this CSV file")
	var cmds stringList
	fs.Var(&cmds, "c", "a CLI command to run on the repeater (repeatable)")
	var t thresholds
	thresholdFlags(fs, &t)
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) == 0 {
		return usageError(fs, "wants the repeater's name")
	}
	o := repeaterOpts{
		node: s.node(), target: rest[0], password: *password, cmds: cmds, timeout: *timeout,
		json: s.g.json, out: s.out, limits: t.limits(), safe: s.g.safe, csvPath: *csvPath,
	}
	if o.password == "" {
		o.password = os.Getenv("MC_PASSWORD")
	}
	actions := rest[1:]
	for len(actions) > 0 {
		switch a := actions[0]; a {
		case "status":
			o.status, actions = true, actions[1:]
		case "telemetry":
			o.telemetry, actions = true, actions[1:]
		case "watch":
			if len(actions) < 2 {
				return usageError(fs, "watch wants an interval")
			}
			d, err := time.ParseDuration(actions[1])
			if err != nil || d <= 0 {
				return usageError(fs, "%q is not a duration like 30s or 5m", actions[1])
			}
			o.watch, actions = d, actions[2:]
		case "cmd":
			if len(actions) < 2 {
				return usageError(fs, "cmd wants the command text")
			}
			o.cmds = append(o.cmds, strings.Join(actions[1:], " "))
			actions = nil
		default:
			return usageError(fs, "unknown action %q", a)
		}
	}
	if o.watch > 0 && len(o.cmds) > 0 {
		return usageError(fs, "watch cannot be combined with commands")
	}
	if !o.status && !o.telemetry && o.watch == 0 && len(o.cmds) == 0 {
		o.status = true // a bare "repeater NAME" shows its status
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	return runRepeater(s.ctx, l, o)
}

func cmdCLI(s *session, args []string) int {
	fs := newFlags("cli", "[-c CMD]... [TEXT...]")
	var cmds stringList
	fs.Var(&cmds, "c", "a CLI command (repeatable); without -c, the words after the options are one command")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) > 0 {
		cmds = append(cmds, strings.Join(rest, " "))
	}
	if len(cmds) == 0 {
		return usageError(fs, "wants a command")
	}
	if s.g.console {
		src := s.console()
		if src == nil {
			return exitFail
		}
		return runConsoleCmds(s.ctx, src, cmds, s.g.safe, s.g.reconnectWait, s.g.json, s.out)
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	return runLocalCmds(s.ctx, l, cmds, s.g.safe, s.g.reconnectWait, s.g.json, s.out)
}

func cmdScan(s *session, args []string) int {
	fs := newFlags("scan", "")
	if _, code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if err := meshgore.Enable(); err != nil {
		fmt.Println("error: failed to enable the Bluetooth adapter:", err)
		return exitFail
	}
	if _, err := meshgore.Scan(""); err != nil && err != meshgore.ErrScanOnly {
		fmt.Println("error:", err)
		return exitFail
	}
	return exitOK
}

func cmdPorts(s *session, args []string) int {
	fs := newFlags("ports", "")
	if _, code, ok := parseFlags(fs, args); !ok {
		return code
	}
	ports, err := meshgore.ListSerialPorts()
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	for _, p := range ports {
		fmt.Println(p)
	}
	return exitOK
}

// hopNames names the repeaters whose key starts with a hop's hash, from the contact
// cache. Several matches are all shown; none gives the hash alone.
func hopNames(cached []*meshgore.Contact, hop []byte) string {
	var names []string
	for _, ct := range cached {
		if ct.Type == 2 && bytes.HasPrefix(ct.PublicKey[:], hop) {
			names = append(names, ct.Name)
		}
	}
	if len(names) == 0 {
		return fmt.Sprintf("%x (unknown)", hop)
	}
	return fmt.Sprintf("%x %s", hop, strings.Join(names, " | "))
}

func printPath(label string, cached []*meshgore.Contact, hops [][]byte) {
	if len(hops) == 0 {
		fmt.Printf("%s: direct, no repeater\n", label)
		return
	}
	fmt.Printf("%s: %d hop(s)\n", label, len(hops))
	for i, h := range hops {
		fmt.Printf("  %d. %s\n", i+1, hopNames(cached, h))
	}
}

func cmdPath(s *session, args []string) int {
	fs := newFlags("path", "[-password P] [-attempts N] CONTACT")
	attempts := fs.Int("attempts", 5, "how many times to send the login and the request (each a flood that may be lost)")
	password := fs.String("password", "", "log in to the repeater first (admin or guest password; -password= is an empty one; or set MC_PASSWORD)")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		return usageError(fs, "wants one contact")
	}
	if *attempts < 1 {
		return usageError(fs, "-attempts wants a number of at least 1")
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	ct, err := resolveContact(l.Client, s.node(), rest[0])
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	fmt.Printf("Path to: %s (%s, key %x)\n", ct.Name, ct.TypeName(), ct.PublicKey[:6])
	// An empty password is a login too (a guest password may be empty), so what counts is
	// that the flag or the variable was given, not that it has text.
	login := false
	fs.Visit(func(f *flag.Flag) { login = login || f.Name == "password" })
	if !login {
		if v, ok := os.LookupEnv("MC_PASSWORD"); ok {
			*password, login = v, true
		}
	}
	if login {
		if err := l.Client.LoginWithAttempts(ct.PublicKey, *password, *attempts, true); err != nil {
			fmt.Println("error:", err)
			return exitFail
		}
		fmt.Println("Logged in")
		defer l.Client.Logout(ct.PublicKey)
	}
	p, err := l.Client.DiscoverPath(ct.PublicKey, *attempts)
	if err != nil {
		fmt.Println("error:", err)
		if !login && ct.Type != 1 {
			fmt.Println("A repeater or room server answers only a node that has logged in: try -password.")
		}
		return exitFail
	}
	cached, _ := loadContactCache(s.node())
	if s.g.json {
		hex := func(hops [][]byte) []string {
			out := []string{}
			for _, h := range hops {
				out = append(out, fmt.Sprintf("%x", h))
			}
			return out
		}
		enc := json.NewEncoder(s.out)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"to": ct.Name, "key": fmt.Sprintf("%x", ct.PublicKey[:]), "out": hex(p.Out), "in": hex(p.In)})
		return exitOK
	}
	printPath("Out", cached, p.Out)
	printPath("Back", cached, p.In)
	return exitOK
}
