package meshgore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tinygo.org/x/bluetooth"
)

// Nordic UART Service, as used by the MeshCore companion protocol.
const (
	serviceUUIDStr = "6E400001-B5A3-F393-E0A9-E50E24DCCA9E"
	rxUUIDStr      = "6E400002-B5A3-F393-E0A9-E50E24DCCA9E" // app -> firmware (write)
	txUUIDStr      = "6E400003-B5A3-F393-E0A9-E50E24DCCA9E" // firmware -> app (notify)
)

var (
	adapter = bluetooth.DefaultAdapter

	serviceUUID = mustUUID(serviceUUIDStr)
	rxUUID      = mustUUID(rxUUIDStr)
	txUUID      = mustUUID(txUUIDStr)
)

func mustUUID(s string) bluetooth.UUID {
	u, err := bluetooth.ParseUUID(s)
	if err != nil {
		panic(fmt.Sprintf("meshgore: bad UUID %q: %v", s, err))
	}
	return u
}

// Enable turns on the local Bluetooth adapter. Call it once before Scan.
func Enable() error { return adapter.Enable() }

// Scan looks for the companion advertising as "MeshCore-<name>" and returns it
// once found. With an empty name it lists nearby MeshCore nodes through Logf
// for 10 seconds and then returns an error.
func Scan(name string) (bluetooth.ScanResult, error) {
	var target bluetooth.ScanResult
	var fname string
	found := false

	if name != "" {
		fname = "MeshCore-" + name
		logf("Looking for %q", fname)
	}
	logf("Scanning...")
	if name == "" {
		// Nothing to look for: list what is nearby for a few seconds, then stop.
		time.AfterFunc(10*time.Second, func() { _ = adapter.StopScan() })
	}
	err := adapter.Scan(func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
		dname := result.LocalName()
		if !strings.HasPrefix(dname, "MeshCore-") {
			return
		}
		logf("%q (%s) RSSI: %d", dname, result.Address.String(), result.RSSI)
		if dname == fname {
			target = result
			found = true
			_ = adapter.StopScan()
			logf("Found: %s", dname)
		}
	})
	if err != nil {
		return target, fmt.Errorf("scan error: %w", err)
	}
	if !found {
		if name == "" {
			return target, errors.New("no name given; the MeshCore nodes seen were logged")
		}
		return target, errors.New("device not found")
	}
	return target, nil
}

// Link is a connection to a companion that can be re-established. Client and
// Device change on Reconnect, so do not keep copies of them across one.
type Link struct {
	Addr   bluetooth.Address
	Device bluetooth.Device
	Client *Client
	Self   *SelfInfo // the node's own details, from the first handshake
}

// Dial connects to the companion at addr and runs the handshake (APP_START and
// DEVICE_QUERY; the result is in Self and Client.Info). The first attempt may
// trigger OS pairing, after which the connection can go stale; reconnecting
// once the bond exists works, so it tries up to 3 times.
func Dial(addr bluetooth.Address) (*Link, error) {
	l := &Link{Addr: addr}
	for attempt := 1; ; attempt++ {
		device, client, self, err := connectAndHandshake(addr)
		if err == nil {
			l.Device, l.Client, l.Self = device, client, self
			return l, nil
		}
		logf("attempt %d failed: %v", attempt, err)
		if attempt == 3 {
			return nil, errors.New("giving up")
		}
		logf("Reconnecting...")
		time.Sleep(2 * time.Second)
	}
}

// Alive reports whether the BLE connection is still up.
func (l *Link) Alive() bool {
	ok, err := l.Device.Connected()
	return err == nil && ok
}

// Reconnect replaces a dropped connection, retrying with exponential backoff
// (2s, 4s, ... up to 1 minute) until it succeeds or ctx is cancelled. The new
// Client replaces l.Client.
func (l *Link) Reconnect(ctx context.Context) error {
	_ = l.Device.Disconnect()
	delay := 2 * time.Second
	for attempt := 1; ; attempt++ {
		device, client, _, err := connectAndHandshake(l.Addr)
		if err == nil {
			l.Device, l.Client = device, client
			logf("Reconnected")
			return nil
		}
		logf("reconnect attempt %d failed: %v (retrying in %s)", attempt, err, delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay *= 2; delay > time.Minute {
			delay = time.Minute
		}
	}
}

// Close disconnects from the companion.
func (l *Link) Close() { _ = l.Device.Disconnect() }

// connectAndHandshake connects, discovers the service and characteristics,
// subscribes to notifications and runs APP_START and DEVICE_QUERY. On error the
// connection is closed.
func connectAndHandshake(addr bluetooth.Address) (device bluetooth.Device, client *Client, self *SelfInfo, err error) {
	// The BLE library can panic on a half-connected peripheral (seen on macOS
	// right after the node reboots); report that as a failed attempt so callers
	// retry instead of crashing.
	defer func() {
		if r := recover(); r != nil {
			client, self, err = nil, nil, fmt.Errorf("connection failed: %v", r)
		}
	}()
	device, err = adapter.Connect(addr, bluetooth.ConnectionParams{})
	if err != nil {
		return device, nil, nil, fmt.Errorf("connect: %w", err)
	}
	if device.Address == (bluetooth.Address{}) {
		// On macOS, Connect can return an empty Device and a nil error when the
		// peripheral reports disconnected; using it would crash.
		return device, nil, nil, errors.New("connect: peripheral is not connected yet")
	}
	defer func() {
		if err != nil {
			func() {
				defer func() { _ = recover() }()
				device.Disconnect()
			}()
		}
	}()
	logf("Connected!")

	services, err := device.DiscoverServices([]bluetooth.UUID{serviceUUID})
	if err != nil {
		return device, nil, nil, fmt.Errorf("service discovery: %w", err)
	}
	if len(services) == 0 {
		return device, nil, nil, fmt.Errorf("service %s not found", serviceUUIDStr)
	}
	svc := services[0]
	logf("Service: %s", svc.UUID().String())

	chars, err := svc.DiscoverCharacteristics([]bluetooth.UUID{rxUUID, txUUID})
	if err != nil {
		return device, nil, nil, fmt.Errorf("characteristic discovery: %w", err)
	}
	var rx, tx *bluetooth.DeviceCharacteristic
	for i := range chars {
		switch chars[i].UUID() {
		case rxUUID:
			rx = &chars[i]
		case txUUID:
			tx = &chars[i]
		}
	}
	if rx == nil || tx == nil {
		return device, nil, nil, errors.New("RX/TX characteristic not found")
	}
	logf("RX (write):   %s", rx.UUID().String())
	logf("TX (notify):  %s", tx.UUID().String())

	client, err = NewClient(rx, tx)
	if err != nil {
		return device, nil, nil, err
	}
	logf("Notifications enabled")

	self, err = client.AppStart("mc-go")
	if err != nil {
		return device, nil, nil, err
	}
	if _, err = client.DeviceQuery(); err != nil {
		return device, nil, nil, err
	}
	return device, client, self, nil
}
