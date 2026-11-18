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

// knownPrefix is the usual start of a companion's Bluetooth name. Other builds use
// other prefixes (see TODO.md); the service UUID is what finds those.
const knownPrefix = "MeshCore-"

// nameMatches reports whether an advertised Bluetooth name is the node called
// want. want may be the full name ("MeshCore-Node") or what follows the first
// "-" of it ("Node"), so a node advertising with another prefix is still found.
func nameMatches(advertised, want string) bool {
	if want == "" {
		return false
	}
	if advertised == want {
		return true
	}
	_, rest, ok := strings.Cut(advertised, "-")
	return ok && rest == want
}

// isCompanion reports whether a scan result looks like a companion: it offers the
// Nordic UART service the protocol runs on, or has the usual name prefix (for a
// result whose advertisement left out the service list).
func isCompanion(hasService bool, name string) bool {
	return hasService || strings.HasPrefix(name, knownPrefix)
}

// ErrScanOnly is what Scan returns when it was given no name: it only listed what it saw.
var ErrScanOnly = errors.New("no name given; the companions seen were logged")

// Scan looks for the companion called name (see nameMatches) and returns it once
// found. Devices are recognised by the service UUID they advertise, or by the
// "MeshCore-" name prefix. With an empty name it lists the nodes it sees through
// Logf for 10 seconds and then returns an error.
func Scan(name string) (bluetooth.ScanResult, error) {
	var target bluetooth.ScanResult
	found := false
	listed := map[string]bool{}

	if name != "" {
		logf("Looking for %q", name)
	}
	logf("Scanning...")
	// Without a name, list what is nearby for a few seconds. With one, give up if
	// the node never shows (it may be off, out of range or connected to another app).
	limit := 30 * time.Second
	if name == "" {
		limit = 10 * time.Second
	}
	stop := time.AfterFunc(limit, func() { _ = adapter.StopScan() })
	defer stop.Stop()
	unnamed := map[string]int16{} // companions seen so far without a name, with their signal
	err := adapter.Scan(func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
		dname := result.LocalName()
		if !isCompanion(result.HasServiceUUID(serviceUUID), dname) {
			return
		}
		// The name often comes in a later packet than the service list, so wait for it
		// before listing the device.
		if addr := result.Address.String(); dname == "" {
			if !listed[addr] {
				unnamed[addr] = result.RSSI
			}
		} else if !listed[addr] {
			listed[addr] = true
			delete(unnamed, addr)
			logf("%q (%s) RSSI: %d", dname, addr, result.RSSI)
		}
		if nameMatches(dname, name) {
			target = result
			found = true
			_ = adapter.StopScan()
			logf("Found: %s", dname)
		}
	})
	for addr, rssi := range unnamed {
		logf("(no name) (%s) RSSI: %d", addr, rssi)
	}
	if err != nil {
		return target, fmt.Errorf("scan error: %w", err)
	}
	if !found {
		if name == "" {
			return target, ErrScanOnly
		}
		return target, fmt.Errorf("device %q not found after %s (is it on, in range and not connected to another app?)", name, limit)
	}
	return target, nil
}

// Link is a connection to a companion that can be re-established. Client and
// Device change on Reconnect, so do not keep copies of them across one.
type Link struct {
	Addr   bluetooth.Address // Bluetooth
	Device bluetooth.Device
	Port   string // serial: the port, instead of Addr and Device
	Baud   int
	hub    *pushHub // unsolicited frames; outlives the Client, which a reconnect replaces
	Client *Client
	Self   *SelfInfo // the node's own details, from the first handshake
}

// DialAttempts is how many times Dial tries to connect before giving up.
var DialAttempts = 10

// Dial connects to the companion at addr and runs the handshake (APP_START and
// DEVICE_QUERY; the result is in Self and Client.Info). The first attempt may
// trigger OS pairing, after which the connection can go stale; reconnecting
// once the bond exists works, so it tries up to DialAttempts times.
func Dial(addr bluetooth.Address) (*Link, error) {
	l := &Link{Addr: addr, hub: newPushHub()}
	for attempt := 1; ; attempt++ {
		device, client, self, err := connectAndHandshake(addr, l.hub)
		if err == nil {
			l.Device, l.Client, l.Self = device, client, self
			return l, nil
		}
		logf("attempt %d failed: %v", attempt, err)
		if attempt >= DialAttempts {
			return nil, errors.New("giving up")
		}
		logf("Reconnecting...")
		time.Sleep(2 * time.Second)
	}
}

// Alive reports whether the connection is still up.
func (l *Link) Alive() bool {
	if l.Port != "" {
		return l.Client.tr.Alive()
	}
	ok, err := l.Device.Connected()
	return err == nil && ok
}

// disconnect drops the current connection.
func (l *Link) disconnect() {
	if l.Port != "" {
		if l.Client != nil {
			_ = l.Client.tr.Close()
		}
		return
	}
	_ = l.Device.Disconnect()
}

// connect makes one attempt to connect again and replaces l.Client on success.
func (l *Link) connect() error {
	if l.Port != "" {
		client, _, err := serialConnect(l.Port, l.Baud, l.hub)
		if err == nil {
			l.Client = client
		}
		return err
	}
	device, client, _, err := connectAndHandshake(l.Addr, l.hub)
	if err == nil {
		l.Device, l.Client = device, client
	}
	return err
}

// Reconnect replaces a dropped connection, retrying with exponential backoff
// (2s, 4s, ... up to 1 minute) until it succeeds or ctx is cancelled. The new
// Client replaces l.Client.
func (l *Link) Reconnect(ctx context.Context) error {
	l.disconnect()
	delay := 2 * time.Second
	for attempt := 1; ; attempt++ {
		err := l.connect()
		if err == nil {
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
func (l *Link) Close() { l.disconnect() }

// connectAndHandshake connects, discovers the service and characteristics,
// subscribes to notifications and runs APP_START and DEVICE_QUERY. On error the
// connection is closed.
func connectAndHandshake(addr bluetooth.Address, hub *pushHub) (device bluetooth.Device, client *Client, self *SelfInfo, err error) {
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

	client, err = newBLEClient(rx, tx, hub)
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

// FindCompanion scans for the companion called name and returns its address.
func FindCompanion(name string) (bluetooth.Address, error) {
	r, err := Scan(name)
	return r.Address, err
}
