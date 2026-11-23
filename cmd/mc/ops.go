package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/raff/meshgore"
)

// repeaterOpts configures runRepeater.
type repeaterOpts struct {
	node, target, password string
	status                 bool
	telemetry              bool
	cmds                   []string
	timeout                time.Duration
	json                   bool
	out                    io.Writer // where JSON goes; progress text goes to os.Stdout
	limits                 meshgore.Thresholds
	safe                   bool          // refuse commands that are not known read-only
	watch                  time.Duration // poll status and/or telemetry at this interval instead of a single run
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
	OK        bool                     `json:"ok"`
	Error     string                   `json:"error,omitempty"`
	Repeater  string                   `json:"repeater,omitempty"`
	Key       string                   `json:"key,omitempty"`
	Status    *meshgore.RepeaterStatus `json:"status,omitempty"`
	Telemetry []meshgore.Reading       `json:"telemetry,omitempty"`
	Alerts    []string                 `json:"alerts,omitempty"`
	Commands  []cmdResult              `json:"commands,omitempty"`
}

// runRepeater logs in to a remote repeater through the companion node, reads
// its status and runs commands. It returns the exit code.
func runRepeater(ctx context.Context, l *meshgore.Link, o repeaterOpts) int {
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

	if err := client.Login(ctx, ct.PublicKey, o.password); err != nil {
		return fail(err)
	}
	fmt.Println("Logged in")
	if o.watch > 0 {
		if !o.status && !o.telemetry {
			o.status = true // watching with no choice of data reads the status
		}
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

	if o.telemetry {
		rs, err := client.RequestTelemetry(ct.PublicKey)
		if err != nil {
			return fail(err)
		}
		res.Telemetry = rs
		fmt.Println("Telemetry:")
		for _, r := range rs {
			fmt.Printf("  %-12s %s\n", r.Name, r.Value)
		}
	}

	from := [6]byte(ct.PublicKey[:6])
	format := messageFormatter(o.node, l)
	for _, c := range o.cmds {
		fmt.Printf("> %s\n", c)
		if err := client.SendCLI(ct.PublicKey, c); err != nil {
			return fail(err)
		}
		replies := client.CollectReplies(from, o.timeout, 3*time.Second, func(m *meshgore.Message) {
			fmt.Println(format(m)) // not a reply: show it like any other message
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
// node) and reports whether one arrived. It returns the exit code.
func runHealthcheck(l *meshgore.Link, channels []*meshgore.Channel, code string, wait time.Duration, printMessages func() []*meshgore.Message) int {
	client := l.Client
	waiting := l.Subscribe(meshgore.PushMsgWaiting) // before sending: the echo may come back at once
	defer waiting.Close()

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
		case <-waiting.C:
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

// channelWait says what to listen for on a channel after sending to it.
type channelWait struct {
	idx   int
	wait  time.Duration
	match string // a message on the channel counts when its text contains this, in any case; "" counts all
	first bool   // stop at the first match
}

// matches says whether msg is one the wait is looking for.
func (w channelWait) matches(msg *meshgore.Message) bool {
	return msg.Channel && msg.ChannelIdx == w.idx &&
		strings.Contains(strings.ToLower(msg.Text), strings.ToLower(w.match))
}

// collectChannel reads messages (sync drains the node's queue, wake says one is
// waiting, and poll covers a missed wake) for w.wait, or until the first match with
// w.first. Matches go to onMatch, everything else to other. A last sync runs when
// the time is up, so a message that came in at the end is not left in the queue.
func collectChannel(ctx context.Context, wake <-chan []byte, poll time.Duration, sync func() ([]*meshgore.Message, error),
	w channelWait, onMatch, other func(*meshgore.Message)) (matched []*meshgore.Message) {
	deadline := time.After(w.wait)
	tick := time.NewTicker(poll)
	defer tick.Stop()
	over := false
	for !over {
		select {
		case <-wake:
		case <-tick.C:
		case <-deadline:
			over = true
		case <-ctx.Done():
			return matched
		}
		msgs, err := sync()
		if err != nil {
			fmt.Println("message sync:", err)
		}
		for i, m := range msgs {
			if !w.matches(m) {
				other(m)
				continue
			}
			matched = append(matched, m)
			onMatch(m)
			if w.first {
				for _, rest := range msgs[i+1:] { // not ours to drop
					other(rest)
				}
				return matched
			}
		}
	}
	return matched
}

// senderOf returns the name a channel message starts with ("Name: text"), or "".
func senderOf(text string) string {
	if i := strings.Index(text, ": "); i > 0 && i <= 40 {
		return text[:i]
	}
	return ""
}

// summarizeReplies says how many messages matched and who sent them.
func summarizeReplies(matched []*meshgore.Message) string {
	var names []string
	seen := map[string]bool{}
	for _, m := range matched {
		if n := senderOf(m.Text); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	out := fmt.Sprintf("%d matching message(s)", len(matched))
	if len(names) > 0 {
		out += " from " + strings.Join(names, ", ")
	}
	return out
}

// printNodeInfo prints what the handshake learned about the companion.
func printNodeInfo(l *meshgore.Link) {
	s := l.Self
	fmt.Printf("Node %q\n  pubkey %x\n  pos %.6f,%.6f\n  radio %.3f MHz BW %.1f kHz SF%d CR%d\n",
		s.Name, s.PublicKey, s.Lat, s.Lon, s.FreqMHz, s.BandwidthKHz, s.SF, s.CR)
	fmt.Printf("  adverts carry location: %s, multi-acks %d\n", map[byte]string{0: "none", 1: "share"}[s.AdvLocPolicy], s.MultiAcks)
	if d := l.Client.Info; d != nil {
		fmt.Printf("Device: %s fw v%d (%s, build %s)\n  max contacts %d, max channels %d\n",
			d.Model, d.FirmwareVer, d.Version, d.FirmwareBuild, d.MaxContacts, d.MaxChannels)
		if d.HasPathHash {
			fmt.Printf("  path hash mode %d (%d-byte hashes)\n", d.PathHashMode, d.PathHashMode+1)
		}
	}
}

// runLocalCmds runs CLI commands on the companion itself.
func runLocalCmds(ctx context.Context, l *meshgore.Link, cmds []string, safe bool, wait time.Duration, asJSON bool, out io.Writer) int {
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
				return fail(errors.New("with -reconnect-wait=0 nothing can run after a reboot"))
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

// printStats reads and prints a node's own statistics and returns exitAlert if a
// limit is crossed.
func printStats(src statsSource, limits meshgore.Thresholds, asJSON bool, out io.Writer) int {
	st, err := src.Stats()
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	alerts := st.Check(limits)
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.Encode(st)
	} else {
		fmt.Print(st)
	}
	for _, a := range alerts {
		fmt.Println("ALERT:", a)
	}
	if len(alerts) > 0 {
		return exitAlert
	}
	return exitOK
}

// runVars prints the companion's custom variables and what they suggest about
// the hardware.
func runVars(l *meshgore.Link) int {
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

// runConsoleCmds runs commands on a repeater or room server's serial console. A
// reboot waits for the port to come back and the commands after it run on the
// reopened console.
func runConsoleCmds(ctx context.Context, src *consoleSource, cmds []string, safe bool, wait time.Duration, asJSON bool, out io.Writer) int {
	fail := func(err error) int { fmt.Println("error:", err); return exitFail }
	if safe {
		for _, c := range cmds {
			if !meshgore.IsReadOnlyCommand(c) {
				return fail(fmt.Errorf("%q is not a known read-only command; use -safe=false to allow it (see CLI.md)", c))
			}
		}
	}
	code := exitOK
	var res []cmdResult
	for _, c := range cmds {
		fmt.Printf("> %s\n", c)
		lines, err := src.c.Run(c, src.timeout)
		if err != nil {
			code = fail(err)
			break
		}
		if len(lines) == 0 {
			fmt.Println("(no reply)")
		}
		for _, l := range lines {
			fmt.Println(l)
		}
		res = append(res, cmdResult{c, lines})
		if f := strings.Fields(c); len(f) > 0 && (f[0] == "reboot" || f[0] == "clkreboot") {
			rctx, cancel := context.WithTimeout(ctx, wait)
			err := src.Reconnect(rctx)
			cancel()
			if err != nil {
				code = fail(fmt.Errorf("the node did not come back within %s: %w", wait, err))
				break
			}
		}
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"ok": code == exitOK, "commands": res})
	}
	return code
}
