package meshgore

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Remote-administration commands. The doc does not cover these; the layouts
// follow meshcore_py.
const (
	cmdSendTxtMsg = 0x02
	cmdSendLogin  = 0x1A
	cmdLogout     = 0x1D

	pushLoginSuccess = 0x85
	pushLoginFailed  = 0x86

	txtTypeCLIData = 0x01 // text type used for repeater CLI commands
)

// sendAccepted runs cmd and succeeds on MSG_SENT or OK (firmware varies). It
// returns the suggested timeout from MSG_SENT, or 0 if the reply had none.
func (c *Client) sendAccepted(cmd []byte) (suggested time.Duration, err error) {
	err = c.exchange(cmd, func(f []byte) (bool, error) {
		switch f[0] {
		case respMsgSent:
			if len(f) >= 10 {
				suggested = time.Duration(binary.LittleEndian.Uint32(f[6:])) * time.Millisecond
			}
			return true, nil
		case respOK:
			return true, nil
		}
		return false, nil
	})
	return suggested, err
}

// loginAttempts is how many times Login sends the request. A single flood
// transmission is easily lost.
const loginAttempts = 3

// Login authenticates to a remote repeater or room server identified by its
// full public key, and waits for the result, which arrives over the radio as a
// push frame some time after the node accepts the request.
func (c *Client) Login(key [32]byte, password string) error {
	cmd := append([]byte{cmdSendLogin}, key[:]...)
	cmd = append(cmd, password...)
	sub := c.Subscribe(pushLoginSuccess, pushLoginFailed)
	defer sub.Close()
	for attempt := 1; attempt <= loginAttempts; attempt++ {
		if attempt > 1 {
			// The first try goes by the stored route, which may be stale; forget it so
			// the retries flood, as a message retry does.
			if err := c.ResetPath(key); err != nil {
				logf("%v; continuing", err)
			}
		}
		suggested, err := c.sendAccepted(cmd)
		if err != nil {
			return fmt.Errorf("SEND_LOGIN: %w", err)
		}
		wait := suggested * 4 // the device's hint is in ms; leave generous slack
		if wait < 8*time.Second {
			wait = 8 * time.Second
		}
		if wait > 30*time.Second {
			wait = 30 * time.Second // a long hint comes from a long stored route, the thing that failed
		}
		ok, err := c.awaitLogin(sub, key, wait)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if attempt < loginAttempts {
			logf("no login reply after %s, retrying (%d/%d)...", wait.Round(time.Second), attempt+1, loginAttempts)
		}
	}
	return errors.New("no login reply: wrong password (repeaters stay silent on a bad one) or repeater out of range")
}

// awaitLogin waits for the login result push from key. It returns false on
// timeout, and an error if the repeater rejected the login.
func (c *Client) awaitLogin(sub *Sub, key [32]byte, wait time.Duration) (bool, error) {
	deadline := time.After(wait)
	for {
		select {
		case f := <-sub.C:
			if f[0] != pushLoginSuccess && f[0] != pushLoginFailed {
				continue
			}
			// Both carry the repeater's key prefix at bytes 2..8 when present.
			if len(f) >= 8 && string(f[2:8]) != string(key[:6]) {
				continue // a reply for some other node
			}
			if f[0] == pushLoginFailed {
				return false, errors.New("login failed (wrong password)")
			}
			return true, nil
		case <-deadline:
			return false, nil
		}
	}
}

// Logout ends the session with a repeater.
func (c *Client) Logout(key [32]byte) error {
	_, err := c.sendAccepted(append([]byte{cmdLogout}, key[:]...))
	return err
}

