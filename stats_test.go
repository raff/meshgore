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
