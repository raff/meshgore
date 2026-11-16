package meshgore

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// statusFrame builds a 0x87 push: 8-byte header plus the 56-byte body.
func statusFrame() []byte {
	f := make([]byte, 8+56)
	f[0] = pushStatusResp
	le := binary.LittleEndian
	b := f[8:]
	le.PutUint16(b[0:], 4130)   // battery mV
	le.PutUint16(b[4:], 0xFF90) // noise floor -112
	le.PutUint16(b[6:], 0xFFDF) // last RSSI -33
	le.PutUint32(b[8:], 652)    // received
	le.PutUint32(b[20:], 5959)  // uptime s
	le.PutUint16(b[42:], 49)    // snr 12.25
	le.PutUint32(b[52:], 37)    // recv errors
	return f
}

func TestParseStatus(t *testing.T) {
	s, err := parseStatus(statusFrame())
	if err != nil {
		t.Fatal(err)
	}
	if s.BatteryMV != 4130 || s.NoiseFloor != -112 || s.LastRSSI != -33 || s.LastSNR != 12.25 ||
		s.Received != 652 || s.Uptime.Seconds() != 5959 || !s.HasRecvErrs || s.RecvErrors != 37 {
		t.Fatalf("unexpected status: %+v", s)
	}
	if _, err := parseStatus(statusFrame()[:30]); err == nil {
		t.Fatal("short frame should fail")
	}
	// Older firmware: no recv_errors field.
	old, err := parseStatus(statusFrame()[:60])
	if err != nil || old.HasRecvErrs {
		t.Fatalf("old frame: %+v, %v", old, err)
	}
}

