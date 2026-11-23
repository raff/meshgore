package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/raff/meshgore"
)

// nodeActions are the things -set-name, -set-position, -add-channel and -advert
// do to the companion itself. They run in that order, so a single command can
// set up a fresh node and announce it.
type nodeActions struct {
	name     string
	position string // "lat,lon"
	channel  string
	secret   string // hex, "random", or empty for a hashtag channel
	slot     int    // -1: the first empty slot
	force    bool   // allow overwriting a used slot
	advert   string // "flood", "zerohop" or empty
	gps      string // "on", "off" or empty
	interval int    // GPS update interval in seconds; -1: leave alone

	radio      string // "freq,bw,sf,cr"
	syncClock  bool
	rxDelay    string // seconds, as typed; empty: leave alone
	airtime    string // airtime factor, as typed
	shareLoc   string // "share" or "none"
	multiAcks  string // "0", "1", ...
	showClock  bool
	showTuning bool
	showTele   bool

	node          string // the companion's name, for the contact cache
	autoAdd       string // "show", "all", "none" or a list of types
	autoOverwrite string // "on" or "off"
	autoHops      string // 0..64
	pathHash      string // 0..2
	discover      string // node types to look for
	discoverWait  time.Duration
	discoverSave  bool // save the answers that are not contacts
	removeContact string
	removeChannel string
}

func (a nodeActions) any() bool {
	return a.name != "" || a.position != "" || a.channel != "" || a.advert != "" || a.gps != "" || a.interval >= 0 ||
		a.radio != "" || a.syncClock || a.rxDelay != "" || a.airtime != "" || a.shareLoc != "" || a.multiAcks != "" ||
		a.showClock || a.showTuning || a.showTele ||
		a.autoAdd != "" || a.autoOverwrite != "" || a.autoHops != "" || a.pathHash != "" || a.discover != "" ||
		a.removeContact != "" || a.removeChannel != ""
}

// parsePosition parses "lat,lon" in degrees.
func parsePosition(s string) (lat, lon float64, err error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("a position is \"<lat>,<lon>\" in degrees, not %q", s)
	}
	if lat, err = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err != nil {
		return 0, 0, fmt.Errorf("bad latitude %q", parts[0])
	}
	if lon, err = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err != nil {
		return 0, 0, fmt.Errorf("bad longitude %q", parts[1])
	}
	return lat, lon, nil
}

// channelSecret works out the secret for a new channel. A hashtag channel needs
// none, since its secret comes from its name. A private channel needs one, given
// as 32 hex digits, or "random" to make one. generated says it was made here, so
// the caller can show it (the only chance to).
func channelSecret(name, spec string) (secret [16]byte, generated bool, err error) {
	switch {
	case spec == "" && strings.HasPrefix(name, "#"):
		return meshgore.HashtagSecret(name), false, nil
	case spec == "":
		return secret, false, fmt.Errorf("%q is not a #hashtag channel, so it needs -secret (32 hex digits, or \"random\")", name)
	case spec == "random":
		if _, err := rand.Read(secret[:]); err != nil {
			return secret, false, err
		}
		return secret, true, nil
	}
	b, err := hex.DecodeString(spec)
	if err != nil || len(b) != 16 {
		return secret, false, fmt.Errorf("-secret must be exactly 32 hex digits (16 bytes), or \"random\"")
	}
	copy(secret[:], b)
	return secret, false, nil
}

// firstEmptySlot returns the lowest slot number below max that is not in used.
func firstEmptySlot(used []*meshgore.Channel, max int) (int, bool) {
	taken := make(map[int]bool, len(used))
	for _, ch := range used {
		taken[ch.Index] = true
	}
	for i := 0; i < max; i++ {
		if !taken[i] {
			return i, true
		}
	}
	return 0, false
}

