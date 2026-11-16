package meshgore

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Settings and housekeeping commands. Layouts are from the firmware source
// (examples/companion_radio/MyMesh.cpp, src/Mesh.cpp).
const (
	cmdRemoveContact    = 0x0F
	cmdSetAutoAdd       = 0x3A
	cmdGetAutoAdd       = 0x3B
	cmdSetPathHashMode  = 0x3D
	cmdSendControlData  = 0x37
	respAutoAddConfig   = 0x19
	pushControlData     = 0x8E
	ctlNodeDiscoverReq  = 0x80
	ctlNodeDiscoverResp = 0x90
)

// Advert types, as the firmware numbers them.
const (
	TypeChat     = 1
	TypeRepeater = 2
	TypeRoom     = 3
	TypeSensor   = 4
)

// Bits of the auto-add configuration. The bit for advert type T is 1<<T.
const (
	AutoAddOverwriteOldest = 0x01
	AutoAddChat            = 1 << TypeChat
	AutoAddRepeater        = 1 << TypeRepeater
	AutoAddRoom            = 1 << TypeRoom
	AutoAddSensor          = 1 << TypeSensor
	AutoAddAllTypes        = AutoAddChat | AutoAddRepeater | AutoAddRoom | AutoAddSensor
)

// AutoAdd is which adverts the node saves as contacts by itself. The type bits
// only count while "manual add" is on (SET_OTHER_PARAMS): with it off the node
// adds every type. MaxHops 0 means no limit.
type AutoAdd struct {
	Config  byte
	MaxHops byte
}

// GetAutoAdd reads the auto-add settings.
func (c *Client) GetAutoAdd() (AutoAdd, error) {
	f, err := c.Do([]byte{cmdGetAutoAdd}, respAutoAddConfig)
	if err != nil {
		return AutoAdd{}, fmt.Errorf("GET_AUTOADD_CONFIG: %w", err)
	}
	if len(f) < 3 {
		return AutoAdd{}, fmt.Errorf("GET_AUTOADD_CONFIG: reply too short: %d bytes", len(f))
	}
	return AutoAdd{f[1], f[2]}, nil
}

// SetAutoAdd writes the auto-add settings. The firmware limits MaxHops to 64.
func (c *Client) SetAutoAdd(a AutoAdd) error {
	if a.MaxHops > 64 {
		return fmt.Errorf("SET_AUTOADD_CONFIG: max hops %d is out of range (0..64)", a.MaxHops)
	}
	if _, err := c.Do([]byte{cmdSetAutoAdd, a.Config, a.MaxHops}, respOK); err != nil {
		return fmt.Errorf("SET_AUTOADD_CONFIG: %w", err)
	}
	return nil
}

// Describe lists what the settings mean, given whether manual add is on.
func (a AutoAdd) Describe(manual bool) string {
	var types []string
	for _, t := range []struct {
		bit  byte
		name string
	}{{AutoAddChat, "chat"}, {AutoAddRepeater, "repeater"}, {AutoAddRoom, "room"}, {AutoAddSensor, "sensor"}} {
		if !manual || a.Config&t.bit != 0 {
			types = append(types, t.name)
		}
	}
	added := strings.Join(types, ", ")
	if added == "" {
		added = "nothing (manual)"
	}
	hops := "any distance"
	if a.MaxHops > 0 {
		hops = fmt.Sprintf("up to %d hops", a.MaxHops)
	}
	return fmt.Sprintf("auto-add: %s; %s; overwrite oldest when full: %v", added, hops, a.Config&AutoAddOverwriteOldest != 0)
}

// SetPathHashMode sets how many bytes of each hop's hash a flood packet carries:
// mode+1, mode 0..2. The current value is in DeviceInfo.PathHashMode.
func (c *Client) SetPathHashMode(mode int) error {
	if mode < 0 || mode > 2 {
		return fmt.Errorf("SET_PATH_HASH_MODE: mode %d is out of range (0..2)", mode)
	}
	if _, err := c.Do([]byte{cmdSetPathHashMode, 0, byte(mode)}, respOK); err != nil {
		return fmt.Errorf("SET_PATH_HASH_MODE: %w", err)
	}
	return nil
}

