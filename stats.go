package meshgore

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Companion statistics. CMD_GET_STATS takes a sub-type and answers with
// RESP_CODE_STATS echoing it; the layouts are from the stats binary frames doc.
const (
	cmdGetBattAndStorage = 0x14
	cmdGetStats          = 0x38

	respBattAndStorage = 0x0C
	respStats          = 0x18

	statsCore    = 0
	statsRadio   = 1
	statsPackets = 2
)

// CompanionStats is a companion node's own statistics. A part the firmware
// does not answer is left out (its Has flag stays false).
type CompanionStats struct {
	HasCore    bool
	BatteryMV  uint16
	Uptime     time.Duration
	ErrorFlags uint16
	QueueLen   uint8

	HasRadio   bool
	NoiseFloor int16 // dBm
	LastRSSI   int8  // dBm
	LastSNR    float64
	TxAirtime  time.Duration
	RxAirtime  time.Duration

	HasPackets bool
	Received   uint32
	Sent       uint32
	SentFlood  uint32
	SentDirect uint32
	RecvFlood  uint32
	RecvDirect uint32
	RecvErrors uint32
	HasErrors  bool // only the 30-byte frame carries recv_errors

	HasStorage bool
	UsedKB     uint32
	TotalKB    uint32
}

func (s *CompanionStats) parseCore(f []byte) error {
	if len(f) < 11 {
		return fmt.Errorf("core stats too short: %d bytes", len(f))
	}
	le := binary.LittleEndian
	s.HasCore = true
	s.BatteryMV = le.Uint16(f[2:])
	s.Uptime = time.Duration(le.Uint32(f[4:])) * time.Second
	s.ErrorFlags = le.Uint16(f[8:])
	s.QueueLen = f[10]
	return nil
}

func (s *CompanionStats) parseRadio(f []byte) error {
	if len(f) < 14 {
		return fmt.Errorf("radio stats too short: %d bytes", len(f))
	}
	le := binary.LittleEndian
	s.HasRadio = true
	s.NoiseFloor = int16(le.Uint16(f[2:]))
	s.LastRSSI = int8(f[4])
	s.LastSNR = float64(int8(f[5])) / 4
	s.TxAirtime = time.Duration(le.Uint32(f[6:])) * time.Second
	s.RxAirtime = time.Duration(le.Uint32(f[10:])) * time.Second
	return nil
}

func (s *CompanionStats) parsePackets(f []byte) error {
	if len(f) < 26 {
		return fmt.Errorf("packet stats too short: %d bytes", len(f))
	}
	le := binary.LittleEndian
	s.HasPackets = true
	s.Received = le.Uint32(f[2:])
	s.Sent = le.Uint32(f[6:])
	s.SentFlood = le.Uint32(f[10:])
	s.SentDirect = le.Uint32(f[14:])
	s.RecvFlood = le.Uint32(f[18:])
	s.RecvDirect = le.Uint32(f[22:])
	if len(f) >= 30 {
		s.RecvErrors, s.HasErrors = le.Uint32(f[26:]), true
	}
	return nil
}

func (s *CompanionStats) parseStorage(f []byte) error {
	if len(f) < 11 {
		return fmt.Errorf("battery and storage reply too short: %d bytes", len(f))
	}
	le := binary.LittleEndian
	s.HasStorage = true
	if !s.HasCore {
		s.BatteryMV = le.Uint16(f[1:])
	}
	s.UsedKB = le.Uint32(f[3:])
	s.TotalKB = le.Uint32(f[7:])
	return nil
}

// Stats reads the companion's own statistics. Parts the firmware rejects or
// answers oddly are skipped; the error is returned only if nothing came back.
func (c *Client) Stats() (*CompanionStats, error) {
	s := &CompanionStats{}
	var errs []string
	get := func(name string, cmd []byte, want byte, parse func([]byte) error) {
		var f []byte
		var err error
		if f, err = c.Do(cmd, want); err == nil {
			err = parse(f)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		}
	}
	// A stats frame answers with its sub-type in byte 1; Do only matches byte 0,
	// and the requests are sent one at a time, so the order keeps them straight.
	get("core", []byte{cmdGetStats, statsCore}, respStats, s.parseCore)
	get("radio", []byte{cmdGetStats, statsRadio}, respStats, s.parseRadio)
	get("packets", []byte{cmdGetStats, statsPackets}, respStats, s.parsePackets)
	get("storage", []byte{cmdGetBattAndStorage}, respBattAndStorage, s.parseStorage)
	if !s.HasCore && !s.HasRadio && !s.HasPackets && !s.HasStorage {
		return nil, fmt.Errorf("no statistics: %s", strings.Join(errs, "; "))
	}
	for _, e := range errs {
		logf("stats: %s", e)
	}
	return s, nil
}