// runNodeActions applies a to the companion and returns the process exit code.
// Everything is checked before the first change, so a typo in a later flag does
// not leave the node half configured.
func runNodeActions(ctx context.Context, l *meshgore.Link, a nodeActions) int {
	fail := func(err error) int { fmt.Println("error:", err); return exitFail }

	var lat, lon float64
	var secret [16]byte
	var generated bool
	var err error
	if a.position != "" {
		if lat, lon, err = parsePosition(a.position); err != nil {
			return fail(err)
		}
	}
	if a.channel != "" {
		if secret, generated, err = channelSecret(a.channel, a.secret); err != nil {
			return fail(err)
		}
	}
	if a.advert != "" && a.advert != "flood" && a.advert != "zerohop" {
		return fail(fmt.Errorf("-advert must be \"flood\" or \"zerohop\", not %q", a.advert))
	}

	if a.gps != "" && a.gps != "on" && a.gps != "off" {
		return fail(fmt.Errorf("gps wants on or off, not %q", a.gps))
	}
	if a.interval > 86400 {
		return fail(fmt.Errorf("the GPS interval must be 0..86400 seconds"))
	}

	var freq, bw, rxd, atf float64
	var sf, cr, acks int
	if a.radio != "" {
		if freq, bw, sf, cr, err = meshgore.ParseRadioSpec(a.radio); err != nil {
			return fail(fmt.Errorf("-set-radio: %w", err))
		}
	}
	if a.rxDelay != "" {
		if rxd, err = strconv.ParseFloat(a.rxDelay, 64); err != nil {
			return fail(fmt.Errorf("-rx-delay: bad number %q", a.rxDelay))
		}
	}
	if a.airtime != "" {
		if atf, err = strconv.ParseFloat(a.airtime, 64); err != nil {
			return fail(fmt.Errorf("-airtime-factor: bad number %q", a.airtime))
		}
	}
	if a.shareLoc != "" && a.shareLoc != "share" && a.shareLoc != "none" {
		return fail(fmt.Errorf("share-location must be \"share\" or \"none\", not %q", a.shareLoc))
	}
	if a.multiAcks != "" {
		if acks, err = strconv.Atoi(a.multiAcks); err != nil || acks < 0 || acks > 255 {
			return fail(fmt.Errorf("multi-acks must be 0..255, not %q", a.multiAcks))
		}
	}

	autoCfg, err := parseAutoAdd(a)
	if err != nil {
		return fail(err)
	}
	var hash int
	if a.pathHash != "" {
		if hash, err = strconv.Atoi(a.pathHash); err != nil || hash < 0 || hash > 2 {
			return fail(fmt.Errorf("path-hash must be 0, 1 or 2, not %q", a.pathHash))
		}
	}
	var discoverTypes byte
	if a.discover != "" {
		if discoverTypes, err = parseTypes(a.discover); err != nil || discoverTypes == 0 {
			return fail(fmt.Errorf("discover wants a list of chat, repeater, room, sensor (or all), not %q", a.discover))
		}
	}

	c := l.Client
	if a.name != "" {
		if err := c.SetName(a.name); err != nil {
			return fail(err)
		}
		fmt.Printf("Name set to %q\n", a.name)
	}
	if a.position != "" {
		if err := c.SetPosition(lat, lon); err != nil {
			return fail(err)
		}
		fmt.Printf("Position set to %.6f,%.6f\n", lat, lon)
	}
	if a.gps != "" {
		if err := c.SetGPS(a.gps == "on"); err != nil {
			return fail(err)
		}
		fmt.Printf("GPS %s\n", a.gps)
	}
	if a.interval >= 0 {
		if err := c.SetGPSInterval(a.interval); err != nil {
			return fail(err)
		}
		fmt.Printf("GPS interval set to %d s\n", a.interval)
	}
	if a.radio != "" {
		if err := c.SetRadio(freq, bw, sf, cr); err != nil {
			return fail(err)
		}
		fmt.Printf("Radio set to %.3f MHz BW %.1f kHz SF%d CR%d (saved)\n", freq, bw, sf, cr)
	}
	if a.rxDelay != "" || a.airtime != "" {
		t, err := c.Tuning()
		if err != nil {
			return fail(err)
		}
		if a.rxDelay != "" {
			t.RxDelayBase = rxd
		}
		if a.airtime != "" {
			t.AirtimeFactor = atf
		}
		if err := c.SetTuning(*t); err != nil {
			return fail(err)
		}
		fmt.Printf("Tuning set: receive delay %g, airtime factor %g\n", t.RxDelayBase, t.AirtimeFactor)
	}
	if a.shareLoc != "" || a.multiAcks != "" {
		// SET_OTHER_PARAMS overwrites everything, so start from the current values.
		p := meshgore.OtherParamsOf(l.Self)
		if a.shareLoc != "" {
			p.AdvLocPolicy = map[string]byte{"none": 0, "share": 1}[a.shareLoc]
		}
		if a.multiAcks != "" {
			p.MultiAcks = byte(acks)
		}
		if err := c.SetOtherParams(p); err != nil {
			return fail(err)
		}
		l.Self.AdvLocPolicy, l.Self.MultiAcks = p.AdvLocPolicy, p.MultiAcks
		fmt.Printf("Advert location: %s, multi-acks: %d\n", map[byte]string{0: "none", 1: "share"}[p.AdvLocPolicy], p.MultiAcks)
	}
	if a.syncClock {
		now := time.Now()
		if err := c.SetDeviceTime(now); err != nil {
			return fail(fmt.Errorf("%w (the firmware refuses to move the clock backwards)", err))
		}
		fmt.Println("Clock set to", now.UTC().Format(time.DateTime), "UTC")
	}
	if autoCfg.set() {
		if code := applyAutoAdd(l, autoCfg); code != exitOK {
			return code
		}
	}
	if a.pathHash != "" {
		if err := c.SetPathHashMode(hash); err != nil {
			return fail(err)
		}
		fmt.Printf("Path hash mode set to %d (%d-byte hashes)\n", hash, hash+1)
	}
	if a.removeContact != "" {
		if code := removeContact(c, a.node, a.removeContact); code != exitOK {
			return code
		}
	}
	if a.removeChannel != "" {
		if code := removeChannel(c, a); code != exitOK {
			return code
		}
	}
	if a.channel != "" {
		if code := addChannel(c, a, secret, generated); code != exitOK {
			return code
		}
	}
	if a.advert != "" {
		if err := c.SendAdvert(a.advert == "flood"); err != nil {
			return fail(err)
		}
		fmt.Printf("Advert (%s) sent\n", a.advert)
	}
	if a.discover != "" {
		if code := runDiscover(ctx, c, a, discoverTypes); code != exitOK {
			return code
		}
	}
	if a.showClock {
		t, err := c.DeviceTime()
		if err != nil {
			return fail(err)
		}
		fmt.Printf("Clock: %s UTC (%+.0fs from this computer)\n", t.Format(time.DateTime), t.Sub(time.Now()).Seconds())
	}
	if a.showTuning {
		t, err := c.Tuning()
		if err != nil {
			return fail(err)
		}
		fmt.Printf("Tuning: receive delay %g, airtime factor %g\n", t.RxDelayBase, t.AirtimeFactor)
	}
	if a.showTele {
		rs, err := c.SelfTelemetry()
		if err != nil {
			return fail(err)
		}
		fmt.Println("Telemetry:")
		for _, r := range rs {
			fmt.Printf("  %-12s %-28s (channel %d)\n", r.Name, r.Value, r.Channel)
		}
	}
	return exitOK
}

