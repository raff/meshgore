package meshgore

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func collect(r *frameReader, chunks ...[]byte) [][]byte {
	var got [][]byte
	for _, c := range chunks {
		r.feed(c, func(f []byte) { got = append(got, append([]byte(nil), f...)) })
	}
	return got
}

func TestFrameReader(t *testing.T) {
	var r frameReader
	// noise, a frame split across reads (length and payload), another frame, a
	// zero-length frame, an oversize one, then a frame after the bad ones
	got := collect(&r,
		[]byte("boot log\n>"), []byte{3}, []byte{0, 0x09}, []byte{1, 2},
		[]byte{'>', 1, 0, 0x00},
		[]byte{'>', 0, 0, '>', 0xFF, 0xFF, '>', 2, 0, 7, 8})
	want := [][]byte{{9, 1, 2}, {0}, {7, 8}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("frame %d: got % x, want % x", i, got[i], want[i])
		}
	}
	// a frame that never finishes can be abandoned
	collect(&r, []byte{'>', 5, 0, 1})
	if !r.partial() {
		t.Error("expected a partial frame")
	}
	r.reset()
	if got := collect(&r, []byte{'>', 1, 0, 4}); len(got) != 1 || got[0][0] != 4 {
		t.Errorf("after reset: %v", got)
	}
}

func TestFrameForNode(t *testing.T) {
	got, err := frameForNode([]byte{0x16, 3})
	if err != nil || !bytes.Equal(got, []byte{'<', 2, 0, 0x16, 3}) {
		t.Errorf("got % x, %v", got, err)
	}
	if _, err := frameForNode(nil); err == nil {
		t.Error("empty frame accepted")
	}
	if _, err := frameForNode(make([]byte, maxSerialFrame+1)); err == nil {
		t.Error("oversize frame accepted")
	}
}

// fakeNode answers GET_DEVICE_TIME on the other end of a pipe, with some noise
// in front of the framed reply the way a node's log output might be.
func fakeNode(t *testing.T, conn net.Conn) {
	buf := make([]byte, 64)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		if n >= 4 && buf[0] == '<' && buf[3] == cmdGetDeviceTime {
			conn.Write([]byte("debug text"))
			conn.Write([]byte{'>', 5, 0, respCurrTime, 0x80, 0x51, 0x01, 0x00}) // 86400
		}
	}
}

func TestSerialClientRoundTrip(t *testing.T) {
	app, node := net.Pipe()
	defer node.Close()
	go fakeNode(t, node)
	c := newSerialClient(app, newPushHub())
	defer c.tr.Close()

	done := make(chan struct{})
	var got time.Time
	var err error
	go func() { got, err = c.DeviceTime(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("no reply over the serial transport")
	}
	if err != nil || got.Unix() != 86400 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestSerialTransportDies(t *testing.T) {
	app, node := net.Pipe()
	c := newSerialClient(app, newPushHub())
	node.Close() // the node went away
	deadline := time.Now().Add(2 * time.Second)
	for c.tr.Alive() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.tr.Alive() {
		t.Error("transport still alive after the port closed")
	}
	if err := c.write([]byte{1}); err == nil {
		t.Error("write to a closed port succeeded")
	}
}
