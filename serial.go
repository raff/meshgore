package meshgore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"go.bug.st/serial"
)

// USB serial framing, from the firmware's ArduinoSerialInterface: a start byte
// ('<' from the app, '>' from the node), a 2-byte little-endian length, then the
// same frame as over Bluetooth. There is no checksum or escaping.
const (
	serialToNode   = '<'
	serialFromNode = '>'
	// maxSerialFrame is the firmware's MAX_FRAME_SIZE.
	maxSerialFrame = 176

	// DefaultBaud is what the firmware opens the port with. It is ignored on a
	// native USB port.
	DefaultBaud = 115200

	// serialStall is how long a frame may stay incomplete before the parser gives
	// up on it and looks for the next start byte.
	serialStall = 2 * time.Second
)

// frameReader splits a serial byte stream into frames. Bytes outside a frame
// are dropped, which is how it finds the next frame after noise.
type frameReader struct {
	state int // 0 idle, 1 wants length low, 2 wants length high, 3 in payload
	n     int
	buf   []byte
}

// reset forgets a partly received frame.
func (r *frameReader) reset() { r.state, r.buf = 0, r.buf[:0] }

// partial reports whether a frame is in progress.
func (r *frameReader) partial() bool { return r.state != 0 }

// feed adds received bytes and calls emit with each complete frame. The slice
// passed to emit is only valid during the call.
func (r *frameReader) feed(b []byte, emit func([]byte)) {
	for _, c := range b {
		switch r.state {
		case 0:
			if c == serialFromNode {
				r.state = 1
			}
		case 1:
			r.n, r.state = int(c), 2
		case 2:
			r.n |= int(c) << 8
			if r.n == 0 || r.n > maxSerialFrame {
				r.state = 0 // not a frame we can use: wait for the next start byte
				continue
			}
			r.buf, r.state = r.buf[:0], 3
		case 3:
			r.buf = append(r.buf, c)
			if len(r.buf) == r.n {
				emit(r.buf)
				r.reset()
			}
		}
	}
}

// frameForNode wraps a frame for sending to the node.
func frameForNode(frame []byte) ([]byte, error) {
	if len(frame) == 0 || len(frame) > maxSerialFrame {
		return nil, fmt.Errorf("frame of %d bytes does not fit the serial protocol (1..%d)", len(frame), maxSerialFrame)
	}
	out := make([]byte, 0, 3+len(frame))
	out = append(out, serialToNode, byte(len(frame)), byte(len(frame)>>8))
	return append(out, frame...), nil
}

// serialTransport carries frames over a serial port.
type serialTransport struct {
	port io.ReadWriteCloser
	dead atomic.Bool
}

func (t *serialTransport) Alive() bool { return !t.dead.Load() }

func (t *serialTransport) Close() error {
	t.dead.Store(true)
	return t.port.Close()
}

func (t *serialTransport) WriteFrame(frame []byte) error {
	b, err := frameForNode(frame)
	if err != nil {
		return err
	}
	if _, err := t.port.Write(b); err != nil {
		t.dead.Store(true)
		return fmt.Errorf("serial write: %w", err)
	}
	return nil
}

// readLoop feeds what the port delivers to c until the port fails or closes. A
// port that goes away (the node rebooted, the cable was pulled) marks the
// transport dead.
func (t *serialTransport) readLoop(c *Client) {
	var fr frameReader
	buf := make([]byte, 512)
	last := time.Now()
	for {
		n, err := t.port.Read(buf)
		if n > 0 {
			if fr.partial() && time.Since(last) > serialStall {
				fr.reset() // the rest of that frame never came
			}
			last = time.Now()
			fr.feed(buf[:n], func(f []byte) { c.onNotify(f) })
		}
		if err != nil {
			t.dead.Store(true)
			return
		}
		if n == 0 {
			time.Sleep(10 * time.Millisecond) // a read timeout, not an error
		}
	}
}

// newSerialClient returns a client that talks over rwc.
func newSerialClient(rwc io.ReadWriteCloser, hub *pushHub) *Client {
	t := &serialTransport{port: rwc}
	c := newClient(t, hub)
	go t.readLoop(c)
	return c
}

// ListSerialPorts lists the serial ports the system knows.
func ListSerialPorts() ([]string, error) { return serial.GetPortsList() }

// serialConnect opens the port and runs the handshake. On error the port is
// closed.
func serialConnect(path string, baud int, hub *pushHub) (*Client, *SelfInfo, error) {
	port, err := serial.Open(path, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	_ = port.SetReadTimeout(200 * time.Millisecond)
	_ = port.ResetInputBuffer()
	logf("Opened %s at %d baud", path, baud)
	c := newSerialClient(port, hub)
	self, err := c.handshake()
	if err != nil {
		c.tr.Close()
		return nil, nil, err
	}
	return c, self, nil
}

// handshake runs APP_START and DEVICE_QUERY.
func (c *Client) handshake() (*SelfInfo, error) {
	self, err := c.AppStart("mc-go")
	if err != nil {
		return nil, err
	}
	if _, err := c.DeviceQuery(); err != nil {
		return nil, err
	}
	return self, nil
}

// DialSerial connects to a companion on a serial port, for example
// /dev/cu.usbmodem14301. Nothing is paired or scanned. The node must be running a
// build with its USB serial interface enabled; a build that has only Bluetooth
// does not answer, and the handshake times out.
func DialSerial(path string, baud int) (*Link, error) {
	if baud == 0 {
		baud = DefaultBaud
	}
	l := &Link{Port: path, Baud: baud, hub: newPushHub()}
	c, self, err := serialConnect(path, baud, l.hub)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("%w (is this node running a build with the USB serial interface?)", err)
	}
	l.Client, l.Self = c, self
	return l, nil
}
