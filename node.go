package meshgore

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Commands that configure the companion itself. The channel layout is in the
// protocol doc; the others follow meshcore_py and the firmware source.
const (
	cmdSetAdvertName   = 0x08
	cmdSetAdvertLatLon = 0x0E
	cmdSetChannel      = 0x20
	cmdGetCustomVars   = 0x28
	cmdSetCustomVar    = 0x29

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

func buildSetCustomVar(name, value string) ([]byte, error) {
	if name == "" || strings.ContainsAny(name, ":,") || strings.Contains(value, ",") {
		return nil, fmt.Errorf("bad variable %q=%q", name, value)
	}
	return append([]byte{cmdSetCustomVar}, name+":"+value...), nil
}

// SetCustomVar changes a custom variable (see CustomVars). The firmware answers
// ILLEGAL_ARG for a name the board does not have or a value out of range.
func (c *Client) SetCustomVar(name, value string) error {
	cmd, err := buildSetCustomVar(name, value)
	if err != nil {
		return fmt.Errorf("SET_CUSTOM_VAR: %w", err)
	}
	if _, err := c.Do(cmd, respOK); err != nil {
		return fmt.Errorf("SET_CUSTOM_VAR %s: %w", name, err)
	}
	return nil
}

// SetGPS switches the GPS on or off.
func (c *Client) SetGPS(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return c.SetCustomVar("gps", v)
}

// SetGPSInterval sets how often the GPS updates the position, in seconds
// (0..86400; 0 means no automatic updates).
func (c *Client) SetGPSInterval(seconds int) error {
	if seconds < 0 || seconds > 86400 {
		return fmt.Errorf("GPS interval %d is out of range (0..86400 seconds)", seconds)
	}
	return c.SetCustomVar("gps_interval", fmt.Sprint(seconds))
}

const (
	cmdRunCLI   = 0x42
	respCLIText = 0x1D
)

// RunCLI runs a CLI command on the companion itself (v14+ firmware) and returns
// its text reply. The companion's command set is smaller than a repeater's and
// an unknown command is answered with "Unknown command", not an error.
func (c *Client) RunCLI(command string) (string, error) {
	if len(command) < 2 {
		return "", errors.New("RUN_CLI_COMMAND: command too short")
	}
	f, err := c.Do(append([]byte{cmdRunCLI}, command...), respCLIText)
	if err != nil {
		return "", fmt.Errorf("RUN_CLI_COMMAND: %w", err)
	}
	return strings.TrimSpace(string(f[1:])), nil
}

// RunCLINoReply sends a CLI command that makes the node restart or power off, so
// no reply comes and the write is often never confirmed (CoreBluetooth blocks for
// about 50 s). The write is left running in the background and this returns after
// a short grace period; closing or replacing the connection ends it.
func (c *Client) RunCLINoReply(command string) {
	done := make(chan error, 1)
	go func() { done <- c.write(append([]byte{cmdRunCLI}, command...)) }()
	select {
	case err := <-done:
		if err != nil {
			logf("write: %v", err)
		}
	case <-time.After(3 * time.Second):
	}
}
