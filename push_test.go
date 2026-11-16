package meshgore

import (
	"net"
	"testing"
	"time"
)

func recv(t *testing.T, s *Sub) []byte {
	t.Helper()
	select {
	case f := <-s.C:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
		return nil
	}
}

func none(t *testing.T, s *Sub) {
	t.Helper()
	select {
	case f := <-s.C:
		t.Fatalf("unexpected frame % x", f)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHubFanOutAndFilter(t *testing.T) {
	h := newPushHub()
	all, waiting, two := h.subscribe(), h.subscribe(0x83), h.subscribe(0x83, 0x87)
	h.publish([]byte{0x83})
	h.publish([]byte{0x87, 1})
	h.publish([]byte{0x8A})
	h.publish(nil) // ignored

	for _, want := range []byte{0x83, 0x87, 0x8A} {
		if f := recv(t, all); f[0] != want {
			t.Errorf("all: got % x, want %#x", f, want)
		}
	}
	if f := recv(t, waiting); f[0] != 0x83 {
		t.Errorf("filtered: % x", f)
	}
	none(t, waiting)
	recv(t, two)
	if f := recv(t, two); f[0] != 0x87 {
		t.Errorf("two: % x", f)
	}
	none(t, two)
}

func TestHubClose(t *testing.T) {
	h := newPushHub()
	a, b := h.subscribe(), h.subscribe()
	a.Close()
	a.Close() // twice is fine
	h.publish([]byte{0x83})
	none(t, a)
	recv(t, b)
}

func TestHubSlowSubscriberDoesNotBlock(t *testing.T) {
	h := newPushHub()
	slow, fast := h.subscribe(), h.subscribe()
	done := make(chan struct{})
	go func() {
		for i := 0; i < subBuffer+10; i++ {
			h.publish([]byte{0x83, byte(i)})
			<-fast.C // the fast one keeps up
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("publish blocked on a subscriber that does not read")
	}
	if slow.Dropped() != 10 || fast.Dropped() != 0 {
		t.Errorf("dropped: slow %d, fast %d; want 10 and 0", slow.Dropped(), fast.Dropped())
	}
}

// A frame the node pushes reaches every subscriber, and a reader waiting for a
// command's answer is not disturbed by one that listens for something else.
func TestClientPushesReachAllSubscribers(t *testing.T) {
	app, node := net.Pipe()
	defer node.Close()
	c := newSerialClient(app, newPushHub())
	defer c.tr.Close()
	live, other := c.Subscribe(pushMsgWaiting), c.Subscribe(pushMsgWaiting, pushAck)
	go node.Write([]byte{'>', 1, 0, pushMsgWaiting})
	if f := recv(t, live); f[0] != pushMsgWaiting {
		t.Errorf("live: % x", f)
	}
	if f := recv(t, other); f[0] != pushMsgWaiting {
		t.Errorf("other: % x", f)
	}
}

// A link keeps one hub while its client is replaced, so a subscription made
// before a reconnect still gets frames from the connection after it.
func TestHubSurvivesClientReplacement(t *testing.T) {
	hub := newPushHub()
	sub := hub.subscribe(pushMsgWaiting)

	app1, node1 := net.Pipe()
	c1 := newSerialClient(app1, hub)
	node1.Close()
	c1.tr.Close()

	app2, node2 := net.Pipe()
	defer node2.Close()
	c2 := newSerialClient(app2, hub)
	defer c2.tr.Close()
	go node2.Write([]byte{'>', 1, 0, pushMsgWaiting})
	if f := recv(t, sub); f[0] != pushMsgWaiting {
		t.Errorf("% x", f)
	}
}
