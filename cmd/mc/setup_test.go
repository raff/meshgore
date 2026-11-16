package main

import (
	"testing"

	"github.com/raff/meshgore"
)

func TestParsePosition(t *testing.T) {
	lat, lon, err := parsePosition("48.8584, 2.2945")
	if err != nil || lat != 48.8584 || lon != 2.2945 {
		t.Errorf("got %v %v %v", lat, lon, err)
	}
	for _, bad := range []string{"", "37.3", "37.3,abc", "a,-122", "1,2,3"} {
		if _, _, err := parsePosition(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestChannelSecret(t *testing.T) {
	// A hashtag channel derives its secret from the name.
	s, gen, err := channelSecret("#healthcheck", "")
	if err != nil || gen || s != meshgore.HashtagSecret("#healthcheck") {
		t.Errorf("hashtag: %v %v %v", s, gen, err)
	}
	// A private channel needs a secret.
	if _, _, err := channelSecret("private", ""); err == nil {
		t.Error("private channel without a secret should fail")
	}
	s, gen, err = channelSecret("private", "000102030405060708090a0b0c0d0e0f")
	if err != nil || gen || s[15] != 15 {
		t.Errorf("hex: %v %v %v", s, gen, err)
	}
	for _, bad := range []string{"abcd", "zz0102030405060708090a0b0c0d0e0f", "000102030405060708090a0b0c0d0e0f00"} {
		if _, _, err := channelSecret("private", bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	a, gen, err := channelSecret("private", "random")
	b, _, _ := channelSecret("private", "random")
	if err != nil || !gen || a == b || a == ([16]byte{}) {
		t.Errorf("random: %v %v %v", a, gen, err)
	}
}

func TestFirstEmptySlot(t *testing.T) {
	used := []*meshgore.Channel{{Index: 0}, {Index: 1}, {Index: 3}}
	if i, ok := firstEmptySlot(used, 8); !ok || i != 2 {
		t.Errorf("got %d %v", i, ok)
	}
	if _, ok := firstEmptySlot(used, 2); ok {
		t.Error("slots 0 and 1 are both used")
	}
	if i, ok := firstEmptySlot(nil, 8); !ok || i != 0 {
		t.Errorf("empty: %d %v", i, ok)
	}
}

func TestNodeActionsAny(t *testing.T) {
	if (nodeActions{interval: -1}).any() || (nodeActions{slot: 3, force: true, interval: -1}).any() {
		t.Error("slot and overwrite alone are not actions")
	}
	for _, a := range []nodeActions{{name: "x", interval: -1}, {position: "1,2", interval: -1}, {channel: "#c", interval: -1}, {advert: "flood", interval: -1}, {gps: "on", interval: -1}, {interval: 60}} {
		if !a.any() {
			t.Errorf("%+v should count as an action", a)
		}
	}
}

func TestParseTypes(t *testing.T) {
	for spec, want := range map[string]byte{
		"repeater": 0x04, "chat,repeater": 0x06, "all": 0x1E, "none": 0, "Room, Sensor": 0x18,
	} {
		if got, err := parseTypes(spec); err != nil || got != want {
			t.Errorf("%q: got %#x, %v; want %#x", spec, got, err, want)
		}
	}
	if _, err := parseTypes("router"); err == nil {
		t.Error("unknown type accepted")
	}
}

func TestParseAutoAdd(t *testing.T) {
	c, err := parseAutoAdd(nodeActions{autoAdd: "repeater,room", autoOverwrite: "on", autoHops: "3"})
	if err != nil || c.manual == nil || !*c.manual || *c.types != 0x0C || !*c.overwrite || *c.hops != 3 {
		t.Errorf("%+v, %v", c, err)
	}
	c, _ = parseAutoAdd(nodeActions{autoAdd: "all"})
	if *c.manual || *c.types != 0x1E {
		t.Errorf("all: %+v", c)
	}
	if c, _ := parseAutoAdd(nodeActions{autoAdd: "show"}); !c.show || c.manual != nil {
		t.Errorf("show: %+v", c)
	}
	if c, _ := parseAutoAdd(nodeActions{}); c.set() {
		t.Error("nothing asked, but set() is true")
	}
	for _, bad := range []nodeActions{{autoAdd: "bogus"}, {autoOverwrite: "yes"}, {autoHops: "65"}, {autoHops: "x"}} {
		if _, err := parseAutoAdd(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
