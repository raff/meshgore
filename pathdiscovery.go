package meshgore

import (
	"errors"
	"fmt"
	"time"
)

// Path discovery, from the firmware source (examples/companion_radio/MyMesh.cpp):
// CMD_SEND_PATH_DISCOVERY_REQ is a flood telemetry request to one contact. The
// answer is PUSH_CODE_PATH_DISCOVERY_RESPONSE with the route the request took out
// and the route the reply took back.
const (
	cmdPathDiscovery  = 0x34
	pushPathDiscovery = 0x8D
)

// PathDiscovery is the route to a contact: the repeaters the request went through
// on the way out, and the ones the reply came back through. Each hop is the first
// 1 to 3 bytes of a repeater's public key (see DeviceInfo.PathHashMode). An empty
// path means no repeater: the two nodes hear each other directly.
type PathDiscovery struct {
	Key [6]byte  // public key prefix of the contact
	Out [][]byte // from this node to the contact
	In  [][]byte // from the contact back to this node
}

// parsePath reads a path: a length byte (low 6 bits the number of hops, top 2 bits
// the hash size minus 1) and the hashes. It returns the hops and the rest of f.
func parsePath(f []byte) (hops [][]byte, rest []byte, err error) {
	if len(f) < 1 {
		return nil, nil, errors.New("path length missing")
	}
	n, size := int(f[0]&63), int(f[0]>>6)+1
	if size == 4 {
		return nil, nil, fmt.Errorf("path length %#x: hash size is reserved", f[0])
	}
	f = f[1:]
	if len(f) < n*size {
		return nil, nil, fmt.Errorf("path of %d hops of %d bytes needs %d bytes, have %d", n, size, n*size, len(f))
	}
	for i := 0; i < n; i++ {
		hops = append(hops, f[i*size:(i+1)*size])
	}
	return hops, f[n*size:], nil
}

// parsePathDiscovery reads a push frame: 0x8D, a reserved byte, the 6-byte key
// prefix, then the outbound path and the inbound path.
func parsePathDiscovery(f []byte) (*PathDiscovery, error) {
	if len(f) < 10 {
		return nil, fmt.Errorf("PATH_DISCOVERY_RESPONSE too short: %d bytes", len(f))
	}
	p := &PathDiscovery{}
	copy(p.Key[:], f[2:8])
	var err error
	var rest []byte
	if p.Out, rest, err = parsePath(f[8:]); err != nil {
		return nil, fmt.Errorf("PATH_DISCOVERY_RESPONSE outbound: %w", err)
	}
	if p.In, _, err = parsePath(rest); err != nil {
		return nil, fmt.Errorf("PATH_DISCOVERY_RESPONSE inbound: %w", err)
	}
	return p, nil
}

// DiscoverPath finds the route to a contact (full public key). The request is a
// flood, so it is retried like login and telemetry, and the contact must answer
// a telemetry request. attempts is how many times the request is sent. Only one such request can be pending on the node.
func (c *Client) DiscoverPath(key [32]byte, attempts int) (*PathDiscovery, error) {
	cmd := append([]byte{cmdPathDiscovery, 0}, key[:]...)
	sub := c.Subscribe(pushPathDiscovery)
	defer sub.Close()
	for attempt := 1; attempt <= attempts; attempt++ {
		suggested, err := c.sendAccepted(cmd)
		if err != nil {
			return nil, fmt.Errorf("SEND_PATH_DISCOVERY_REQ: %w", err)
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
				if len(f) < 8 || string(f[2:8]) != string(key[:6]) {
					continue
				}
				return parsePathDiscovery(f)
			case <-deadline:
				break recv
			}
		}
		if attempt < attempts {
			logf("no path reply after %s, retrying (%d/%d)...", wait.Round(time.Second), attempt+1, attempts)
		}
	}
	return nil, errors.New("no path discovery reply")
}