// addChannel adds a channel, doing nothing if the same channel is already there.
func addChannel(c *meshgore.Client, a nodeActions, secret [16]byte, generated bool) int {
	fail := func(err error) int { fmt.Println("error:", err); return exitFail }

	used, err := c.Channels()
	if err != nil {
		return fail(err)
	}
	for _, ch := range used {
		if ch.Name != a.channel {
			continue
		}
		if ch.Secret == secret {
			fmt.Printf("Channel %q is already in slot %d; nothing to do\n", a.channel, ch.Index)
			return exitOK
		}
		if !a.force {
			return fail(fmt.Errorf("a channel named %q already exists in slot %d with a different secret; use \"channel add -slot=%d -overwrite\" to replace it", a.channel, ch.Index, ch.Index))
		}
	}

	slot := a.slot
	if slot < 0 {
		var ok bool
		if slot, ok = firstEmptySlot(used, c.Info.MaxChannels); !ok {
			return fail(fmt.Errorf("all %d channel slots are in use", c.Info.MaxChannels))
		}
	} else if !a.force {
		for _, ch := range used {
			if ch.Index == slot {
				return fail(fmt.Errorf("slot %d holds %q; use -overwrite to replace it", slot, ch.Name))
			}
		}
	}

	if err := c.SetChannel(slot, a.channel, secret); err != nil {
		return fail(err)
	}
	fmt.Printf("Channel %q added in slot %d\n", a.channel, slot)
	if generated {
		fmt.Printf("Secret (keep it; it cannot be shown again, and others need it to join): %x\n", secret)
	}
	return exitOK
}

