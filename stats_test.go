package meshgore

import "testing"

func TestCompanionStatsParse(t *testing.T) {
	var s CompanionStats
	core := []byte{0x18, 0, 0xE7, 0x10, 0x10, 0x07, 0, 0, 0, 0, 2}
	radio := []byte{0x18, 1, 0x88, 0xFF, 0xDD, 0x38, 1, 0, 0, 0, 67, 0, 0, 0}
	pk := make([]byte, 30)
	pk[0], pk[1], pk[2], pk[26] = 0x18, 2, 5, 3
	st := []byte{0x0C, 0xE7, 0x10, 28, 0, 0, 0, 100, 0, 0, 0}
	for _, err := range []error{s.parseCore(core), s.parseRadio(radio), s.parsePackets(pk), s.parseStorage(st)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.BatteryMV != 4327 || s.Uptime.Seconds() != 1808 || s.QueueLen != 2 {
		t.Errorf("core: %+v", s)
	}
	if s.NoiseFloor != -120 || s.LastRSSI != -35 || s.LastSNR != 14 || s.RxAirtime.Seconds() != 67 {
		t.Errorf("radio: %+v", s)
	}
	if s.Received != 5 || !s.HasErrors || s.RecvErrors != 3 || s.UsedKB != 28 || s.TotalKB != 100 {
		t.Errorf("packets/storage: %+v", s)
	}
	if err := s.parseCore(core[:5]); err == nil {
		t.Error("short core frame accepted")
	}
	if err := s.parsePackets(pk[:26]); err != nil {
		t.Error(err)
	}
}

func TestBuildSetRadio(t *testing.T) {
	got, err := buildSetRadio(910.525, 62.5, 7, 5)
	want := []byte{0x0B, 0xBD, 0xE4, 0x0D, 0, 0x24, 0xF4, 0, 0, 7, 5} // 910525 kHz, 62500 Hz
	if err != nil || string(got) != string(want) {
		t.Errorf("got % x, %v; want % x", got, err, want)
	}
	for _, bad := range [][4]float64{{100, 62.5, 7, 5}, {910, 600, 7, 5}, {910, 62.5, 13, 5}, {910, 62.5, 7, 9}} {
		if _, err := buildSetRadio(bad[0], bad[1], int(bad[2]), int(bad[3])); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestParseLPP(t *testing.T) {
	// voltage 4.23 V; GPS 37.7749, -122.4194, 12.34 m (0.0001 deg, 0.01 m)
	b := []byte{1, 0x74, 0x01, 0xA7, 1, 0x88, 0x05, 0xC3, 0x95, 0xED, 0x51, 0xFE, 0x00, 0x04, 0xD2, 9, 0x99, 1}
	got, rest := parseLPP(b)
	if len(got) != 2 || got[0].Value != "4.23 V" || got[1].Value != "37.7749,-122.4194 alt 12.34 m" {
		t.Errorf("got %+v", got)
	}
	if len(rest) != 3 {
		t.Errorf("rest % x", rest)
	}
}

func TestParseLPPPadding(t *testing.T) {
	// a repeater's reply: voltage 4.09 V, temperature 20.0 C, then four zero bytes
	got, rest := parseLPP([]byte{1, 0x74, 0x01, 0x99, 1, 0x67, 0x00, 0xC8, 0, 0, 0, 0})
	if len(got) != 2 || got[0].Value != "4.09 V" || got[1].Value != "20.0 C" || len(rest) != 0 {
		t.Errorf("got %+v, rest % x", got, rest)
	}
}
