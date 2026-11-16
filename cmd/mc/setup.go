package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

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
}

func (a nodeActions) any() bool {
	return a.name != "" || a.position != "" || a.channel != "" || a.advert != "" || a.gps != "" || a.interval >= 0
}

// parsePosition parses "lat,lon" in degrees.
func parsePosition(s string) (lat, lon float64, err error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("-set-position wants \"<lat>,<lon>\" in degrees, not %q", s)
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
		return secret, false, fmt.Errorf("%q is not a #hashtag channel, so it needs -channel-secret (32 hex digits, or \"random\")", name)
	case spec == "random":
		if _, err := rand.Read(secret[:]); err != nil {
			return secret, false, err
		}
		return secret, true, nil
	}
	b, err := hex.DecodeString(spec)
	if err != nil || len(b) != 16 {
		return secret, false, fmt.Errorf("-channel-secret must be exactly 32 hex digits (16 bytes), or \"random\"")
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
func runNodeActions(l *meshgore.Link, a nodeActions) int {
	defer l.Close()
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
		return fail(fmt.Errorf("-gps must be \"on\" or \"off\", not %q", a.gps))
	}
	if a.interval > 86400 {
		return fail(fmt.Errorf("-gps-interval must be 0..86400 seconds"))
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
			return fail(fmt.Errorf("a channel named %q already exists in slot %d with a different secret; use -channel-slot=%d -channel-overwrite to replace it", a.channel, ch.Index, ch.Index))
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
				return fail(fmt.Errorf("slot %d holds %q; use -channel-overwrite to replace it", slot, ch.Name))
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