// parseTypes turns "repeater,room" (or "all") into a mask of advert type bits.
func parseTypes(spec string) (byte, error) {
	var mask byte
	for _, t := range strings.Split(spec, ",") {
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "all":
			mask |= meshgore.AutoAddAllTypes
		case "chat":
			mask |= meshgore.AutoAddChat
		case "repeater":
			mask |= meshgore.AutoAddRepeater
		case "room":
			mask |= meshgore.AutoAddRoom
		case "sensor":
			mask |= meshgore.AutoAddSensor
		case "none", "":
		default:
			return 0, fmt.Errorf("unknown node type %q", t)
		}
	}
	return mask, nil
}

// autoAddChange is what -auto-add, -auto-add-overwrite and -auto-add-max-hops ask for.
type autoAddChange struct {
	show      bool
	manual    *bool // turn manual add on or off
	types     *byte // the type bits to set
	overwrite *bool
	hops      *byte
}

func (c autoAddChange) set() bool {
	return c.show || c.manual != nil || c.types != nil || c.overwrite != nil || c.hops != nil
}

// parseAutoAdd checks the auto-add flags. "all" makes the node add every type (manual
// add off); "none" or a list of types turns manual add on, so only those are added.
func parseAutoAdd(a nodeActions) (autoAddChange, error) {
	var c autoAddChange
	b := func(v bool) *bool { return &v }
	switch spec := strings.ToLower(strings.TrimSpace(a.autoAdd)); spec {
	case "":
	case "show":
		c.show = true
	case "all":
		m, all := false, byte(meshgore.AutoAddAllTypes)
		c.manual, c.types = &m, &all
	default:
		mask, err := parseTypes(spec)
		if err != nil {
			return c, fmt.Errorf("-auto-add: %w (use all, none, show or a list of chat, repeater, room, sensor)", err)
		}
		m := true
		c.manual, c.types = &m, &mask
	}
	switch a.autoOverwrite {
	case "":
	case "on", "off":
		c.overwrite = b(a.autoOverwrite == "on")
	default:
		return c, fmt.Errorf("auto-add -overwrite must be on or off, not %q", a.autoOverwrite)
	}
	if a.autoHops != "" {
		n, err := strconv.Atoi(a.autoHops)
		if err != nil || n < 0 || n > 64 {
			return c, fmt.Errorf("auto-add -max-hops must be 0..64 (0 = no limit), not %q", a.autoHops)
		}
		h := byte(n)
		c.hops = &h
	}
	return c, nil
}

// applyAutoAdd reads the node's auto-add settings, changes what was asked and
// prints the result. The type bits count only while manual add is on, which is a
// separate flag (SET_OTHER_PARAMS), so both are written.
func applyAutoAdd(l *meshgore.Link, ch autoAddChange) int {
	fail := func(err error) int { fmt.Println("error:", err); return exitFail }
	cur, err := l.Client.GetAutoAdd()
	if err != nil {
		return fail(err)
	}
	manual := l.Self.ManualAddContacts
	if ch.show && ch.manual == nil && ch.overwrite == nil && ch.hops == nil {
		fmt.Println(cur.Describe(manual))
		return exitOK
	}
	next := cur
	if ch.types != nil {
		next.Config = next.Config&^meshgore.AutoAddAllTypes | *ch.types
	}
	if ch.overwrite != nil {
		if *ch.overwrite {
			next.Config |= meshgore.AutoAddOverwriteOldest
		} else {
			next.Config &^= meshgore.AutoAddOverwriteOldest
		}
	}
	if ch.hops != nil {
		next.MaxHops = *ch.hops
	}
	if next != cur {
		if err := l.Client.SetAutoAdd(next); err != nil {
			return fail(err)
		}
	}
	if ch.manual != nil && *ch.manual != manual {
		p := meshgore.OtherParamsOf(l.Self)
		p.ManualAddContacts = *ch.manual
		if err := l.Client.SetOtherParams(p); err != nil {
			return fail(err)
		}
		l.Self.ManualAddContacts, manual = *ch.manual, *ch.manual
	}
	fmt.Println(next.Describe(manual))
	return exitOK
}

