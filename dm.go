package meshgore

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Direct messages. The doc does not cover them; the layouts and the retry
// policy follow meshcore_py.
const (
	cmdResetPath = 0x0D

	cmdSendSelfAdvert = 0x07

	// Retry policy: up to 3 attempts; after 2 unacknowledged attempts the path
	// is reset so the next one floods; at most 2 attempts once flooding.
	dmMaxAttempts      = 3
	dmFloodAfter       = 2
	dmMaxFloodAttempts = 2
)

// SentMessage is the device's answer to a send: the message is queued for the
// radio, not yet delivered.
type SentMessage struct {
	Flood   bool          // sent by flood (no known path) rather than direct
	Ack     [4]byte       // the code the recipient's acknowledgement will carry
	Timeout time.Duration // how long the device suggests waiting for the ack
}

// buildDM lays out a SEND_TXT_MSG frame. The destination is addressed by its
// 6-byte key prefix. attempt distinguishes retries of the same message.
func buildDM(to [32]byte, text string, attempt byte, timestamp uint32) []byte {
	cmd := []byte{cmdSendTxtMsg, 0x00, attempt, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(cmd[3:], timestamp)
	cmd = append(cmd, to[:6]...)
	return append(cmd, text...)
}

func parseMsgSent(f []byte) (*SentMessage, error) {
	if len(f) < 10 {
		return nil, fmt.Errorf("MSG_SENT too short: %d bytes", len(f))
	}
	s := &SentMessage{
		Flood:   f[1] == 1,
		Timeout: time.Duration(binary.LittleEndian.Uint32(f[6:])) * time.Millisecond,
	}
	copy(s.Ack[:], f[2:6])
	return s, nil
}

// parseAck reads an ACK push (0x82): the code, and the round-trip time the node
// measured in milliseconds (zero if the frame predates that field).
func parseAck(f []byte) (code [4]byte, trip time.Duration, ok bool) {
	if len(f) < 5 || f[0] != pushAck {
		return code, 0, false
	}
	copy(code[:], f[1:5])
	if len(f) >= 9 {
		trip = time.Duration(binary.LittleEndian.Uint32(f[5:])) * time.Millisecond
	}
	return code, trip, true
}

// SendMessage sends one direct message and returns once the device has queued
// it. It does not wait for delivery; see SendMessageWithRetry.
func (c *Client) SendMessage(to [32]byte, text string, attempt byte, timestamp uint32) (*SentMessage, error) {
	f, err := c.Do(buildDM(to, text, attempt, timestamp), respMsgSent)
	if err != nil {
		return nil, fmt.Errorf("SEND_TXT_MSG: %w", err)
	}
	return parseMsgSent(f)
}

// ResetPath forgets the stored route to a contact, so the next message floods.
func (c *Client) ResetPath(key [32]byte) error {
	if _, err := c.Do(append([]byte{cmdResetPath}, key[:]...), respOK); err != nil {
		return fmt.Errorf("RESET_PATH: %w", err)
	}
	return nil
}

// Delivery is the outcome of SendMessageWithRetry.
type Delivery struct {
	Acked     bool          // the recipient acknowledged the message
	Attempts  int           // how many times it was sent
	Flood     bool          // the last attempt was a flood rather than direct
	RoundTrip time.Duration // send to ack; 0 if not acked. The node's own measurement when it reports one, else measured here
	// NodeReported says RoundTrip came from the node, which times the radio
	// exchange alone; the local measurement also includes the Bluetooth hop.
	NodeReported bool
}

// SendMessageWithRetry sends text and waits for the recipient's acknowledgement,
// retrying as the official client does: up to 3 attempts, resetting the path
// after 2 failures so the message floods. Every attempt carries the same
// timestamp, so the recipient shows the message once; earlier attempts' acks
// still count. A message that was never acknowledged is not an error: check
// Delivery.Acked, since it may well have arrived.
func (c *Client) SendMessageWithRetry(ctx context.Context, to [32]byte, text string) (*Delivery, error) {
	if text == "" {
		return nil, errors.New("empty message")
	}
	ts := uint32(time.Now().Unix())
	d := &Delivery{}
	sent := map[[4]byte]bool{}
	floodAttempts := 0
	var lastErr error

	for d.Attempts < dmMaxAttempts && (!d.Flood || floodAttempts < dmMaxFloodAttempts) {
		if d.Attempts == dmFloodAfter && !d.Flood {
			if err := c.ResetPath(to); err != nil {
				logf("%v; continuing", err)
			} else {
				d.Flood = true
			}
		}

		start := time.Now()
		s, err := c.SendMessage(to, text, byte(d.Attempts), ts)
		d.Attempts++
		if err != nil {
			lastErr = err
			logf("attempt %d: %v", d.Attempts, err)
			if d.Flood {
				floodAttempts++
			}
			continue
		}
		d.Flood = s.Flood // the device says whether this one went direct or by flood
		if d.Flood {
			floodAttempts++
		}
		sent[s.Ack] = true

		wait := s.Timeout * 6 / 5
		if wait < 2*time.Second {
			wait = 2 * time.Second
		}
		logf("attempt %d sent (%s); waiting up to %s for the ack", d.Attempts, routeName(d.Flood), wait.Round(time.Millisecond))
		ok, trip, err := c.awaitAck(ctx, sent, wait)
		if err != nil {
			return d, err
		}
		if ok {
			d.Acked, d.RoundTrip = true, time.Since(start)
			if trip > 0 {
				d.RoundTrip, d.NodeReported = trip, true
			}
			return d, nil
		}
	}
	if len(sent) == 0 && lastErr != nil {
		return d, lastErr // the device never accepted the message
	}
	return d, nil
}

func routeName(flood bool) string {
	if flood {
		return "flood"
	}
	return "direct"
}

// awaitAck waits for an ACK push carrying one of the codes, and returns the
// round-trip time the node reported in it (0 if none). Other pushes seen
// meanwhile are put back so a later reader still gets them.
func (c *Client) awaitAck(ctx context.Context, codes map[[4]byte]bool, wait time.Duration) (ok bool, trip time.Duration, err error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var others [][]byte
	defer func() {
		for _, f := range others {
			select {
			case c.Push <- f:
			default:
			}
		}
	}()
	for {
		select {
		case f := <-c.Push:
			if code, trip, isAck := parseAck(f); isAck && codes[code] {
				return true, trip, nil
			}
			others = append(others, f)
		case <-timer.C:
			return false, 0, nil
		case <-ctx.Done():
			return false, 0, ctx.Err()
		}
	}
}

// advertCmd builds SEND_SELF_ADVERT. A zero-hop advert is heard only by nodes in
// direct radio range; a flood advert is rebroadcast by repeaters across the mesh.
func advertCmd(flood bool) []byte {
	if flood {
		return []byte{cmdSendSelfAdvert, 0x01}
	}
	return []byte{cmdSendSelfAdvert}
}

// SendAdvert makes the companion broadcast its own advertisement, which is how
// other nodes learn it exists (and appear in their contact lists). It returns
// once the node has accepted the request.
func (c *Client) SendAdvert(flood bool) error {
	if _, err := c.Do(advertCmd(flood), respOK); err != nil {
		return fmt.Errorf("SEND_SELF_ADVERT: %w", err)
	}
	return nil
}