// RemoveContact deletes a contact from the node. The node re-adds it when it
// hears its advert again, unless auto-add is off for that type.
func (c *Client) RemoveContact(key [32]byte) error {
	if _, err := c.Do(append([]byte{cmdRemoveContact}, key[:]...), respOK); err != nil {
		return fmt.Errorf("REMOVE_CONTACT: %w", err)
	}
	return nil
}

// ClearChannel empties a channel slot. The firmware has no delete command; it
// accepts an empty name with an all-zero secret, and then ignores the slot.
func (c *Client) ClearChannel(idx int) error {
	if idx < 0 || idx > 255 {
		return fmt.Errorf("channel slot %d is out of range", idx)
	}
	if c.Info != nil && idx >= c.Info.MaxChannels {
		return fmt.Errorf("SET_CHANNEL: slot %d does not exist; the device has %d", idx, c.Info.MaxChannels)
	}
	cmd := make([]byte, 50) // code, slot, 32 bytes name, 16 bytes secret
	cmd[0], cmd[1] = cmdSetChannel, byte(idx)
	if _, err := c.Do(cmd, respOK); err != nil {
		return fmt.Errorf("SET_CHANNEL: %w", err)
	}
	return nil
}

// Neighbor is a node that answered a discovery request.
type Neighbor struct {
	Type      int     // advert type
	Key       []byte  // 32 bytes, or an 8-byte prefix
	TheirSNR  float64 // how well it heard our request, dB
	SNR, RSSI float64 // how well we heard its answer
	Heard     time.Time
}

// buildDiscover makes the control payload of a discovery request: the type, a
// filter with one bit per advert type, a tag and "since" (0: any node).
func buildDiscover(types byte, tag uint32) []byte {
	b := make([]byte, 10)
	b[0], b[1] = ctlNodeDiscoverReq, types
	binary.LittleEndian.PutUint32(b[2:], tag)
	return b
}

// parseDiscoverResponse reads a control-data push (0x8E, our SNR*4, RSSI, path
// length, then the control payload) and returns the neighbor if it answers tag.
func parseDiscoverResponse(f []byte, tag uint32) (*Neighbor, bool) {
	if len(f) < 4+6+8 || f[0] != pushControlData {
		return nil, false
	}
	p := f[4:]
	if p[0]&0xF0 != ctlNodeDiscoverResp || binary.LittleEndian.Uint32(p[2:]) != tag {
		return nil, false
	}
	return &Neighbor{
		Type: int(p[0] & 0x0F), Key: append([]byte(nil), p[6:]...),
		TheirSNR: float64(int8(p[1])) / 4, SNR: float64(int8(f[1])) / 4, RSSI: float64(int8(f[2])),
		Heard: time.Now(),
	}, true
}

// Discover asks the nodes in direct radio range to identify themselves, and
// collects the answers for wait. types is a mask of AutoAdd* type bits (for
// example AutoAddRepeater). The request goes one hop only, and a repeater answers
// at most 4 times in 2 minutes.
func (c *Client) Discover(types byte, wait time.Duration) ([]*Neighbor, error) {
	if types == 0 {
		return nil, errors.New("DISCOVER: no node type selected")
	}
	var r [4]byte
	if _, err := rand.Read(r[:]); err != nil {
		return nil, err
	}
	tag := binary.LittleEndian.Uint32(r[:])
	sub := c.Subscribe(pushControlData) // before sending, so a quick answer is not missed
	defer sub.Close()
	if _, err := c.Do(append([]byte{cmdSendControlData}, buildDiscover(types, tag)...), respOK); err != nil {
		return nil, fmt.Errorf("SEND_CONTROL_DATA: %w", err)
	}
	var out []*Neighbor
	seen := map[string]bool{}
	deadline := time.After(wait)
	for {
		select {
		case f := <-sub.C:
			if n, ok := parseDiscoverResponse(f, tag); ok && !seen[string(n.Key)] {
				seen[string(n.Key)] = true
				out = append(out, n)
			}
		case <-deadline:
			return out, nil
		}
	}
}
