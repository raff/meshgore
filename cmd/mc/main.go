package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/raff/meshgore"
)

func main() {
	name := flag.String("name", "", "Meshcore device name")
	healthcheck := flag.String("healthcheck", "", "code to send on the #healthcheck channel")
	listen := flag.Duration("listen", 15*time.Second, "how long to wait for new messages (0 to exit after syncing)")
	contactsFlag := flag.Bool("contacts", false, "sync and list contacts (only changes are fetched after the first time)")
	fullSync := flag.Bool("full-sync", false, "with -contacts: discard the cache and download every contact again")
	hcWait := flag.Duration("healthcheck-wait", 0, "after sending the healthcheck, wait this long for the code to appear on #healthcheck; exit 1 if it doesn't")
	repeater := flag.String("repeater", "", "repeater to administer remotely: contact name or hex key (prefix)")
	password := flag.String("password", "", "repeater admin password (or set MC_PASSWORD)")
	status := flag.Bool("status", false, "print statistics: the repeater's with -repeater, else the companion's own")
	var cmds stringList
	flag.Var(&cmds, "cmd", "CLI command to run on the repeater, or on the companion itself without -repeater (repeatable)")
	cmdTimeout := flag.Duration("cmd-timeout", 30*time.Second, "how long to wait for each command's reply")
	jsonOut := flag.Bool("json", false, "with -repeater: print the result as JSON on stdout; progress goes to stderr")
	minBattery := flag.Float64("min-battery", 0, "with -repeater -status: exit 2 if battery volts are below this (0 = off)")
	maxNoise := flag.Int("max-noise", 0, "with -repeater -status: exit 2 if the noise floor in dBm is above this, e.g. -100 (0 = off)")
	safe := flag.Bool("safe", true, "with -repeater: only allow read-only -cmd commands; -safe=false allows anything")
	watch := flag.Duration("watch", 0, "with -repeater: stay connected and read its status every interval (e.g. 5m); -json prints one object per line")
	csvPath := flag.String("csv", "", "with -watch: append each reading to this CSV file")
	verbose := flag.Bool("v", false, "log raw protocol frames")
	dm := flag.String("dm", "", "send a direct message to this contact (name, hex key prefix or full key); needs -text")
	dmText := flag.String("text", "", "the message for -dm")
	setName := flag.String("set-name", "", "set the name the companion advertises")
	setPosition := flag.String("set-position", "", "set the position the companion advertises, as \"<lat>,<lon>\" in degrees")
	addChannel := flag.String("add-channel", "", "add a channel with this name (a #hashtag channel needs no secret)")
	channelSecretFlag := flag.String("channel-secret", "", "with -add-channel: the secret as 32 hex digits, or \"random\"; not needed for #hashtag channels")
	channelSlot := flag.Int("channel-slot", -1, "with -add-channel: the slot to use (default: the first empty one)")
	channelOverwrite := flag.Bool("channel-overwrite", false, "with -add-channel: allow replacing a channel that is already in the slot")
	vars := flag.Bool("vars", false, "list the companion's custom variables (what its board supports, e.g. GPS)")
	advert := flag.String("advert", "", "make the companion advertise itself: \"flood\" (repeated across the mesh) or \"zerohop\" (direct neighbours only)")
	reconnectWait := flag.Duration("reconnect-wait", 2*time.Minute, "after a -cmd that reboots the companion, how long to wait for it to come back (0 = don't reconnect)")
	gps := flag.String("gps", "", "switch the companion's GPS \"on\" or \"off\" (only on builds with GPS; see -vars)")
	gpsInterval := flag.Int("gps-interval", -1, "how often the GPS updates the position, in seconds (0..86400; 0 = no automatic updates)")
	flag.Parse()

	// In JSON mode stdout is reserved for the result, so progress text (which
	// is printed with fmt.Print*) is redirected to stderr.
	realStdout := os.Stdout
	if *jsonOut {
		os.Stdout = os.Stderr
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	meshgore.Debug = *verbose
	meshgore.Logf = func(format string, args ...any) { fmt.Printf(format+"\n", args...) }

	if err := meshgore.Enable(); err != nil {
		log.Fatalf("failed to enable BLE adapter: %v", err)
	}
	target, err := meshgore.Scan(*name)
	if err != nil {
		log.Fatal(err)
	}
	l, err := meshgore.Dial(target.Address)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	printNodeInfo(l)

	if *watch > 0 && (*repeater == "" || len(cmds) > 0) {
		log.Fatal("-watch needs -repeater and cannot be combined with -cmd")
	}

	if *vars {
		os.Exit(runVars(l))
	}

	if len(cmds) > 0 && *repeater == "" {
		os.Exit(runLocalCmds(ctx, l, cmds, *safe, *reconnectWait, *jsonOut, realStdout))
	}

	if *status && *repeater == "" {
		os.Exit(runStats(l, *jsonOut, realStdout))
	}

	if acts := (nodeActions{
		name: *setName, position: *setPosition, channel: *addChannel,
		secret: *channelSecretFlag, slot: *channelSlot, force: *channelOverwrite, advert: *advert,
		gps: *gps, interval: *gpsInterval,
	}); acts.any() {
		os.Exit(runNodeActions(l, acts))
	}

	if *dm != "" {
		if *dmText == "" {
			log.Fatal("-dm needs -text")
		}
		os.Exit(runDM(ctx, l, *name, *dm, *dmText, *jsonOut, realStdout))
	}

	if *repeater != "" {
		pw := *password
		if pw == "" {
			pw = os.Getenv("MC_PASSWORD")
		}
		os.Exit(runRepeater(ctx, l, repeaterOpts{
			watch: *watch, csvPath: *csvPath,
			node: *name, target: *repeater, password: pw, status: *status,
			cmds: cmds, timeout: *cmdTimeout, json: *jsonOut, out: realStdout,
			limits: meshgore.Thresholds{MinBatteryV: *minBattery, MaxNoiseFloor: *maxNoise}, safe: *safe,
		}))
	}

	channels, err := l.Client.Channels()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Channels:")
	names := map[int]string{}
	for _, ch := range channels {
		names[ch.Index] = ch.Name
		fmt.Printf("  [%d] %s\n", ch.Index, ch.Name)
	}

	// Contacts are opt-in. After the first download only changes are fetched.
	contactNames := map[[6]byte]string{}
	if *contactsFlag || *fullSync {
		contacts, changed, err := syncContacts(l.Client, *name, *fullSync)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Contacts (%d, %d new or changed):\n", len(contacts), changed)
		for _, ct := range contacts {
			contactNames[[6]byte(ct.PublicKey[:6])] = ct.Name
			fmt.Printf("  %-24s %-8s key %x last advert %s\n",
				ct.Name, ct.TypeName(), ct.PublicKey[:6], ct.LastAdvert.Format(time.DateTime))
		}
	}

	// printMessages drains the device's message queue, prints what it finds
	// and returns it.
	printMessages := func() []*meshgore.Message {
		msgs, err := l.Client.SyncMessages()
		for _, msg := range msgs {
			from := fmt.Sprintf("%x", msg.From)
			if msg.Channel {
				from = names[msg.ChannelIdx]
				if from == "" {
					from = fmt.Sprintf("channel %d", msg.ChannelIdx)
				}
			} else if n := contactNames[msg.From]; n != "" {
				from = n
			}
			fmt.Printf("[%s] %s: %s\n", msg.Time.Format(time.DateTime), from, msg.Text)
		}
		if err != nil {
			fmt.Println("message sync:", err)
		}
		return msgs
	}

	if *healthcheck != "" {
		os.Exit(runHealthcheck(l, channels, *healthcheck, *hcWait, printMessages))
	}

	// Print queued messages, then new ones as the device announces them.
	printMessages()

	if *listen == 0 {
		return
	}
	fmt.Printf("Listening for %s...\n", *listen)
	timeout := time.After(*listen)
	check := time.NewTicker(5 * time.Second) // detect a dropped connection
	defer check.Stop()
	for {
		select {
		case f := <-l.Client.Push: // re-evaluated each loop: the client changes on reconnect
			if f[0] == meshgore.PushMsgWaiting {
				printMessages()
			}
		case <-check.C:
			if l.Alive() {
				continue
			}
			fmt.Println("connection to the companion lost; reconnecting...")
			if err := l.Reconnect(ctx); err != nil {
				return
			}
			printMessages() // catch up on anything missed meanwhile
		case <-timeout:
			return
		case <-ctx.Done():
			return
		}
	}
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ", ") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// repeaterOpts configures runRepeater.
type repeaterOpts struct {
	node, target, password string
	status                 bool
	cmds                   []string
	timeout                time.Duration
	json                   bool
	out                    io.Writer // where JSON goes; progress text goes to os.Stdout
	limits                 meshgore.Thresholds
	safe                   bool          // refuse commands that are not known read-only
	watch                  time.Duration // poll status at this interval instead of a single run
	csvPath                string        // with watch: append readings to this CSV file
}

// Exit codes: 0 ok, 1 failure, 2 a threshold was crossed.
const (
	exitOK    = 0
	exitFail  = 1
	exitAlert = 2
)

type cmdResult struct {
	Cmd     string   `json:"cmd"`
	Replies []string `json:"replies"`
}

type repeaterResult struct {
	OK       bool                     `json:"ok"`
	Error    string                   `json:"error,omitempty"`
	Repeater string                   `json:"repeater,omitempty"`
	Key      string                   `json:"key,omitempty"`
	Status   *meshgore.RepeaterStatus `json:"status,omitempty"`
	Alerts   []string                 `json:"alerts,omitempty"`
	Commands []cmdResult              `json:"commands,omitempty"`
}

// runRepeater logs in to a remote repeater through the companion node, reads
// its status and runs commands. It returns the process exit code and
// disconnects the device itself because os.Exit skips deferred calls.
func runRepeater(ctx context.Context, l *meshgore.Link, o repeaterOpts) int {
	defer l.Close()
	client := l.Client

	var res repeaterResult
	code := exitOK
	defer func() {
		res.OK = code == exitOK
		if o.json && o.watch == 0 {
			enc := json.NewEncoder(o.out)
			enc.SetIndent("", "  ")
			enc.Encode(res)
		}
	}()
	fail := func(err error) int {
		fmt.Println("error:", err)
		res.Error = err.Error()
		code = exitFail
		return code
	}

	if o.safe {
		for _, c := range o.cmds {
			if !meshgore.IsReadOnlyCommand(c) {
				return fail(fmt.Errorf("%q is not a known read-only command; use -safe=false to allow it (see CLI.md)", c))
			}
		}
	}

	ct, err := resolveContact(client, o.node, o.target)
	if err != nil {
		return fail(err)
	}
	res.Repeater, res.Key = ct.Name, hex.EncodeToString(ct.PublicKey[:])
	fmt.Printf("Repeater: %s (%s, key %x)\n", ct.Name, ct.TypeName(), ct.PublicKey[:6])

	if err := client.Login(ct.PublicKey, o.password); err != nil {
		return fail(err)
	}
	fmt.Println("Logged in")
	if o.watch > 0 {
		return watchRepeater(ctx, l, ct, o)
	}
	defer client.Logout(ct.PublicKey)

	if o.status {
		st, err := client.RequestStatus(ct.PublicKey)
		if err != nil {
			return fail(err)
		}
		res.Status = st
		fmt.Print(st)
		if res.Alerts = st.Check(o.limits); len(res.Alerts) > 0 {
			for _, a := range res.Alerts {
				fmt.Println("ALERT:", a)
			}
			code = exitAlert
		}
	}

	from := [6]byte(ct.PublicKey[:6])
	for _, c := range o.cmds {
		fmt.Printf("> %s\n", c)
		if err := client.SendCLI(ct.PublicKey, c); err != nil {
			return fail(err)
		}
		replies := client.CollectReplies(from, o.timeout, 3*time.Second, func(m *meshgore.Message) {
			fmt.Printf("(other message: %q)\n", m.Text)
		})
		if len(replies) == 0 {
			return fail(fmt.Errorf("no reply to %q within %s", c, o.timeout))
		}
		res.Commands = append(res.Commands, cmdResult{c, replies})
		for _, r := range replies {
			fmt.Println(r)
		}
	}
	return code
}

// runHealthcheck sends code on #healthcheck. With wait > 0 it then waits for a
// message on that channel containing the code (an echo or reply from another
// node) and reports whether one arrived. It returns the process exit code and
// disconnects the device itself because os.Exit skips deferred calls.
func runHealthcheck(l *meshgore.Link, channels []*meshgore.Channel, code string, wait time.Duration, printMessages func() []*meshgore.Message) int {
	defer l.Close()
	client := l.Client

	var hc *meshgore.Channel
	for _, ch := range channels {
		if strings.EqualFold(ch.Name, "#healthcheck") {
			hc = ch
			break
		}
	}
	if hc == nil {
		fmt.Println("channel #healthcheck not found on the device")
		return 1
	}
	tag, flood, err := client.SendChannelMessage(hc.Index, code)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	fmt.Printf("Sent %q on %s [%d]", code, hc.Name, hc.Index)
	if tag != 0 { // only MSG_SENT replies carry a tag; a plain OK does not
		fmt.Printf(" (ack tag %08x, flood=%v)", tag, flood)
	}
	fmt.Println()
	if wait == 0 {
		return 0
	}

	fmt.Printf("Waiting up to %s for %q to appear on %s...\n", wait, code, hc.Name)
	deadline := time.After(wait)
	poll := time.NewTicker(5 * time.Second) // in case a messages-waiting push is missed
	defer poll.Stop()
	for {
		select {
		case f := <-client.Push:
			if f[0] != meshgore.PushMsgWaiting {
				continue
			}
		case <-poll.C:
		case <-deadline:
			fmt.Println("healthcheck FAILED: not seen before timeout")
			return 1
		}
		for _, msg := range printMessages() {
			if msg.Channel && msg.ChannelIdx == hc.Index && strings.Contains(msg.Text, code) {
				fmt.Println("healthcheck OK")
				return 0
			}
		}
	}
}

// printNodeInfo prints what the handshake learned about the companion.
func printNodeInfo(l *meshgore.Link) {
	s := l.Self
	fmt.Printf("Node %q\n  pubkey %x\n  pos %.6f,%.6f\n  radio %.3f MHz BW %.1f kHz SF%d CR%d\n",
		s.Name, s.PublicKey, s.Lat, s.Lon, s.FreqMHz, s.BandwidthKHz, s.SF, s.CR)
	if d := l.Client.Info; d != nil {
		fmt.Printf("Device: %s fw v%d (%s, build %s)\n  max contacts %d, max channels %d\n",
			d.Model, d.FirmwareVer, d.Version, d.FirmwareBuild, d.MaxContacts, d.MaxChannels)
	}
}

// runVars prints the companion's custom variables and what they suggest about
// the hardware.
// runLocalCmds runs CLI commands on the companion itself.
func runLocalCmds(ctx context.Context, l *meshgore.Link, cmds []string, safe bool, wait time.Duration, asJSON bool, out *os.File) int {
	defer l.Close()
	fail := func(err error) int { fmt.Println("error:", err); return exitFail }
	if safe {
		for _, c := range cmds {
			if !meshgore.IsReadOnlyCommand(c) {
				return fail(fmt.Errorf("%q is not a known read-only command; use -safe=false to allow it (see CLI.md)", c))
			}
		}
	}
	type result struct {
		Command string `json:"command"`
		Reply   string `json:"reply"`
	}
	var res []result
	for i, c := range cmds {
		fmt.Printf("> %s\n", c)
		if strings.TrimSpace(c) == "reboot" {
			l.Client.RunCLINoReply(c)
			fmt.Println("(rebooting)")
			res = append(res, result{c, ""})
			if i == len(cmds)-1 && wait == 0 {
				break
			}
			if wait == 0 {
				return fail(errors.New("-reconnect-wait=0: cannot run further commands after reboot"))
			}
			fmt.Printf("Waiting up to %s for the companion to come back...\n", wait)
			rctx, cancel := context.WithTimeout(ctx, wait)
			err := l.Reconnect(rctx)
			cancel()
			if err != nil {
				return fail(fmt.Errorf("companion did not come back within %s: %w", wait, err))
			}
			printNodeInfo(l)
			continue
		}
		r, err := l.Client.RunCLI(c)
		if err != nil {
			return fail(err)
		}
		fmt.Println(r)
		res = append(res, result{c, r})
	}
	if asJSON {
		json.NewEncoder(out).Encode(map[string]any{"commands": res})
	}
	return exitOK
}

// runStats prints the companion's own statistics.
func runStats(l *meshgore.Link, asJSON bool, out *os.File) int {
	defer l.Close()
	st, err := l.Client.Stats()
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.Encode(st)
		return exitOK
	}
	fmt.Print(st)
	return exitOK
}

func runVars(l *meshgore.Link) int {
	defer l.Close()
	vars, err := l.Client.CustomVars()
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	if len(vars) == 0 {
		fmt.Println("No custom variables: this build has no configurable sensors or GPS.")
		return exitOK
	}
	hasGPS := false
	for _, v := range vars {
		fmt.Printf("  %-20s %s\n", v.Name, v.Value)
		hasGPS = hasGPS || v.Name == "gps"
	}
	if hasGPS {
		fmt.Println("GPS: supported (the \"gps\" variable exists).")
	} else {
		fmt.Println("GPS: no \"gps\" variable, so GPS support is probably not compiled into this build.")
	}
	return exitOK
}