// removeContact deletes a contact from the node and from the local cache.
func removeContact(c *meshgore.Client, node, target string) int {
	fail := func(err error) int { fmt.Println("error:", err); return exitFail }
	ct, err := resolveContact(c, node, target)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("Removing contact %q (%s, key %x)\n", ct.Name, ct.TypeName(), ct.PublicKey[:6])
	if err := c.RemoveContact(ct.PublicKey); err != nil {
		return fail(err)
	}
	cached, lastMod := loadContactCache(node)
	kept := cached[:0]
	for _, x := range cached {
		if x.PublicKey != ct.PublicKey {
			kept = append(kept, x)
		}
	}
	if len(kept) != len(cached) {
		saveContactCache(node, kept, lastMod)
	}
	fmt.Println("Removed. The node adds it again if it hears another advert and auto-add allows it.")
	return exitOK
}

// removeChannel empties a channel slot, given by number or by name.
func removeChannel(c *meshgore.Client, a nodeActions) int {
	fail := func(err error) int { fmt.Println("error:", err); return exitFail }
	chans, err := c.Channels()
	if err != nil {
		return fail(err)
	}
	var target *meshgore.Channel
	if n, err := strconv.Atoi(a.removeChannel); err == nil {
		for _, ch := range chans {
			if ch.Index == n {
				target = ch
			}
		}
		if target == nil {
			return fail(fmt.Errorf("channel slot %d is empty or does not exist", n))
		}
	} else {
		for _, ch := range chans {
			if ch.Name == a.removeChannel {
				target = ch
			}
		}
		if target == nil {
			return fail(fmt.Errorf("no channel named %q", a.removeChannel))
		}
	}
	if target.Index == 0 && !a.force {
		return fail(errors.New("slot 0 is the Public channel; use \"channel remove 0 -overwrite\" to remove it anyway"))
	}
	fmt.Printf("Removing channel [%d] %s\n", target.Index, target.Name)
	if err := c.ClearChannel(target.Index); err != nil {
		return fail(err)
	}
	if ch, err := c.GetChannel(target.Index); err == nil && ch.Empty() {
		fmt.Println("Removed.")
	} else {
		fmt.Println("Cleared; the slot may still read back with its old name until the node is restarted.")
	}
	return exitOK
}

// runDiscover asks nearby nodes to identify themselves and prints the answers.
func runDiscover(ctx context.Context, c *meshgore.Client, a nodeActions, types byte) int {
	wait := a.discoverWait
	if wait <= 0 {
		wait = 8 * time.Second
	}
	fmt.Printf("Discovering %s within radio range (%s)...\n", a.discover, wait)
	ns, err := c.Discover(ctx, types, wait)
	if err != nil {
		fmt.Println("error:", err)
		return exitFail
	}
	names := map[string]string{}
	if cached, _ := loadContactCache(a.node); cached != nil {
		for _, ct := range cached {
			names[string(ct.PublicKey[:8])] = ct.Name
		}
	}
	// The cache is empty after a reset; the node's own contact list has the names.
	if live, err := c.GetContacts(); err != nil {
		fmt.Println("warning: could not read the node's contacts for names:", err)
	} else {
		for _, ct := range live {
			if ct.Name != "" {
				names[string(ct.PublicKey[:8])] = ct.Name
			}
		}
	}
	if len(ns) == 0 {
		fmt.Println("No answer. A repeater answers a discovery request at most 4 times in 2 minutes.")
	}
	for _, n := range ns {
		typeName := map[int]string{1: "chat", 2: "repeater", 3: "room", 4: "sensor"}[n.Type]
		name := names[string(n.Key[:8])]
		note := ""
		if name == "" {
			name = "(unknown)"
			switch {
			case !a.discoverSave:
			case len(n.Key) != 32:
				note = "  (not saved: the reply has only a key prefix)"
			case typeName == "":
				note = "  (not saved: unknown node type)"
			default:
				ct := &meshgore.Contact{Type: byte(n.Type), Name: fmt.Sprintf("%s-%x", typeName, n.Key[:3]), LastAdvert: time.Now()}
				copy(ct.PublicKey[:], n.Key)
				if err := c.AddContact(ct); err != nil {
					note = "  (not saved: " + err.Error() + ")"
				} else {
					name, note = ct.Name, "  [saved as a contact]"
				}
			}
		}
		fmt.Printf("  %-24s %-8s key %x  heard us %+.2f dB, we heard it %+.2f dB (RSSI %.0f)%s\n",
			name, typeName, n.Key[:6], n.TheirSNR, n.SNR, n.RSSI, note)
	}
	return exitOK
}
