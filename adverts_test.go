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