func TestStatusJSON(t *testing.T) {
	s, _ := parseStatus(statusFrame())
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"battery_v":4.13`, `"uptime_s":5959`, `"noise_floor_dbm":-112`, `"receive_errors":37`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("JSON %s lacks %s", data, want)
		}
	}
}

func TestCheck(t *testing.T) {
	s, _ := parseStatus(statusFrame())
	if a := s.Check(Thresholds{}); len(a) != 0 {
		t.Errorf("no limits set, got %v", a)
	}
	if a := s.Check(Thresholds{MinBatteryV: 3.6, MaxNoiseFloor: -100}); len(a) != 0 {
		t.Errorf("healthy status alerted: %v", a)
	}
	if a := s.Check(Thresholds{MinBatteryV: 4.5, MaxNoiseFloor: -120}); len(a) != 2 {
		t.Errorf("want 2 alerts, got %v", a)
	}
}

func TestIsReadOnlyCommand(t *testing.T) {
	safe := []string{"ver", "board", "clock", "CLOCK", " neighbors ", "get radio", "get acl", "get name",
		"region", "region get *", "region list allowed", "region home", "region default",
		"stats-core", "stats-radio", "stats-packets", "log", "gps", "GPS", "gps advert", "sensor list", "sensor list 8", "sensor get gps"}
	unsafe := []string{"", "reboot", "poweroff", "clkreboot", "clock sync", "time 1700000000", "advert",
		"advert.zerohop", "erase", "start ota", "set name x", "set radio 910.5,62.5,7,5", "tempradio 910,62.5,7,5,10",
		"password hunter2", "get prv.key", "get", "get radio extra", "region home X", "region default X",
		"log erase", "log start", "stats-core x", "erase", "region put X", "region save", "gps on", "gps off", "gps sync", "gps setloc", "gps advert share",
		"sensor set gps 1", "sensor list x", "sensor get", "sensor", "log erase", "setperm abc 1", "neighbor.remove abc", "neighbors x"}
	for _, c := range safe {
		if !IsReadOnlyCommand(c) {
			t.Errorf("%q should be safe", c)
		}
	}
	for _, c := range unsafe {
		if IsReadOnlyCommand(c) {
			t.Errorf("%q should not be safe", c)
		}
	}
}

func TestParseContactEnd(t *testing.T) {
	if got := parseContactEnd([]byte{respContactEnd, 0x78, 0x56, 0x34, 0x12}); got != 0x12345678 {
		t.Errorf("got %#x", got)
	}
	if got := parseContactEnd([]byte{respContactEnd}); got != 0 {
		t.Errorf("short frame: got %d", got)
	}
}

func TestBuildDM(t *testing.T) {
	var to [32]byte
	for i := range to {
		to[i] = byte(i + 1)
	}
	got := buildDM(to, "hi", 2, 0x12345678)
	want := []byte{0x02, 0x00, 0x02, 0x78, 0x56, 0x34, 0x12, 1, 2, 3, 4, 5, 6, 'h', 'i'}
	if string(got) != string(want) {
		t.Errorf("got % x, want % x", got, want)
	}
}

func TestParseMsgSent(t *testing.T) {
	s, err := parseMsgSent([]byte{respMsgSent, 1, 0xAA, 0xBB, 0xCC, 0xDD, 0x1e, 0x09, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Flood || s.Ack != [4]byte{0xAA, 0xBB, 0xCC, 0xDD} || s.Timeout != 2334*time.Millisecond {
		t.Errorf("unexpected: %+v", s)
	}
	if _, err := parseMsgSent([]byte{respMsgSent, 0}); err == nil {
		t.Error("short frame should fail")
	}
}

func TestParseAck(t *testing.T) {
	code, trip, ok := parseAck([]byte{pushAck, 0xd2, 0x4d, 0xfe, 0xd6, 0x76, 0x02, 0x00, 0x00})
	if !ok || code != [4]byte{0xd2, 0x4d, 0xfe, 0xd6} || trip != 630*time.Millisecond {
		t.Errorf("got %v %v %v", code, trip, ok)
	}
	// An older frame without the round-trip field.
	if code, trip, ok := parseAck([]byte{pushAck, 1, 2, 3, 4}); !ok || code != [4]byte{1, 2, 3, 4} || trip != 0 {
		t.Errorf("short ack: %v %v %v", code, trip, ok)
	}
	if _, _, ok := parseAck([]byte{pushMsgWaiting}); ok {
		t.Error("not an ack")
	}
	if _, _, ok := parseAck([]byte{pushAck, 1}); ok {
		t.Error("truncated ack")
	}
}

func TestAdvertCmd(t *testing.T) {
	if got := advertCmd(false); string(got) != "\x07" {
		t.Errorf("zero-hop: % x", got)
	}
	if got := advertCmd(true); string(got) != "\x07\x01" {
		t.Errorf("flood: % x", got)
	}
}

func TestBuildSetName(t *testing.T) {
	got, err := buildSetName("MyNode")
	if err != nil || string(got) != "\x08MyNode" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := buildSetName(""); err == nil {
		t.Error("empty name should fail")
	}
	if _, err := buildSetName(strings.Repeat("x", 31)); err != nil {
		t.Errorf("31 bytes is allowed: %v", err)
	}
	if _, err := buildSetName(strings.Repeat("x", 32)); err == nil {
		t.Error("32 bytes would be cut off by the device")
	}
}

func TestBuildSetPosition(t *testing.T) {
	got, err := buildSetPosition(48.8584, 2.2945)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 13 || got[0] != cmdSetAdvertLatLon {
		t.Fatalf("frame: % x", got)
	}
	lat := int32(binary.LittleEndian.Uint32(got[1:]))
	lon := int32(binary.LittleEndian.Uint32(got[5:]))
	if lat != 48858400 || lon != 2294500 {
		t.Errorf("lat %d lon %d", lat, lon)
	}
	for _, bad := range [][2]float64{{91, 0}, {-91, 0}, {0, 181}, {0, -181}, {math.NaN(), 0}} {
		if _, err := buildSetPosition(bad[0], bad[1]); err == nil {
			t.Errorf("%v should be rejected", bad)
		}
	}
}

func TestHashtagSecret(t *testing.T) {
	// SHA-256("#healthcheck")[:16], computed independently with Python's hashlib.
	want := "3ae59c2cda86744c75e68b3975a902ae"
	s := HashtagSecret("#healthcheck")
	if got := hex.EncodeToString(s[:]); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestBuildSetChannel(t *testing.T) {
	secret := HashtagSecret("#healthcheck")
	got, err := buildSetChannel(3, "#healthcheck", secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 || got[0] != cmdSetChannel || got[1] != 3 {
		t.Fatalf("frame: % x", got)
	}
	if string(got[2:14]) != "#healthcheck" || got[14] != 0 || got[33] != 0 {
		t.Errorf("name field wrong: %q", got[2:34])
	}
	if string(got[34:]) != string(secret[:]) {
		t.Error("secret not at bytes 34..50")
	}
	if _, err := buildSetChannel(0, "x", [16]byte{}); err == nil {
		t.Error("all-zero secret must be refused")
	}
	if _, err := buildSetChannel(0, "", secret); err == nil {
		t.Error("empty name must be refused")
	}
	if _, err := buildSetChannel(-1, "x", secret); err == nil {
		t.Error("negative slot must be refused")
	}
}

func TestParseCustomVars(t *testing.T) {
	got := parseCustomVars([]byte("\x15gps:0,gps_interval:0,temp.offset:1.5"))
	want := []Var{{"gps", "0"}, {"gps_interval", "0"}, {"temp.offset", "1.5"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("var %d: got %v, want %v", i, got[i], want[i])
		}
	}
	if v := parseCustomVars([]byte{respCustomVars}); v != nil {
		t.Errorf("no variables: got %v", v)
	}
	if v := parseCustomVars([]byte("\x15novalue")); len(v) != 1 || v[0].Name != "novalue" || v[0].Value != "" {
		t.Errorf("name without value: %v", v)
	}
}

func TestBuildSetCustomVar(t *testing.T) {
	got, err := buildSetCustomVar("gps", "1")
	if err != nil || string(got) != "\x29gps:1" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := buildSetCustomVar("a:b", "1"); err == nil {
		t.Error("colon in name accepted")
	}
}

func TestSendChannelMessageLimits(t *testing.T) {
	// the checks come before anything is sent, so a client with no connection is enough
	c := &Client{}
	for _, text := range []string{"", strings.Repeat("a", MaxChannelText+1), strings.Repeat("é", MaxChannelText/2+1)} {
		if _, _, err := c.SendChannelMessage(0, text); err == nil || strings.Contains(err.Error(), "timeout") {
			t.Errorf("%d bytes: got %v, want a refusal", len(text), err)
		}
	}
}
