package meshgore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// More companion settings. Layouts are from the firmware source
// (examples/companion_radio/MyMesh.cpp); integers are little-endian.
const (
	cmdGetDeviceTime   = 0x05
	cmdSetDeviceTime   = 0x06
	cmdSetRadioParams  = 0x0B
	cmdSetTuningParams = 0x15
	cmdSetOtherParams  = 0x26
	cmdSelfTelemetry   = 0x27
	cmdGetTuning       = 0x2B

	respCurrTime     = 0x09
	respTuningParams = 0x17
	pushTelemetry    = 0x8B
)

// DeviceTime reads the node's clock.
func (c *Client) DeviceTime() (time.Time, error) {
	f, err := c.Do([]byte{cmdGetDeviceTime}, respCurrTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("GET_DEVICE_TIME: %w", err)
	}
	if len(f) < 5 {
		return time.Time{}, fmt.Errorf("GET_DEVICE_TIME: reply too short: %d bytes", len(f))
	}
	return time.Unix(int64(binary.LittleEndian.Uint32(f[1:])), 0).UTC(), nil
}

// SetDeviceTime sets the node's clock. The firmware refuses to move it backwards
// (ILLEGAL_ARG), so a node whose clock is ahead cannot be corrected this way.
func (c *Client) SetDeviceTime(t time.Time) error {
	cmd := []byte{cmdSetDeviceTime, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(cmd[1:], uint32(t.Unix()))
	if _, err := c.Do(cmd, respOK); err != nil {
		return fmt.Errorf("SET_DEVICE_TIME: %w", err)
	}
	return nil
}

func buildSetRadio(freqMHz, bwKHz float64, sf, cr int) ([]byte, error) {
	switch {
	case freqMHz < 150 || freqMHz > 2500:
		return nil, fmt.Errorf("frequency %v MHz is out of range (150..2500)", freqMHz)
	case bwKHz < 7 || bwKHz > 500:
		return nil, fmt.Errorf("bandwidth %v kHz is out of range (7..500)", bwKHz)
	case sf < 5 || sf > 12:
		return nil, fmt.Errorf("spreading factor %d is out of range (5..12)", sf)
	case cr < 5 || cr > 8:
		return nil, fmt.Errorf("coding rate %d is out of range (5..8)", cr)
	}
	cmd := make([]byte, 11)
	cmd[0] = cmdSetRadioParams
	binary.LittleEndian.PutUint32(cmd[1:], uint32(math.Round(freqMHz*1000)))
	binary.LittleEndian.PutUint32(cmd[5:], uint32(math.Round(bwKHz*1000)))
	cmd[9], cmd[10] = byte(sf), byte(cr)
	return cmd, nil
}

// SetRadio changes the radio parameters (MHz, kHz, SF 5-12, CR 5-8). They are
// saved, and a node on other parameters than its neighbours cannot hear them.
// The Bluetooth link is not affected, so the change can be undone from here.
func (c *Client) SetRadio(freqMHz, bwKHz float64, sf, cr int) error {
	cmd, err := buildSetRadio(freqMHz, bwKHz, sf, cr)
	if err != nil {
		return fmt.Errorf("SET_RADIO_PARAMS: %w", err)
	}
	if _, err := c.Do(cmd, respOK); err != nil {
		return fmt.Errorf("SET_RADIO_PARAMS: %w", err)
	}
	return nil
}

// Tuning is the node's receive-delay and airtime-factor settings.
type Tuning struct {
	RxDelayBase   float64
	AirtimeFactor float64
}

// Tuning reads the node's tuning parameters.
func (c *Client) Tuning() (*Tuning, error) {
	f, err := c.Do([]byte{cmdGetTuning}, respTuningParams)
	if err != nil {
		return nil, fmt.Errorf("GET_TUNING_PARAMS: %w", err)
	}
	if len(f) < 9 {
		return nil, fmt.Errorf("GET_TUNING_PARAMS: reply too short: %d bytes", len(f))
	}
	le := binary.LittleEndian
	return &Tuning{float64(le.Uint32(f[1:])) / 1000, float64(le.Uint32(f[5:])) / 1000}, nil
}

// SetTuning sets both tuning parameters. The firmware does not check them but
// clamps them when it starts (receive delay 0..20, airtime factor 0..9), so
// they are checked here.
func (c *Client) SetTuning(t Tuning) error {
	if t.RxDelayBase < 0 || t.RxDelayBase > 20 {
		return fmt.Errorf("receive delay %v is out of range (0..20)", t.RxDelayBase)
	}
	if t.AirtimeFactor < 0 || t.AirtimeFactor > 9 {
		return fmt.Errorf("airtime factor %v is out of range (0..9)", t.AirtimeFactor)
	}
	cmd := []byte{cmdSetTuningParams, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(cmd[1:], uint32(math.Round(t.RxDelayBase*1000)))
	binary.LittleEndian.PutUint32(cmd[5:], uint32(math.Round(t.AirtimeFactor*1000)))
	if _, err := c.Do(cmd, respOK); err != nil {
		return fmt.Errorf("SET_TUNING_PARAMS: %w", err)
	}
	return nil
}

// OtherParams are the settings CMD_SET_OTHER_PARAMS writes. It overwrites all of
// them at once, so change one by reading the others from SelfInfo first.
type OtherParams struct {
	ManualAddContacts bool
	TelemetryMode     byte
	AdvLocPolicy      byte // 0 none, 1 share
	MultiAcks         byte
}

// OtherParamsOf returns the current values from the node's self info.
func OtherParamsOf(s *SelfInfo) OtherParams {
	return OtherParams{s.ManualAddContacts, s.TelemetryMode, s.AdvLocPolicy, s.MultiAcks}
}

// SetOtherParams writes the settings.
func (c *Client) SetOtherParams(p OtherParams) error {
	cmd := []byte{cmdSetOtherParams, 0, p.TelemetryMode, p.AdvLocPolicy, p.MultiAcks}
	if p.ManualAddContacts {
		cmd[1] = 1
	}
	if _, err := c.Do(cmd, respOK); err != nil {
		return fmt.Errorf("SET_OTHER_PARAMS: %w", err)
	}
	return nil
}

// Reading is one value from the node's own telemetry.
type Reading struct {
	Channel int    `json:"channel"`
	Name    string `json:"name"`
	Value   string `json:"value"`
}

// parseLPP reads telemetry in Cayenne LPP form: channel, type, data. It stops
// at a type it does not know and returns what it read, with the rest as hex.
func parseLPP(b []byte) (out []Reading, rest []byte) {
	for len(b) >= 2 {
		ch, typ, d := int(b[0]), b[1], b[2:]
		size := map[byte]int{0x00: 1, 0x01: 1, 0x02: 2, 0x03: 2, 0x65: 2, 0x66: 1, 0x67: 2, 0x68: 1, 0x71: 6, 0x73: 2, 0x74: 2, 0x86: 6, 0x88: 9}[typ]
		if size == 0 || len(d) < size {
			return out, b
		}
		s16 := func(i int) float64 { return float64(int16(binary.BigEndian.Uint16(d[i:]))) }
		s24 := func(i int) float64 {
			v := int32(d[i])<<16 | int32(d[i+1])<<8 | int32(d[i+2])
			if v&0x800000 != 0 {
				v -= 1 << 24
			}
			return float64(v)
		}
		r := Reading{Channel: ch}
		switch typ {
		case 0x74:
			r.Name, r.Value = "voltage", fmt.Sprintf("%.2f V", float64(binary.BigEndian.Uint16(d))/100)
		case 0x67:
			r.Name, r.Value = "temperature", fmt.Sprintf("%.1f C", s16(0)/10)
		case 0x68:
			r.Name, r.Value = "humidity", fmt.Sprintf("%.1f %%", float64(d[0])/2)
		case 0x73:
			r.Name, r.Value = "pressure", fmt.Sprintf("%.1f hPa", float64(binary.BigEndian.Uint16(d))/10)
		case 0x65:
			r.Name, r.Value = "illuminance", fmt.Sprintf("%d lx", binary.BigEndian.Uint16(d))
		case 0x02, 0x03:
			r.Name, r.Value = "analog", fmt.Sprintf("%.2f", s16(0)/100)
		case 0x88:
			r.Name, r.Value = "gps", fmt.Sprintf("%.4f,%.4f alt %.2f m", s24(0)/1e4, s24(3)/1e4, s24(6)/100)
		default:
			r.Name, r.Value = fmt.Sprintf("type 0x%02x", typ), fmt.Sprintf("% x", d[:size])
		}
		out = append(out, r)
		b = d[size:]
	}
	return out, b
}

// SelfTelemetry asks the node for its own sensor readings (battery, temperature,
// and GPS position on a node with a fix). The answer is a push frame.
func (c *Client) SelfTelemetry() ([]Reading, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.Push) > 0 && len(c.Push) > cap(c.Push)-1 { // never block on a full queue
		<-c.Push
	}
	if err := c.write([]byte{cmdSelfTelemetry, 0, 0, 0}); err != nil {
		return nil, fmt.Errorf("SEND_TELEMETRY_REQ: %w", err)
	}
	deadline := time.After(defaultTimeout)
	for {
		select {
		case f := <-c.Push:
			if f[0] != pushTelemetry || len(f) < 8 {
				continue
			}
			return telemetryReadings(f), nil
		case <-deadline:
			return nil, errors.New("SEND_TELEMETRY_REQ: no telemetry reply")
		}
	}
}

// ParseRadioSpec parses "freq,bw,sf,cr" (MHz, kHz, SF, CR).
func ParseRadioSpec(s string) (freq, bw float64, sf, cr int, err error) {
	p := strings.Split(s, ",")
	if len(p) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("want \"<freq MHz>,<bw kHz>,<sf>,<cr>\", not %q", s)
	}
	if _, err = fmt.Sscanf(strings.Join(p, " "), "%g %g %d %d", &freq, &bw, &sf, &cr); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("bad radio parameters %q: %v", s, err)
	}
	return
}

