package meshgore

import (
	"strings"
	"time"

	"tinygo.org/x/bluetooth"
)

// transport carries companion frames. The protocol is the same over every
// transport; what differs is how a frame is delivered. Received frames go to
// Client.onNotify.
type transport interface {
	// WriteFrame sends one protocol frame.
	WriteFrame(frame []byte) error
	// Alive reports whether the connection is still up.
	Alive() bool
	// Close ends the connection.
	Close() error
}

// bleTransport writes frames to the Nordic UART RX characteristic. Frames
// arrive through a notification callback set up by NewClient, and Link tracks the
// connection itself.
type bleTransport struct {
	rx *bluetooth.DeviceCharacteristic
}

func (t *bleTransport) Alive() bool  { return true }
func (t *bleTransport) Close() error { return nil }

// pairingWait is how long write keeps retrying while the user pairs the device.
const pairingWait = 60 * time.Second

// WriteFrame sends one frame. If the device requires encryption (ATT 0x0f), the OS
// starts pairing and prompts for the BLE PIN; keep retrying until it succeeds.
func (t *bleTransport) WriteFrame(cmd []byte) error {
	deadline := time.Now().Add(pairingWait)
	warned := false
	for {
		_, err := t.rx.Write(cmd)
		if err == nil {
			return nil
		}
		logf("write failed (%s): %v", time.Until(deadline).Round(time.Second), err)
		// A write that blocks behind the pairing dialog times out instead; that
		// is returned so the caller can reconnect with the new bond.
		msg := err.Error()
		retryable := strings.Contains(msg, "Insufficient Encryption")
		if !retryable || time.Now().After(deadline) {
			return err
		}
		if !warned {
			logf("Device requires pairing: enter the BLE PIN in the system dialog (default 123456)...")
			warned = true
		}
		time.Sleep(2 * time.Second)
	}
}