// String formats the statistics as aligned text.
func (s *CompanionStats) String() string {
	var b strings.Builder
	if s.HasCore || s.HasStorage {
		fmt.Fprintf(&b, "Battery:       %.2f V\n", float64(s.BatteryMV)/1000)
	}
	if s.HasCore {
		up := s.Uptime.Round(time.Second)
		days := int(up.Hours()) / 24
		fmt.Fprintf(&b, "Uptime:        %dd %s\n", days, (up - time.Duration(days)*24*time.Hour).String())
		fmt.Fprintf(&b, "TX queue:      %d (error flags: 0x%04x)\n", s.QueueLen, s.ErrorFlags)
	}
	if s.HasStorage {
		fmt.Fprintf(&b, "Storage:       %d of %d KB used\n", s.UsedKB, s.TotalKB)
	}
	if s.HasRadio {
		fmt.Fprintf(&b, "Noise floor:   %d dBm\n", s.NoiseFloor)
		fmt.Fprintf(&b, "Last RSSI/SNR: %d dBm / %.2f dB\n", s.LastRSSI, s.LastSNR)
		fmt.Fprintf(&b, "Airtime:       %s tx, %s rx\n", s.TxAirtime, s.RxAirtime)
	}
	if s.HasPackets {
		fmt.Fprintf(&b, "Packets:       %d received, %d sent\n", s.Received, s.Sent)
		fmt.Fprintf(&b, "  flood:       %d received, %d sent\n", s.RecvFlood, s.SentFlood)
		fmt.Fprintf(&b, "  direct:      %d received, %d sent\n", s.RecvDirect, s.SentDirect)
		if s.HasErrors {
			fmt.Fprintf(&b, "  rx errors:   %d\n", s.RecvErrors)
		}
	}
	return b.String()
}

// MarshalJSON reports durations in seconds; parts that were not read are omitted.
func (s *CompanionStats) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	if s.HasCore || s.HasStorage {
		out["battery_v"] = float64(s.BatteryMV) / 1000
	}
	if s.HasCore {
		out["uptime_s"] = int64(s.Uptime / time.Second)
		out["tx_queue_len"] = s.QueueLen
		out["error_flags"] = s.ErrorFlags
	}
	if s.HasStorage {
		out["storage_used_kb"] = s.UsedKB
		out["storage_total_kb"] = s.TotalKB
	}
	if s.HasRadio {
		out["noise_floor_dbm"] = s.NoiseFloor
		out["last_rssi_dbm"] = s.LastRSSI
		out["last_snr_db"] = s.LastSNR
		out["tx_airtime_s"] = int64(s.TxAirtime / time.Second)
		out["rx_airtime_s"] = int64(s.RxAirtime / time.Second)
	}
	if s.HasPackets {
		out["packets_received"] = s.Received
		out["packets_sent"] = s.Sent
		out["received_flood"] = s.RecvFlood
		out["received_direct"] = s.RecvDirect
		out["sent_flood"] = s.SentFlood
		out["sent_direct"] = s.SentDirect
		if s.HasErrors {
			out["receive_errors"] = s.RecvErrors
		}
	}
	return json.Marshal(out)
}

// Check returns one message per crossed limit.
func (s *CompanionStats) Check(t Thresholds) []string {
	var alerts []string
	if v := float64(s.BatteryMV) / 1000; s.HasCore && t.MinBatteryV > 0 && v < t.MinBatteryV {
		alerts = append(alerts, fmt.Sprintf("battery %.2f V is below %.2f V", v, t.MinBatteryV))
	}
	if s.HasRadio && t.MaxNoiseFloor != 0 && int(s.NoiseFloor) > t.MaxNoiseFloor {
		alerts = append(alerts, fmt.Sprintf("noise floor %d dBm is above %d dBm", s.NoiseFloor, t.MaxNoiseFloor))
	}
	return alerts
}
