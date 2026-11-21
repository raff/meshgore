package meshgore

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"
)

// Advert pushes. With auto-add off for a node type, the firmware does not save
// an advert from an unknown node; it pushes PUSH_CODE_NEW_ADVERT with the same
// frame layout as a contact. An advert from a node that is already a contact
// is pushed as PUSH_CODE_ADVERT with just its 32-byte key.
const pushNewAdvert = 0x8A

// Advert is an advertisement the node heard while we were listening.
type Advert struct {
	Key   [32]byte
	Known bool     // already a contact (only the key was pushed)
	Info  *Contact // name, type, position, path; nil when Known
	Heard time.Time
}

// parseAdvert reads a 0x80 or 0x8A push.
func parseAdvert(f []byte) (*Advert, error) {
	if len(f) == 0 {
		return nil, fmt.Errorf("empty advert push")
	}
	switch f[0] {
	case pushAdvertisement:
		if len(f) < 33 {
			return nil, fmt.Errorf("ADVERT too short: %d bytes", len(f))
		}
		a := &Advert{Known: true, Heard: time.Now()}
		copy(a.Key[:], f[1:33])
		return a, nil
	case pushNewAdvert:
		ct, err := parseContact(f)
		if err != nil {
			return nil, fmt.Errorf("NEW_ADVERT: %w", err)
		}
		return &Advert{Key: ct.PublicKey, Info: ct, Heard: time.Now()}, nil
	}
	return nil, fmt.Errorf("not an advert push: 0x%02x", f[0])
}

// ListenAdverts collects the adverts the node hears for wait, calling fn for
// each one (once per key). It sends nothing: to provoke adverts, send your own
// with SendAdvert first.
func (c *Client) ListenAdverts(ctx context.Context, wait time.Duration, fn func(*Advert)) error {
	sub := c.Subscribe(pushAdvertisement, pushNewAdvert)
	defer sub.Close()
	seen := map[[32]byte]bool{}
	deadline := time.After(wait)
	for {
		select {
		case f := <-sub.C:
			a, err := parseAdvert(f)
			if err != nil || seen[a.Key] {
				continue
			}
			seen[a.Key] = true
			fn(a)
		case <-deadline:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

const cmdAddUpdateContact = 0x09

// addContactCmd builds ADD_UPDATE_CONTACT from a contact: the same layout as the
// contact frame, without the trailing last-modified field. The stored route is
// left empty (flood), so the first message or login finds one.
func addContactCmd(ct *Contact) []byte {
	b := make([]byte, 1+32+1+1+1+64+32+4+4+4)
	b[0] = cmdAddUpdateContact
	copy(b[1:], ct.PublicKey[:])
	b[33], b[34], b[35] = ct.Type, ct.Flags, 0xFF
	copy(b[100:132], ct.Name)
	binary.LittleEndian.PutUint32(b[132:], uint32(ct.LastAdvert.Unix()))
	binary.LittleEndian.PutUint32(b[136:], uint32(int32(ct.Lat*1e6)))
	binary.LittleEndian.PutUint32(b[140:], uint32(int32(ct.Lon*1e6)))
	return b
}

// AddContact saves a contact on the node, for example one learned from an advert
// while auto-add was off. Without it, the node refuses to log in to the key or
// discover a path to it.
func (c *Client) AddContact(ct *Contact) error {
	if _, err := c.Do(addContactCmd(ct), respOK); err != nil {
		return fmt.Errorf("ADD_UPDATE_CONTACT: %w", err)
	}
	return nil
}
