package meshgore

import (
	"bytes"
	"crypto/sha256"
	"strings"
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

func TestScopeKeyAndFrames(t *testing.T) {
	// the key is the first 16 bytes of SHA-256("#" + name)
	want := sha256.Sum256([]byte("#bay-area"))
	if got := ScopeKey("bay-area"); !bytes.Equal(got[:], want[:16]) {
		t.Errorf("key % x, want % x", got, want[:16])
	}
	cmd, err := buildSetScope("bay-area")
	if err != nil || len(cmd) != 48 || cmd[0] != 0x3F {
		t.Fatalf("frame %d bytes, %v", len(cmd), err)
	}
	if string(cmd[1:9]) != "bay-area" || cmd[9] != 0 || cmd[31] != 0 || !bytes.Equal(cmd[32:], want[:16]) {
		t.Errorf("layout wrong: % x", cmd)
	}
	for _, bad := range []string{"", "Bay-Area", "#bay", "a b", "ü", strings.Repeat("a", 30)} {
		if _, err := buildSetScope(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := buildSetScope(strings.Repeat("a", 29)); err != nil {
		t.Errorf("29 bytes refused: %v", err)
	}

	// replies: a name and key, or only the code
	reply := append([]byte{0x1C}, cmd[1:]...)
	sc, err := parseScope(reply)
	if err != nil || !sc.Set || sc.Name != "bay-area" || sc.Key != ScopeKey("bay-area") {
		t.Errorf("%+v, %v", sc, err)
	}
	if sc, err := parseScope([]byte{0x1C}); err != nil || sc.Set {
		t.Errorf("empty reply: %+v, %v", sc, err)
	}
	if _, err := parseScope(make([]byte, 20)); err == nil {
		t.Error("a short reply was accepted")
	}
}
