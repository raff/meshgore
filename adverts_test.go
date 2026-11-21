package meshgore

import "testing"

func TestParseAdvert(t *testing.T) {
	f := make([]byte, 33)
	f[0], f[1], f[32] = pushAdvertisement, 0xAA, 0xBB
	a, err := parseAdvert(f)
	if err != nil || !a.Known || a.Key[0] != 0xAA || a.Key[31] != 0xBB {
		t.Fatalf("known advert: %+v, %v", a, err)
	}
	n := make([]byte, contactFrameLen)
	n[0], n[1], n[33] = pushNewAdvert, 0xCC, 2
	copy(n[100:], "bot-1")
	a, err = parseAdvert(n)
	if err != nil || a.Known || a.Info.Name != "bot-1" || a.Key[0] != 0xCC || a.Info.TypeName() != "repeater" {
		t.Fatalf("new advert: %+v, %v", a, err)
	}
	if _, err := parseAdvert(f[:10]); err == nil {
		t.Fatal("short frame accepted")
	}
}

func TestAddContactCmd(t *testing.T) {
	n := make([]byte, contactFrameLen)
	n[0], n[1], n[33] = pushNewAdvert, 0xCC, 2
	copy(n[100:], "bot-1")
	a, _ := parseAdvert(n)
	b := addContactCmd(a.Info)
	if b[0] != cmdAddUpdateContact || len(b) != 144 || b[1] != 0xCC || b[33] != 2 || b[35] != 0xFF || string(b[100:105]) != "bot-1" {
		t.Fatalf("bad frame % x", b)
	}
	// What we build must read back as the same contact.
	back, err := parseContact(append(append([]byte{0}, b[1:]...), 0, 0, 0, 0))
	if err != nil || back.Name != "bot-1" || back.PublicKey != a.Key || back.OutPathLen != -1 {
		t.Fatalf("round trip: %+v, %v", back, err)
	}
}

func TestParseRxAdvert(t *testing.T) {
	key := make([]byte, 32)
	key[0] = 0xEE
	// SNR, RSSI, header (flood, advert), 2 hops of 1 byte, payload
	f := append([]byte{pushLogData, 20, 0xB0, 1 | payloadAdvert<<2, 2, 0xAA, 0xBB}, key...)
	r, ok := parseRxAdvert(f)
	if !ok || r.key[0] != 0xEE || len(r.path) != 2 || r.path[0][0] != 0xAA || r.path[1][0] != 0xBB || r.direct {
		t.Fatalf("flood advert: %+v ok=%v", r, ok)
	}
	// transport flood, 2-byte hashes, 1 hop
	f = append([]byte{pushLogData, 0, 0, 0 | payloadAdvert<<2, 1, 2, 3, 4, 0x41, 0xAA, 0xBB}, key...)
	r, ok = parseRxAdvert(f)
	if !ok || len(r.path) != 1 || len(r.path[0]) != 2 || r.path[0][1] != 0xBB {
		t.Fatalf("transport advert: %+v ok=%v", r, ok)
	}
	// zero hop
	f = append([]byte{pushLogData, 0, 0, 1 | payloadAdvert<<2, 0}, key...)
	if r, ok = parseRxAdvert(f); !ok || !r.direct || len(r.path) != 0 {
		t.Fatalf("direct advert: %+v ok=%v", r, ok)
	}
	// not an advert (group text), truncated
	if _, ok = parseRxAdvert([]byte{pushLogData, 0, 0, 1 | 5<<2, 0, 1, 2}); ok {
		t.Fatal("group text taken for an advert")
	}
	if _, ok = parseRxAdvert(f[:12]); ok {
		t.Fatal("truncated advert accepted")
	}
}