// SendCLI sends a CLI command to a logged-in repeater. The reply arrives later
// as a contact message from the repeater.
func (c *Client) SendCLI(key [32]byte, command string) error {
	cmd := []byte{cmdSendTxtMsg, txtTypeCLIData, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(cmd[3:], uint32(time.Now().Unix()))
	cmd = append(cmd, key[:6]...) // text messages address the 6-byte prefix
	cmd = append(cmd, command...)
	if _, err := c.sendAccepted(cmd); err != nil {
		return fmt.Errorf("SEND_TXT_MSG: %w", err)
	}
	return nil
}

// ErrNoMatch is returned (wrapped) by FindContact when nothing matches.
var ErrNoMatch = errors.New("no contact matches")

// FindContact resolves arg to a contact. arg may be a full 64-char hex key,
// a hex key prefix, or a contact name (case-insensitive).
func FindContact(contacts []*Contact, arg string) (*Contact, error) {
	var matches []*Contact
	lower := strings.ToLower(arg)
	_, hexErr := hex.DecodeString(lower)
	isHex := hexErr == nil && len(lower) >= 6
	for _, ct := range contacts {
		switch {
		case isHex && strings.HasPrefix(hex.EncodeToString(ct.PublicKey[:]), lower):
			matches = append(matches, ct)
		case strings.EqualFold(ct.Name, arg):
			matches = append(matches, ct)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w %q", ErrNoMatch, arg)
	case 1:
		return matches[0], nil
	}
	names := make([]string, len(matches))
	for i, ct := range matches {
		names[i] = fmt.Sprintf("%s (%x)", ct.Name, ct.PublicKey[:6])
	}
	return nil, fmt.Errorf("%q is ambiguous: %s", arg, strings.Join(names, ", "))
}

// CollectReplies syncs messages until it has seen at least one text from the
// given node and then nothing more for quiet, or until timeout. It returns the
// texts in order. Messages from other senders go to other.
func (c *Client) CollectReplies(from [6]byte, timeout, quiet time.Duration, other func(*Message)) []string {
	sub := c.Subscribe(pushMsgWaiting)
	defer sub.Close()
	var replies []string
	deadline := time.After(timeout)
	poll := time.NewTicker(2 * time.Second) // in case a messages-waiting push is missed
	defer poll.Stop()
	var lastReply time.Time
	for {
		select {
		case <-sub.C:
		case <-poll.C:
			if len(replies) > 0 && time.Since(lastReply) >= quiet {
				return replies
			}
		case <-deadline:
			return replies
		}
		msgs, err := c.SyncMessages()
		if err != nil {
			logf("message sync: %v", err)
		}
		for _, m := range msgs {
			if !m.Channel && m.From == from {
				replies = append(replies, m.Text)
				lastReply = time.Now()
			} else if other != nil {
				other(m)
			}
		}
	}
}

const (
	cmdStatusReq   = 0x1B
	pushStatusResp = 0x87
)

// RepeaterStatus is a repeater's self-reported statistics. The layout is not
// in the protocol doc; it follows meshcore_py.
type RepeaterStatus struct {
	BatteryMV   uint16
	TxQueueLen  uint16
	NoiseFloor  int16 // dBm
	LastRSSI    int16 // dBm
	Received    uint32
	Sent        uint32
	TxAirtime   time.Duration
	Uptime      time.Duration
	SentFlood   uint32
	SentDirect  uint32
	RecvFlood   uint32
	RecvDirect  uint32
	FullEvents  uint16
	LastSNR     float64 // dB
	DirectDups  uint16
	FloodDups   uint16
	RxAirtime   time.Duration
	RecvErrors  uint32
	HasRecvErrs bool // only firmware v1.12.0+ reports it
}

const statusHeaderLen = 8 // code, reserved, 6-byte key prefix
const statusBodyLen = 52  // fields up to rx_airtime; recv_errors adds 4 more

func parseStatus(f []byte) (*RepeaterStatus, error) {
	if len(f) < statusHeaderLen+statusBodyLen {
		return nil, fmt.Errorf("STATUS_RESPONSE too short: %d bytes", len(f))
	}
	le := binary.LittleEndian
	b := f[statusHeaderLen:]
	s := &RepeaterStatus{
		BatteryMV:  le.Uint16(b[0:]),
		TxQueueLen: le.Uint16(b[2:]),
		NoiseFloor: int16(le.Uint16(b[4:])),
		LastRSSI:   int16(le.Uint16(b[6:])),
		Received:   le.Uint32(b[8:]),
		Sent:       le.Uint32(b[12:]),
		TxAirtime:  time.Duration(le.Uint32(b[16:])) * time.Second,
		Uptime:     time.Duration(le.Uint32(b[20:])) * time.Second,
		SentFlood:  le.Uint32(b[24:]),
		SentDirect: le.Uint32(b[28:]),
		RecvFlood:  le.Uint32(b[32:]),
		RecvDirect: le.Uint32(b[36:]),
		FullEvents: le.Uint16(b[40:]),
		LastSNR:    float64(int16(le.Uint16(b[42:]))) / 4,
		DirectDups: le.Uint16(b[44:]),
		FloodDups:  le.Uint16(b[46:]),
		RxAirtime:  time.Duration(le.Uint32(b[48:])) * time.Second,
	}
	if len(b) >= statusBodyLen+4 {
		s.RecvErrors, s.HasRecvErrs = le.Uint32(b[52:]), true
	}
	return s, nil
}

// RequestStatus asks a repeater for its statistics. Like login, the answer
// arrives over the radio as a push frame, so the request is retried.
func (c *Client) RequestStatus(key [32]byte) (*RepeaterStatus, error) {
	cmd := append([]byte{cmdStatusReq}, key[:]...)
	sub := c.Subscribe(pushStatusResp)
	defer sub.Close()
	for attempt := 1; attempt <= loginAttempts; attempt++ {
		suggested, err := c.sendAccepted(cmd)
		if err != nil {
			return nil, fmt.Errorf("STATUS_REQ: %w", err)
		}
		wait := suggested * 4
		if wait < 8*time.Second {
			wait = 8 * time.Second
		}
		deadline := time.After(wait)
	recv:
		for {
			select {
			case f := <-sub.C:
				if f[0] != pushStatusResp || len(f) < statusHeaderLen || string(f[2:8]) != string(key[:6]) {
					continue
				}
				return parseStatus(f)
			case <-deadline:
				break recv
			}
		}
		if attempt < loginAttempts {
			logf("no status reply after %s, retrying (%d/%d)...", wait.Round(time.Second), attempt+1, loginAttempts)
		}
	}
	return nil, errors.New("no status reply from repeater")
}

// String formats the status as aligned text.
func (s *RepeaterStatus) String() string {
	up := s.Uptime.Round(time.Second)
	days := int(up.Hours()) / 24
	var b strings.Builder
	fmt.Fprintf(&b, "Battery:       %.2f V\n", float64(s.BatteryMV)/1000)
	fmt.Fprintf(&b, "Uptime:        %dd %s\n", days, (up - time.Duration(days)*24*time.Hour).String())
	fmt.Fprintf(&b, "Noise floor:   %d dBm\n", s.NoiseFloor)
	fmt.Fprintf(&b, "Last RSSI/SNR: %d dBm / %.2f dB\n", s.LastRSSI, s.LastSNR)
	fmt.Fprintf(&b, "TX queue:      %d (full events: %d)\n", s.TxQueueLen, s.FullEvents)
	fmt.Fprintf(&b, "Packets:       %d received, %d sent\n", s.Received, s.Sent)
	fmt.Fprintf(&b, "  flood:       %d received, %d sent\n", s.RecvFlood, s.SentFlood)
	fmt.Fprintf(&b, "  direct:      %d received, %d sent\n", s.RecvDirect, s.SentDirect)
	fmt.Fprintf(&b, "  duplicates:  %d flood, %d direct\n", s.FloodDups, s.DirectDups)
	if s.HasRecvErrs {
		fmt.Fprintf(&b, "  rx errors:   %d\n", s.RecvErrors)
	}
	fmt.Fprintf(&b, "Airtime:       %s tx, %s rx\n", s.TxAirtime, s.RxAirtime)
	return b.String()
}

// MarshalJSON reports durations in seconds so consumers need no parsing.
func (s *RepeaterStatus) MarshalJSON() ([]byte, error) {
	out := struct {
		BatteryV      float64 `json:"battery_v"`
		UptimeS       int64   `json:"uptime_s"`
		NoiseFloorDBm int16   `json:"noise_floor_dbm"`
		LastRSSIDBm   int16   `json:"last_rssi_dbm"`
		LastSNRDB     float64 `json:"last_snr_db"`
		TxQueueLen    uint16  `json:"tx_queue_len"`
		QueueFull     uint16  `json:"queue_full_events"`
		Received      uint32  `json:"packets_received"`
		Sent          uint32  `json:"packets_sent"`
		RecvFlood     uint32  `json:"received_flood"`
		RecvDirect    uint32  `json:"received_direct"`
		SentFlood     uint32  `json:"sent_flood"`
		SentDirect    uint32  `json:"sent_direct"`
		FloodDups     uint16  `json:"flood_duplicates"`
		DirectDups    uint16  `json:"direct_duplicates"`
		RecvErrors    *uint32 `json:"receive_errors,omitempty"`
		TxAirtimeS    int64   `json:"tx_airtime_s"`
		RxAirtimeS    int64   `json:"rx_airtime_s"`
	}{
		BatteryV:      float64(s.BatteryMV) / 1000,
		UptimeS:       int64(s.Uptime / time.Second),
		NoiseFloorDBm: s.NoiseFloor, LastRSSIDBm: s.LastRSSI, LastSNRDB: s.LastSNR,
		TxQueueLen: s.TxQueueLen, QueueFull: s.FullEvents,
		Received: s.Received, Sent: s.Sent,
		RecvFlood: s.RecvFlood, RecvDirect: s.RecvDirect,
		SentFlood: s.SentFlood, SentDirect: s.SentDirect,
		FloodDups: s.FloodDups, DirectDups: s.DirectDups,
		TxAirtimeS: int64(s.TxAirtime / time.Second),
		RxAirtimeS: int64(s.RxAirtime / time.Second),
	}
	if s.HasRecvErrs {
		out.RecvErrors = &s.RecvErrors
	}
	return json.Marshal(out)
}

// Thresholds are limits for Check; a zero value disables that limit.
type Thresholds struct {
	MinBatteryV   float64 // alert below this battery voltage
	MaxNoiseFloor int     // alert above this noise floor in dBm (e.g. -100)
}

// Check returns one message per crossed limit.
func (s *RepeaterStatus) Check(t Thresholds) []string {
	var alerts []string
	if v := float64(s.BatteryMV) / 1000; t.MinBatteryV > 0 && v < t.MinBatteryV {
		alerts = append(alerts, fmt.Sprintf("battery %.2f V is below %.2f V", v, t.MinBatteryV))
	}
	if t.MaxNoiseFloor != 0 && int(s.NoiseFloor) > t.MaxNoiseFloor {
		alerts = append(alerts, fmt.Sprintf("noise floor %d dBm is above %d dBm", s.NoiseFloor, t.MaxNoiseFloor))
	}
	return alerts
}

// IsReadOnlyCommand reports whether a repeater CLI command is known to only read
// state. It is an allowlist: anything not listed is treated as unsafe, because
// an unknown command could change configuration or reboot the node.
func IsReadOnlyCommand(command string) bool {
	f := strings.Fields(strings.ToLower(command))
	if len(f) == 0 {
		return false
	}
	switch f[0] {
	case "stats-core", "stats-radio", "stats-packets", "log":
		// Serial console only: statistics and a dump of the captured log.
		return len(f) == 1
	case "ver", "board", "clock", "neighbors":
		return len(f) == 1 // "clock sync" and "neighbors" with arguments are not reads
	case "get":
		// get <setting> only reads. get prv.key would reveal the private key.
		return len(f) == 2 && f[1] != "prv.key"
	case "region":
		switch {
		case len(f) == 1: // list regions
			return true
		case f[1] == "get" || f[1] == "list":
			return len(f) == 3
		case f[1] == "home" || f[1] == "default":
			return len(f) == 2 // with a name these set the value
		}
	case "gps":
		// "gps" and "gps advert" report. "gps on|off", "gps sync", "gps setloc"
		// and "gps advert <policy>" change the clock, the location or settings.
		return len(f) == 1 || (len(f) == 2 && f[1] == "advert")
	case "sensor":
		// "sensor list [start]" and "sensor get <key>" read; "sensor set" writes.
		if len(f) >= 2 && f[1] == "list" {
			if len(f) == 2 {
				return true
			}
			_, err := strconv.Atoi(f[2])
			return len(f) == 3 && err == nil
		}
		return len(f) == 3 && f[1] == "get"
	}
	return false
}
