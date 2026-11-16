package meshgore

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Commands that configure the companion itself. The channel layout is in the
// protocol doc; the others follow meshcore_py and the firmware source.
const (
	cmdSetAdvertName   = 0x08
	cmdSetAdvertLatLon = 0x0E
	cmdSetChannel      = 0x20
	cmdGetCustomVars   = 0x28

	respCustomVars = 0x15

	// maxNameLen is the longest node or channel name the firmware stores (a
	// 32-byte field including the terminator). Longer names are silently cut off
	// by the device, so they are rejected here instead.
	maxNameLen = 31
)

func buildSetName(name string) ([]byte, error) {
	if name == "" {
		return nil, errors.New("name is empty")
	}
	if len(name) > maxNameLen {
		return nil, fmt.Errorf("name is %d bytes; the device keeps at most %d", len(name), maxNameLen)
	}
	return append([]byte{cmdSetAdvertName}, name...), nil
}

// SetName sets the name the node advertises, and shows in other nodes' contact
// lists. Send an advert afterwards so others learn it.
func (c *Client) SetName(name string) error {
	cmd, err := buildSetName(name)
	if err != nil {
		return fmt.Errorf("SET_ADVERT_NAME: %w", err)
	}
	if _, err := c.Do(cmd, respOK); err != nil {
		return fmt.Errorf("SET_ADVERT_NAME: %w", err)
	}
	return nil
}

func buildSetPosition(lat, lon float64) ([]byte, error) {
	if math.IsNaN(lat) || math.IsNaN(lon) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return nil, fmt.Errorf("position %v,%v is out of range (latitude -90..90, longitude -180..180)", lat, lon)
	}
	cmd := []byte{cmdSetAdvertLatLon, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(cmd[1:], uint32(int32(math.Round(lat*1e6))))
	binary.LittleEndian.PutUint32(cmd[5:], uint32(int32(math.Round(lon*1e6))))
	// The last four bytes are an altitude the firmware ignores for now.
	return cmd, nil
}

// SetPosition sets the position the node advertises, in degrees. A node with
// GPS enabled will overwrite it with its own fix.
func (c *Client) SetPosition(lat, lon float64) error {
	cmd, err := buildSetPosition(lat, lon)
	if err != nil {
		return fmt.Errorf("SET_ADVERT_LATLON: %w", err)
	}
	if _, err := c.Do(cmd, respOK); err != nil {
		return fmt.Errorf("SET_ADVERT_LATLON: %w", err)
	}
	return nil
}

// HashtagSecret returns the secret of a hashtag channel: the first 16 bytes of
// the SHA-256 of its name, including the leading "#". Anyone who knows the name
// can join such a channel, which is the point of them.
func HashtagSecret(name string) [16]byte {
	var s [16]byte
	sum := sha256.Sum256([]byte(name))
	copy(s[:], sum[:16])
	return s
}

func buildSetChannel(idx int, name string, secret [16]byte) ([]byte, error) {
	if idx < 0 || idx > 255 {
		return nil, fmt.Errorf("channel slot %d is out of range", idx)
	}
	if name == "" {
		return nil, errors.New("channel name is empty")
	}
	if len(name) > maxNameLen {
		return nil, fmt.Errorf("channel name is %d bytes; the device keeps at most %d", len(name), maxNameLen)
	}
	if secret == ([16]byte{}) {
		return nil, errors.New("refusing an all-zero channel secret")
	}
	cmd := make([]byte, 0, 50)
	cmd = append(cmd, cmdSetChannel, byte(idx))
	cmd = append(cmd, name...)
	cmd = append(cmd, make([]byte, 32-len(name))...) // name field is 32 bytes, zero padded
	return append(cmd, secret[:]...), nil
}

// SetChannel writes channel slot idx, replacing whatever was there. Use
// HashtagSecret for a channel whose name starts with "#".
func (c *Client) SetChannel(idx int, name string, secret [16]byte) error {
	cmd, err := buildSetChannel(idx, name, secret)
	if err != nil {
		return fmt.Errorf("SET_CHANNEL: %w", err)
	}
	if c.Info != nil && idx >= c.Info.MaxChannels {
		return fmt.Errorf("SET_CHANNEL: slot %d does not exist; the device has %d", idx, c.Info.MaxChannels)
	}
	if _, err := c.Do(cmd, respOK); err != nil {
		return fmt.Errorf("SET_CHANNEL: %w", err)
	}
	return nil
}

// Var is one custom variable of the node: a setting that depends on what the
// board has (GPS, sensors, ...).
type Var struct {
	Name  string
	Value string
}

// parseCustomVars reads the reply to GET_CUSTOM_VARS: text of the form
// "name:value,name:value", empty if the node has none. The order is kept.
func parseCustomVars(f []byte) []Var {
	if len(f) < 2 {
		return nil
	}
	var vars []Var
	for _, pair := range strings.Split(string(f[1:]), ",") {
		if pair == "" {
			continue
		}
		name, value, _ := strings.Cut(pair, ":")
		vars = append(vars, Var{name, value})
	}
	return vars
}

// CustomVars lists the node's custom variables. Which ones exist depends on the
// board and firmware build, so this is how to find out what a node supports: a
// "gps" variable, for example, is only there if GPS support is compiled in.
func (c *Client) CustomVars() ([]Var, error) {
	f, err := c.Do([]byte{cmdGetCustomVars}, respCustomVars)
	if err != nil {
		return nil, fmt.Errorf("GET_CUSTOM_VARS: %w", err)
	}
	return parseCustomVars(f), nil
}