// telemetryReadings reads the sensor data of a telemetry push frame
// (0x8B, reserved byte, 6-byte key prefix, then the readings).
func telemetryReadings(f []byte) []Reading {
	out, rest := parseLPP(f[8:])
	if len(rest) > 0 {
		out = append(out, Reading{Name: "unparsed", Value: fmt.Sprintf("% x", rest)})
	}
	return out
}

// RequestTelemetry asks a repeater, room server or sensor for its sensor readings
// (battery, and whatever sensors it has). Like login and status, the answer
// arrives over the radio as a push frame, so the request is retried. The node
// answers only if its permissions allow it.
func (c *Client) RequestTelemetry(key [32]byte) ([]Reading, error) {
	cmd := append([]byte{cmdSelfTelemetry, 0, 0, 0}, key[:]...)
	for attempt := 1; attempt <= loginAttempts; attempt++ {
		suggested, err := c.sendAccepted(cmd)
		if err != nil {
			return nil, fmt.Errorf("SEND_TELEMETRY_REQ: %w", err)
		}
		wait := suggested * 4
		if wait < 8*time.Second {
			wait = 8 * time.Second
		}
		deadline := time.After(wait)
	recv:
		for {
			select {
			case f := <-c.Push:
				if f[0] != pushTelemetry || len(f) < 8 || string(f[2:8]) != string(key[:6]) {
					continue
				}
				return telemetryReadings(f), nil
			case <-deadline:
				break recv
			}
		}
		if attempt < loginAttempts {
			logf("no telemetry reply after %s, retrying (%d/%d)...", wait.Round(time.Second), attempt+1, loginAttempts)
		}
	}
	return nil, errors.New("no telemetry reply from repeater")
}
