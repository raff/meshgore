// Package meshgore is a client for MeshCore companion devices over Bluetooth LE,
// with support for administering remote repeaters through the companion.
//
// The protocol is described at https://docs.meshcore.io/companion_protocol/.
// Where that document is silent (contacts, login, status) the layouts follow
// the official meshcore_py library, and the code says so.
package meshgore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// Command codes (app -> firmware).
const (
	cmdAppStart    = 0x01
	cmdDeviceQuery = 0x16
	cmdGetChannel  = 0x1F
	cmdGetContacts = 0x04
	cmdSendChanMsg = 0x03
	cmdGetMessage  = 0x0A
)

// Response codes (firmware -> app, solicited).
const (
	respOK           = 0x00
	respError        = 0x01
	respSelfInfo     = 0x05
	respDeviceInfo   = 0x0D
	respChannel      = 0x12
	respContactStart = 0x02
	respContact      = 0x03
	respContactEnd   = 0x04
	respMsgSent      = 0x06
	respContactMsg   = 0x07
	respChannelMsg   = 0x08
	respNoMoreMsgs   = 0x0A
	respContactMsgV3 = 0x10
	respChannelMsgV3 = 0x11
)

// Push codes (firmware -> app, unsolicited) are >= 0x80.
const (
	pushAdvertisement = 0x80
	pushAck           = 0x82
	pushMsgWaiting    = 0x83
	pushLogData       = 0x88
)

var errCodes = map[byte]string{
	1: "UNSUPPORTED_CMD",
	2: "NOT_FOUND",
	3: "TABLE_FULL",
	4: "BAD_STATE",
	5: "FILE_IO_ERROR",
	6: "ILLEGAL_ARG",
}

const defaultTimeout = 5 * time.Second

// Client speaks the MeshCore companion protocol over an RX/TX characteristic pair.
type Client struct {
	rx *bluetooth.DeviceCharacteristic

	Info *DeviceInfo // set by DeviceQuery

	mu   sync.Mutex // serializes commands: one in flight at a time
	resp chan []byte
	Push chan []byte // unsolicited frames (codes >= 0x80)
}

// NewClient subscribes to tx notifications and returns a ready client.
func NewClient(rx, tx *bluetooth.DeviceCharacteristic) (*Client, error) {
	c := &Client{
		rx:   rx,
		resp: make(chan []byte, 256),
		Push: make(chan []byte, 32),
	}
	if err := tx.EnableNotifications(c.onNotify); err != nil {
		return nil, fmt.Errorf("enable notifications: %w", err)
	}
	return c, nil
}

// onNotify routes one frame to either the response or push channel.
func (c *Client) onNotify(buf []byte) {
	if len(buf) == 0 {
		return
	}
	frame := append([]byte(nil), buf...) // the callback's buffer may be reused
	if Debug {
		logf("<- % x", frame)
	}
	ch := c.resp
	if frame[0] >= 0x80 {
		ch = c.Push
	}
	select {
	case ch <- frame:
	default: // drop if the consumer is not keeping up
	}
}

