package meshgore

import (
	"bytes"
	"testing"
)

func TestBuildDiscover(t *testing.T) {
	got := buildDiscover(AutoAddRepeater, 0x12345678)
	want := []byte{0x80, 0x04, 0x78, 0x56, 0x34, 0x12, 0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("got % x, want % x", got, want)
	}
}

func TestParseDiscoverResponse(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	// push: our SNR 9.00 dB (0x24), RSSI -80, path length 0, then the 38-byte answer:
	// repeater (0x92), their SNR 10.25 dB (41), the tag, the public key
	f := append([]byte{0x8E, 0x24, 0xB0, 0x00, 0x92, 0x29, 0x78, 0x56, 0x34, 0x12}, key...)
	n, ok := parseDiscoverResponse(f, 0x12345678)
	if !ok || n.Type != TypeRepeater || n.TheirSNR != 10.25 || n.SNR != 9 || n.RSSI != -80 || !bytes.Equal(n.Key, key) {
		t.Errorf("got %+v, %v", n, ok)
	}
	if _, ok := parseDiscoverResponse(f, 1); ok {
		t.Error("accepted an answer to another request")
	}
	if _, ok := parseDiscoverResponse(append([]byte{0x8E, 0, 0, 0, 0x80, 0, 0, 0, 0, 0}, key...), 0); ok {
		t.Error("accepted a request as an answer")
	}
	short := append([]byte{0x8E, 0x24, 0xB0, 0x00, 0x92, 0x29, 0x78, 0x56, 0x34, 0x12}, key[:8]...) // prefix-only
	if n, ok := parseDiscoverResponse(short, 0x12345678); !ok || len(n.Key) != 8 {
		t.Errorf("prefix answer: %+v, %v", n, ok)
	}
}

func TestAutoAddDescribe(t *testing.T) {
	a := AutoAdd{Config: AutoAddRepeater | AutoAddOverwriteOldest, MaxHops: 3}
	if got := a.Describe(true); got != "auto-add: repeater; up to 3 hops; overwrite oldest when full: true" {
		t.Error(got)
	}
	if got := a.Describe(false); got != "auto-add: chat, repeater, room, sensor; up to 3 hops; overwrite oldest when full: true" {
		t.Error(got)
	}
	if got := (AutoAdd{}).Describe(true); got != "auto-add: nothing (manual); any distance; overwrite oldest when full: false" {
		t.Error(got)
	}
}
