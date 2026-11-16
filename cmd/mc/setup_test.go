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
	if (nodeActions{}).any() || (nodeActions{slot: 3, force: true}).any() {
		t.Error("slot and overwrite alone are not actions")
	}
	for _, a := range []nodeActions{{name: "x"}, {position: "1,2"}, {channel: "#c"}, {advert: "flood"}} {
		if !a.any() {
			t.Errorf("%+v should count as an action", a)
		}
	}
}