// exchange sends cmd and feeds each solicited frame to handle until it reports
// done. Error frames from the device abort with an error. timeout applies to
// each frame, so a long multi-frame reply is fine as long as it keeps flowing.
func (c *Client) exchange(cmd []byte, handle func(f []byte) (done bool, err error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Discard stale responses from a previous timed-out command.
	for len(c.resp) > 0 {
		<-c.resp
	}

	if Debug {
		logf("-> % x", cmd)
	}
	if err := c.write(cmd); err != nil {
		// CoreBluetooth sometimes never confirms a write that the device did
		// receive, so a confirmation timeout is not fatal: wait for the response.
		if !strings.Contains(err.Error(), "timeout on Write") {
			return fmt.Errorf("write: %w", err)
		}
		logf("write not confirmed; waiting for response anyway")
	}

	for {
		select {
		case f := <-c.resp:
			if f[0] == respError {
				code := byte(0)
				if len(f) > 1 {
					code = f[1]
				}
				name := errCodes[code]
				if name == "" {
					name = "unknown"
				}
				return fmt.Errorf("device error %d (%s)", code, name)
			}
			done, err := handle(f)
			if err != nil || done {
				return err
			}
		case <-time.After(defaultTimeout):
			return errors.New("timeout waiting for response")
		}
	}
}

// Do sends cmd and waits for a response whose first byte is want.
func (c *Client) Do(cmd []byte, want byte) ([]byte, error) {
	var got []byte
	err := c.exchange(cmd, func(f []byte) (bool, error) {
		if f[0] != want {
			return false, nil // unexpected type: keep waiting
		}
		got = f
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("0x%02x: %w", cmd[0], err)
	}
	return got, nil
}

// SelfInfo is the response to APP_START.
type SelfInfo struct {
	AdvType           byte
	TxPower           byte
	MaxTxPower        byte
	PublicKey         [32]byte
	Lat, Lon          float64
	MultiAcks         byte
	AdvLocPolicy      byte
	TelemetryMode     byte
	ManualAddContacts bool
	FreqMHz           float64
	BandwidthKHz      float64
	SF                byte
	CR                byte
	Name              string
}

// AppStart identifies the app to the device and returns its self info.
func (c *Client) AppStart(appName string) (*SelfInfo, error) {
	cmd := append([]byte{cmdAppStart, 0, 0, 0, 0, 0, 0, 0}, appName...)
	f, err := c.Do(cmd, respSelfInfo)
	if err != nil {
		return nil, fmt.Errorf("APP_START: %w", err)
	}
	return parseSelfInfo(f)
}

func parseSelfInfo(f []byte) (*SelfInfo, error) {
	if len(f) < 58 {
		return nil, fmt.Errorf("SELF_INFO too short: %d bytes", len(f))
	}
	le := binary.LittleEndian
	s := &SelfInfo{
		AdvType:           f[1],
		TxPower:           f[2],
		MaxTxPower:        f[3],
		Lat:               float64(int32(le.Uint32(f[36:]))) / 1e6,
		Lon:               float64(int32(le.Uint32(f[40:]))) / 1e6,
		MultiAcks:         f[44],
		AdvLocPolicy:      f[45],
		TelemetryMode:     f[46],
		ManualAddContacts: f[47] != 0,
		FreqMHz:           float64(le.Uint32(f[48:])) / 1000.0,
		BandwidthKHz:      float64(le.Uint32(f[52:])) / 1000.0,
		SF:                f[56],
		CR:                f[57],
		Name:              string(f[58:]),
	}
	copy(s.PublicKey[:], f[4:36])
	return s, nil
}

// DeviceInfo is the response to DEVICE_QUERY.
type DeviceInfo struct {
	FirmwareVer   byte
	MaxContacts   int
	MaxChannels   int
	BLEPin        uint32
	FirmwareBuild string
	Model         string
	Version       string
}

// DeviceQuery asks the device for firmware/model details.
func (c *Client) DeviceQuery() (*DeviceInfo, error) {
	f, err := c.Do([]byte{cmdDeviceQuery, 0x03}, respDeviceInfo)
	if err != nil {
		return nil, fmt.Errorf("DEVICE_QUERY: %w", err)
	}
	d, err := parseDeviceInfo(f)
	if err == nil {
		c.Info = d
	}
	return d, err
}

func parseDeviceInfo(f []byte) (*DeviceInfo, error) {
	if len(f) < 2 {
		return nil, errors.New("DEVICE_INFO too short")
	}
	d := &DeviceInfo{FirmwareVer: f[1]}
	if d.FirmwareVer < 3 {
		return d, nil // older layouts carry nothing more that we parse
	}
	if len(f) < 80 {
		return nil, fmt.Errorf("DEVICE_INFO too short: %d bytes", len(f))
	}
	d.MaxContacts = int(f[2]) * 2
	d.MaxChannels = int(f[3])
	d.BLEPin = binary.LittleEndian.Uint32(f[4:])
	d.FirmwareBuild = cstr(f[8:20])
	d.Model = cstr(f[20:60])
	d.Version = cstr(f[60:80])
	return d, nil
}

// cstr returns b up to the first NUL.
func cstr(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// pairingWait is how long write keeps retrying while the user pairs the device.
const pairingWait = 60 * time.Second

// write sends one frame. If the device requires encryption (ATT 0x0f), the OS
// starts pairing and prompts for the BLE PIN; keep retrying until it succeeds.
func (c *Client) write(cmd []byte) error {
	deadline := time.Now().Add(pairingWait)
	warned := false
	for {
		_, err := c.rx.Write(cmd)
		if err == nil {
			return nil
		}
		logf("write failed (%s): %v", time.Until(deadline).Round(time.Second), err)
		// A write that blocks behind the pairing dialog times out instead; that
		// is returned so the caller can reconnect with the new bond.
		msg := err.Error()
		retryable := strings.Contains(msg, "Insufficient Encryption")
		if !retryable || time.Now().After(deadline) {
			return err
		}
		if !warned {
			logf("Device requires pairing: enter the BLE PIN in the system dialog (default 123456)...")
			warned = true
		}
		time.Sleep(2 * time.Second)
	}
}

// Channel is one channel slot.
type Channel struct {
	Index  int
	Name   string
	Secret [16]byte
}

// Empty reports whether the slot is unused.
func (ch *Channel) Empty() bool { return ch.Name == "" }

// GetChannel reads channel slot idx.
func (c *Client) GetChannel(idx int) (*Channel, error) {
	f, err := c.Do([]byte{cmdGetChannel, byte(idx)}, respChannel)
	if err != nil {
		return nil, fmt.Errorf("GET_CHANNEL %d: %w", idx, err)
	}
	if len(f) < 50 {
		return nil, fmt.Errorf("CHANNEL_INFO too short: %d bytes", len(f))
	}
	ch := &Channel{Index: int(f[1]), Name: cstr(f[2:34])}
	copy(ch.Secret[:], f[34:50])
	return ch, nil
}

// Channels reads every slot up to the device's max and returns the used ones.
func (c *Client) Channels() ([]*Channel, error) {
	if c.Info == nil {
		return nil, errors.New("call DeviceQuery first")
	}
	var out []*Channel
	for i := 0; i < c.Info.MaxChannels; i++ {
		ch, err := c.GetChannel(i)
		if err != nil {
			return out, err
		}
		if !ch.Empty() {
			out = append(out, ch)
		}
	}
	return out, nil
}

// Contact is one entry of the device's contact list.
type Contact struct {
	PublicKey  [32]byte
	Type       byte
	Flags      byte
	OutPathLen int // -1 means flood (no known path)
	Name       string
	LastAdvert time.Time
	Lat, Lon   float64
	LastMod    uint32
}

// TypeName names the contact type.
func (ct *Contact) TypeName() string {
	switch ct.Type {
	case 1:
		return "chat"
	case 2:
		return "repeater"
	case 3:
		return "room"
	case 4:
		return "sensor"
	}
	return fmt.Sprintf("type%d", ct.Type)
}

const contactFrameLen = 148 // 1 + 32 + 1 + 1 + 1 + 64 + 32 + 4 + 4 + 4 + 4

// parseContact layout is not in the protocol doc; it follows meshcore_py.
func parseContact(f []byte) (*Contact, error) {
	if len(f) < contactFrameLen {
		return nil, fmt.Errorf("CONTACT too short: %d bytes", len(f))
	}
	le := binary.LittleEndian
	ct := &Contact{
		Type:       f[33],
		Flags:      f[34],
		OutPathLen: int(f[35] & 0x3F),
		Name:       cstr(f[100:132]),
		LastAdvert: time.Unix(int64(le.Uint32(f[132:])), 0),
		Lat:        float64(int32(le.Uint32(f[136:]))) / 1e6,
		Lon:        float64(int32(le.Uint32(f[140:]))) / 1e6,
		LastMod:    le.Uint32(f[144:]),
	}
	if f[35] == 0xFF {
		ct.OutPathLen = -1
	}
	copy(ct.PublicKey[:], f[1:33])
	return ct, nil
}

// GetContacts downloads the full contact list.
func (c *Client) GetContacts() ([]*Contact, error) {
	contacts, _, err := c.GetContactsSince(0)
	return contacts, err
}

// GetContactsSince downloads the contacts added or changed after since, a
// LastMod value from an earlier call (0 means all of them). It also returns the
// newest LastMod, to pass as since next time. Contacts deleted on the device
// are not reported, so an incremental sync cannot remove them from a local copy;
// do a full sync (since 0) now and then for that.
func (c *Client) GetContactsSince(since uint32) (contacts []*Contact, lastMod uint32, err error) {
	cmd := []byte{cmdGetContacts}
	if since > 0 {
		cmd = binary.LittleEndian.AppendUint32(cmd, since)
	}
	err = c.exchange(cmd, func(f []byte) (bool, error) {
		switch f[0] {
		case respContactStart:
			return false, nil
		case respContact:
			ct, err := parseContact(f)
			if err != nil {
				return false, err
			}
			contacts = append(contacts, ct)
		case respContactEnd:
			lastMod = parseContactEnd(f)
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return contacts, 0, fmt.Errorf("GET_CONTACTS: %w", err)
	}
	// Never go backwards, whatever the end frame says.
	for _, ct := range contacts {
		if ct.LastMod > lastMod {
			lastMod = ct.LastMod
		}
	}
	if lastMod < since {
		lastMod = since
	}
	return contacts, lastMod, nil
}

// parseContactEnd reads the newest-modification timestamp from the end-of-list
// frame (layout from meshcore_py: code, uint32 LE). It returns 0 if absent.
func parseContactEnd(f []byte) uint32 {
	if len(f) < 5 {
		return 0
	}
	return binary.LittleEndian.Uint32(f[1:])
}

// SendChannelMessage posts text to channel idx. It returns the expected-ACK tag.
func (c *Client) SendChannelMessage(idx int, text string) (tag uint32, flood bool, err error) {
	cmd := []byte{cmdSendChanMsg, 0x00, byte(idx), 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(cmd[3:], uint32(time.Now().Unix()))
	cmd = append(cmd, text...)
	// The doc says MSG_SENT (0x06); some firmware answers with a plain OK (0x00).
	err = c.exchange(cmd, func(f []byte) (bool, error) {
		switch f[0] {
		case respMsgSent:
			if len(f) < 10 {
				return false, fmt.Errorf("MSG_SENT too short: %d bytes", len(f))
			}
			tag, flood = binary.LittleEndian.Uint32(f[2:]), f[1] == 1
			return true, nil
		case respOK:
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return 0, false, fmt.Errorf("SEND_CHANNEL_MESSAGE: %w", err)
	}
	return tag, flood, nil
}

// Message is a received contact (direct) or channel message.
type Message struct {
	Channel    bool
	ChannelIdx int     // valid when Channel
	From       [6]byte // sender public key prefix; valid when !Channel
	PathLen    byte    // 0xFF means direct
	TxtType    byte
	Time       time.Time
	SNR        float64 // dB; only in V3 frames, else 0
	Text       string
}

// parseMessage layouts follow the protocol doc.
func parseMessage(f []byte) (*Message, error) {
	le := binary.LittleEndian
	m := &Message{}
	var tsOff, textOff int
	switch f[0] {
	case respContactMsg:
		if len(f) < 13 {
			return nil, fmt.Errorf("CONTACT_MSG too short: %d bytes", len(f))
		}
		copy(m.From[:], f[1:7])
		m.PathLen, m.TxtType = f[7], f[8]
		tsOff, textOff = 9, 13
	case respContactMsgV3:
		if len(f) < 16 {
			return nil, fmt.Errorf("CONTACT_MSG_V3 too short: %d bytes", len(f))
		}
		m.SNR = float64(int8(f[1])) / 4
		copy(m.From[:], f[4:10])
		m.PathLen, m.TxtType = f[10], f[11]
		tsOff, textOff = 12, 16
	case respChannelMsg:
		if len(f) < 8 {
			return nil, fmt.Errorf("CHANNEL_MSG too short: %d bytes", len(f))
		}
		m.Channel, m.ChannelIdx = true, int(f[1])
		m.PathLen, m.TxtType = f[2], f[3]
		tsOff, textOff = 4, 8
	case respChannelMsgV3:
		if len(f) < 11 {
			return nil, fmt.Errorf("CHANNEL_MSG_V3 too short: %d bytes", len(f))
		}
		m.SNR = float64(int8(f[1])) / 4
		m.Channel, m.ChannelIdx = true, int(f[4])
		m.PathLen, m.TxtType = f[5], f[6]
		tsOff, textOff = 7, 11
	default:
		return nil, fmt.Errorf("not a message frame: 0x%02x", f[0])
	}
	m.Time = time.Unix(int64(le.Uint32(f[tsOff:])), 0)
	// Signed contact messages carry a 4-byte signature before the text.
	if !m.Channel && m.TxtType == 2 {
		textOff += 4
	}
	if textOff > len(f) {
		textOff = len(f)
	}
	m.Text = string(f[textOff:])
	return m, nil
}

// SyncMessages fetches queued messages until the device reports none left.
func (c *Client) SyncMessages() ([]*Message, error) {
	var out []*Message
	for {
		var msg *Message
		var more bool
		err := c.exchange([]byte{cmdGetMessage}, func(f []byte) (bool, error) {
			switch f[0] {
			case respNoMoreMsgs:
				return true, nil
			case respContactMsg, respContactMsgV3, respChannelMsg, respChannelMsgV3:
				m, err := parseMessage(f)
				if err != nil {
					return false, err
				}
				msg, more = m, true
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			return out, fmt.Errorf("GET_MESSAGE: %w", err)
		}
		if !more {
			return out, nil
		}
		out = append(out, msg)
	}
}

// PushMsgWaiting is the first byte of the unsolicited frame announcing that
// messages are queued; read them with SyncMessages.
const PushMsgWaiting = pushMsgWaiting
