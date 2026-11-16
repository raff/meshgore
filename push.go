package meshgore

import (
	"sync"
	"sync/atomic"
)

// Frames from the node at 0x80 and up are not answers to a command: message
// waiting, advert, login result, telemetry, control data. Several things want
// them at once (a live message display, a login in progress, a discovery), so
// they go through a hub that gives each subscriber its own copy.

// subBuffer is how many frames a subscriber can fall behind before frames are
// dropped for it.
const subBuffer = 64

// Sub receives pushes. Read from C; call Close when done. A subscriber that
// does not keep up loses frames (Dropped counts them) instead of stalling the
// others, so read promptly, or subscribe to the codes you need only.
type Sub struct {
	// C delivers each push frame, starting with its code. Do not modify the frames:
	// every subscriber gets the same slice.
	C <-chan []byte

	ch      chan []byte
	codes   map[byte]bool // nil: every code
	hub     *pushHub
	dropped atomic.Int64
}

// Dropped is how many frames this subscriber missed because C was full.
func (s *Sub) Dropped() int64 { return s.dropped.Load() }

// Close stops delivery. It is safe to call more than once. C is not closed, so a
// reader that selects on it should also watch for its own cancellation.
func (s *Sub) Close() { s.hub.remove(s) }

// pushHub fans each push out to the subscribers that asked for its code.
type pushHub struct {
	mu   sync.Mutex
	subs map[*Sub]struct{}
}

func newPushHub() *pushHub { return &pushHub{subs: map[*Sub]struct{}{}} }

// subscribe returns a subscription for the given push codes; none means all.
func (h *pushHub) subscribe(codes ...byte) *Sub {
	s := &Sub{ch: make(chan []byte, subBuffer), hub: h}
	s.C = s.ch
	if len(codes) > 0 {
		s.codes = map[byte]bool{}
		for _, c := range codes {
			s.codes[c] = true
		}
	}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *pushHub) remove(s *Sub) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// publish delivers f to every matching subscriber without blocking.
func (h *pushHub) publish(f []byte) {
	if len(f) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.codes != nil && !s.codes[f[0]] {
			continue
		}
		select {
		case s.ch <- f:
		default:
			s.dropped.Add(1)
		}
	}
}

// Subscribe returns a subscription to the node's unsolicited frames with the given
// codes (all of them if none are given). Subscribe before sending the request
// whose answer you wait for, so a quick answer is not missed.
func (c *Client) Subscribe(codes ...byte) *Sub { return c.hub.subscribe(codes...) }

// Subscribe is Client.Subscribe for the link. The subscription survives
// Reconnect: frames from the new connection arrive on the same channel.
func (l *Link) Subscribe(codes ...byte) *Sub { return l.hub.subscribe(codes...) }
