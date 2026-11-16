# mc

A command-line client for [MeshCore](https://meshcore.io) that talks to a
**companion** node over Bluetooth LE. Besides reading your own node it can
administer a **remote repeater** through that companion, which is the way in
when the repeater has Bluetooth turned off.

What it does:

- scan for and connect to a companion, handling pairing
- read node and device info, channels, contacts and incoming messages
- send a message on a channel (a `#healthcheck` probe)
- log in to a repeater over the radio, read its status, and run CLI commands
- output JSON and exit codes for use from cron

This is an unofficial project and is not affiliated with MeshCore. It was developed
against a LilyGo T-Deck and a Seeed T1000-E companion and a repeater, on macOS.

It is built on [tinygo.org/x/bluetooth](https://github.com/tinygo-org/bluetooth)
and is developed and tested on macOS. Linux and Windows are supported by that
library but untested here.

## Build and run

```
go install github.com/raff/meshgore/cmd/mc@latest   # installs the mc command
```

or from a checkout:

```
go build -o mc ./cmd/mc
./mc -name=MyNode
```

`-name` is the part of the BLE name after the first `-`: the node advertising as
`MeshCore-MyNode` is `-name=MyNode`. The full advertised name (`-name=MeshCore-MyNode`)
works too, which is how to select a node with a different prefix. Nodes are
recognised by the Nordic UART service they advertise, or by the `MeshCore-` prefix.
Without `-name`, `mc` scans for 10 seconds,
lists the MeshCore nodes it can see and exits.

`go run ./cmd/mc <flags>` works the same.

## Pairing

The companion requires an encrypted link, so the first connection pairs.

1. macOS shows a pairing dialog. Enter the node's BLE PIN. The default is
   `123456`, but nodes with a screen often show their own, and a firmware build
   can set another one.
2. The first attempt usually times out while the dialog is open. `mc`
   reconnects (up to 3 attempts) once the bond exists.
3. Later runs connect without a dialog.

A wrong PIN looks like this: the first write fails with `ATT error 0x0f
(Insufficient Encryption)`, then writes time out and no reply ever arrives.

Only one BLE client can use a companion at a time, so close the MeshCore app on
your phone first. If a node seems stuck, power-cycle it and remove it from
System Settings -> Bluetooth to pair again.

## USB serial

```
./mc -serial=list                          # show the serial ports
./mc -serial=/dev/cu.usbmodem14301 -status
```

`-serial=<port>` connects over USB instead of Bluetooth: no scanning, no pairing and
no `-name` needed (the node's own name is used). Everything else works the same,
including `-cmd=reboot`, which waits for the port to come back.

The node must run a build with its USB serial interface enabled
(`ENABLE_USB_INTERFACE`). A build that has only Bluetooth does not answer, and `mc`
reports that the handshake timed out. `-baud` sets the speed (default 115200, which a
native USB port ignores). **Not yet tried against real hardware**; the framing is from the
firmware source and is tested against a fake node. Opening a port can reset some boards
(ESP32 boards with a USB-serial chip reset when DTR toggles).

## Usage

### Look at the companion

```
./mc -name=MyNode                  # info, channels, queued messages, then listen 15 s
./mc -name=MyNode -listen=10m      # keep listening for new messages
./mc -name=MyNode -listen=0        # sync once and exit
./mc -name=MyNode -contacts        # also list contacts (only changes after the first time)
./mc -name=MyNode -full-sync       # discard the cache and download every contact again
```

Messages are printed as `[time] channel: sender: text`; direct messages show the
sender's name if contacts are loaded (`-contacts`) and the key prefix if not.

### Send a healthcheck

```
./mc -name=MyNode -healthcheck=MHC-1234
./mc -name=MyNode -healthcheck=MHC-1234 -healthcheck-wait=60s
```

Sends the code as a message on the channel named `#healthcheck` and exits.
With `-healthcheck-wait`, it then waits for a message containing the code to
appear on that channel and exits 1 if none does. That only works if something
else repeats the code back: a node never receives its own channel message and
channel messages have no acknowledgement.

### Set up a fresh node

A reflashed node has no name, no position and none of your channels. One command
can set all of that and announce the node:

```
./mc -name=MyNode -set-name="MyNode" -set-position=48.8584,2.2945 \
     -add-channel="#healthcheck" -advert=flood
```

The steps run in the order name, position, channel, advert, and everything is
checked before the first change, so a typo in a later flag does not leave the node
half configured.

- `-set-name`: the name the node advertises. The device keeps at most 31 bytes and
  silently cuts off more, so `mc` refuses a longer name.
- `-set-position=<lat>,<lon>`: in degrees. A node with GPS enabled may replace it
  with its own fix.
- `-add-channel=<name>`: a `#hashtag` channel needs nothing else, since its secret
  comes from its name (the first 16 bytes of the SHA-256 of the name, `#`
  included) and anyone who knows the name can join. A private channel needs
  `-channel-secret=<32 hex digits>`, or `-channel-secret=random` to make one,
  which `mc` prints once. Keep it: others need it to join and it cannot be shown
  again (the node never reveals it).
- `-channel-slot=<n>` picks the slot; the default is the first empty one. A used
  slot is only replaced with `-channel-overwrite`.
- Adding a channel that is already there with the same secret does nothing, so the
  command is safe to repeat.

### See what the node supports

```
./mc -name=MyNode -vars
```

Lists the node's custom variables (`GET_CUSTOM_VARS`): settings that exist only if
the board has the hardware, such as `gps` and `gps_interval` on a node with GPS.
The output says whether a `gps` variable is there, which is the way to tell if GPS
support was compiled in.

### Companion statistics

```
./mc -name=MyNode -status
./mc -name=MyNode -status -json
```

Without `-repeater`, `-status` reads the companion's own statistics: battery,
uptime, storage, noise floor, last RSSI and SNR, airtime and packet counts. A part
the firmware does not answer is left out.

### CLI commands on the companion

```
./mc -name=MyNode -cmd=ver -cmd=board -cmd="get radio"
```

Without `-repeater`, `-cmd` runs on the companion itself (`CMD_RUN_CLI_COMMAND`,
firmware v14+), with the same `-safe` check as for a repeater. The companion's
command set is much smaller; unknown commands reply `Unknown command`. Tried on a
T1000-E (v1.18.0): `ver`, `board`, `get name`, `get radio`, `get tx`, `get freq`,
`get radio.rxgain`, `get rxdelay`, `get af` and `get tz.offset` work, and so does
`set tx` (needs `-safe=false`). `clock`, `gps`,
`neighbors`, `get lat`, `get lon`, `get repeat` and `get public.key` do not.
Replies from `get` start with `> `.

`-cmd=reboot -safe=false` waits for the companion to come back (up to
`-reconnect-wait`, default 2 minutes) and then runs any later `-cmd`s.

### Clock, radio and other companion settings

```
./mc -name=MyNode -clock -tuning -telemetry     # read
./mc -name=MyNode -sync-clock                   # set the clock from this computer
./mc -name=MyNode -set-radio=910.525,62.5,7,5   # freq MHz, bw kHz, SF, CR
./mc -name=MyNode -rx-delay=0 -airtime-factor=1
./mc -name=MyNode -share-location=share -multi-acks=1
```

- A T1000-E has no battery-backed clock, so its time drifts back after every
  reboot; `-sync-clock` fixes that. The firmware refuses to move a clock backwards.
- `-set-radio` is saved by the node. Nodes on different radio settings cannot hear
  each other, but the Bluetooth link is unaffected, so you can always set it back.
  The companion has no `tempradio` (no automatic revert).
- `-telemetry` with `-repeater=<name> -password=...` asks a repeater for its sensor
  readings (the CLI has no `telemetry` command; this is a separate radio request,
  like `-status`). Without `-repeater` it shows
  the companion's own sensors: battery, temperature and, with a GPS
  fix, the position (`0,0` means no fix yet).
- `-share-location` and `-multi-acks` are written together by the firmware; `mc`
  reads the current values first so the other one is kept.
- `-watch=5m` without `-repeater` polls the companion's own statistics, with `-csv`,
  `-json`, `-min-battery` and `-max-noise` as for a repeater.

### GPS on the companion

```
./mc -name=MyNode -gps=on
./mc -name=MyNode -gps=off
./mc -name=MyNode -gps-interval=60
```

Needs a build with GPS; `-vars` shows a `gps` variable if there is one. The
T1000-E on v1.18.0 has `gps` but rejects `gps_interval` (`ILLEGAL_ARG`), so
`-gps-interval` works only on builds that list that variable.

### Advertise the companion

```
./mc -name=MyNode -advert=flood
./mc -name=MyNode -advert=zerohop
```

Makes the companion broadcast its own advertisement, which is how other nodes
learn it exists and add it to their contact lists. Use it after flashing a new
node, or when others cannot see yours.

- `flood`: repeaters rebroadcast it across the mesh, so distant nodes hear it.
- `zerohop`: only nodes in direct radio range hear it.

Nodes limit how often they accept adverts, so do not repeat it in a loop.
A node needs a name set before others can show anything useful for it.

### Send a direct message

```
./mc -name=MyNode -dm="Some Contact" -text="hello"
```

Sends a private message to a contact (name, hex key prefix, or full key; the same
lookup as `-repeater`) and waits for the recipient's acknowledgement. Unlike
channel messages, direct messages are acknowledged, so this tells you whether it
arrived.

Delivery follows the official client's rules: up to 3 attempts, all with the same
timestamp so the recipient sees the message once. After 2 unacknowledged attempts
the stored route is reset, so the next one floods. Once flooding, it stops after 2
attempts. It exits 0 when acknowledged and 1 otherwise; with `-json` it prints
`{"ok", "acked", "attempts", "flood", "round_trip_s", ...}`.

The reported time is the one the node measured for the radio exchange, which is
the more accurate figure; if the node does not report one, `mc` measures it itself
and says so (that includes the Bluetooth hop to the node).

"No acknowledgement" does not prove the message was lost. The recipient may have
received it while the ack went missing, so the message is not retried forever.
The recipient needs to be in the node's contact list.

### Administer a repeater

```
./mc -name=MyNode -repeater=MyRepeater -password=... -status
./mc -name=MyNode -repeater=MyRepeater -password=... -cmd=ver -cmd="get name"
```

`mc` logs in through the companion, runs what you asked for, and logs out.
The password is the **repeater's admin password**, not the BLE PIN. A wrong
password gets no reply at all, and `mc` reports "wrong password or repeater out
of range" after about 25 seconds. Set `MC_PASSWORD` instead of `-password` to
keep it out of the process list.

`-repeater` takes a contact name, a hex key prefix (6+ hex digits) or a full
64-digit key. A full key needs no lookup. Otherwise the repeater has to be in
the node's contact list, which is slow to download the first time. `mc` keeps a
cache per node in `~/Library/Caches/mc/contacts-<name>.json` (`os.UserCacheDir`),
together with the timestamp of the last sync. When a name is not in the cache it
syncs again, and a sync only asks the node for contacts added or changed since
that timestamp, so after the first download it takes seconds. `-contacts` does
the same sync on demand and lists everything.

An incremental sync cannot learn that a contact was *deleted* on the node. If
the cache drifts, or the node was reset, run `-full-sync` to discard it and
download everything again.

`-cmd` can be repeated. Each command waits for its reply, up to `-cmd-timeout`
(default 30 s) and a quiet period of 3 s after the last reply line. The
available commands are in [CLI.md](CLI.md).

**Safe mode is on by default.** Only known read-only commands are accepted
(`ver`, `get ...`, `neighbors`, `clock`, ...). Anything else, including
`set`, `advert`, `reboot` and unknown commands, is refused before anything is
sent. Use `-safe=false` to allow changes.

### Watch a repeater

```
MC_PASSWORD=... ./mc -name=MyNode -repeater=MyRepeater -watch=5m -csv=repeater.csv
```

`-watch=<interval>` stays connected and reads the repeater's status every
interval until you press Ctrl-C. It logs in once, so there is no pairing or
login on each reading. Each reading prints one line; the thresholds
(`-min-battery`, `-max-noise`) print an `ALERT:` line when crossed, and the
program keeps running. `-watch` cannot be combined with `-cmd`.

- `-csv=<file>` appends each successful reading to a CSV file, writing the
  header when the file is new. Columns: `time`, `battery_v`, `uptime_s`,
  `noise_floor_dbm`, `last_rssi_dbm`, `last_snr_db`, `tx_queue_len`,
  `packets_received`, `packets_sent`, `receive_errors`, `tx_airtime_s`,
  `rx_airtime_s`, `alerts`.
- With `-json`, each reading is one compact JSON object per line
  (`{"time": ..., "ok": ..., "status": {...}, "alerts": [...]}`). A failed
  reading has `"ok": false` and an `error`.
- If the repeater does not answer, the reading is reported as an error and
  `mc` logs in again before the next one, in case the repeater rebooted.
- Press Ctrl-C to stop; it logs out and exits 0.

### Reconnection

The companion drops the Bluetooth link now and then (the protocol doc says it
may disconnect after inactivity). `-watch` and a long `-listen` check the link
and, when it drops, reconnect with exponential backoff (2 s, 4 s, ... up to 1
minute) until it works or you press Ctrl-C. After a reconnect `-watch` logs in
to the repeater again, and `-listen` fetches the messages it missed.

### GPS on a repeater

A repeater with a GPS stores its position for adverts; the GPS only needs to run
long enough to get a fix. With the repeater outdoors:

```
./mc -name=MyNode -repeater=MyRepeater -password=... -safe=false -cmd="gps on"
./mc -name=MyNode -repeater=MyRepeater -password=... -cmd="gps"          # wait for "on, active, fix, N sats"
./mc -name=MyNode -repeater=MyRepeater -password=... -safe=false -cmd="gps setloc" -cmd="get lat" -cmd="get lon" -cmd="gps off"
```

`gps setloc` copies the fix into the repeater's stored location (`get lat` and
`get lon` read that stored value, not the live GPS), and the stored location stays
after `gps off`. `sensor list` shows which sensor settings the build has, the
repeater's counterpart of `-vars`. See [CLI.md](CLI.md).

### Monitoring and cron

```
MC_PASSWORD=... ./mc -name=MyNode -repeater=MyRepeater \
  -status -json -min-battery=3.6 -max-noise=-100
```

- `-status` reads the repeater's statistics: battery, uptime, noise floor,
  last RSSI/SNR, packet counts, duplicates, receive errors and airtime.
- `-json` prints one JSON object on stdout; all progress text goes to stderr.
- `-min-battery=<volts>` and `-max-noise=<dBm>` raise an alert when crossed
  (0 turns a limit off).

Exit codes:

| Code | Meaning |
| --- | --- |
| 0 | everything worked and no limit was crossed |
| 1 | failure: not found, connect or pairing failed, login failed, no reply, command refused |
| 2 | the run worked but a limit was crossed (see `alerts`) |

Example output:

```json
{
  "ok": true,
  "repeater": "MyRepeater",
  "key": "a1b2c3d4...",
  "status": {
    "battery_v": 4.13,
    "uptime_s": 5959,
    "noise_floor_dbm": -112,
    "last_rssi_dbm": -33,
    "last_snr_db": 12.25,
    "packets_received": 652,
    "packets_sent": 643,
    "receive_errors": 37,
    "tx_airtime_s": 195,
    "rx_airtime_s": 192
  },
  "commands": [{ "cmd": "ver", "replies": ["..."] }]
}
```

`alerts` (a list of strings) and `error` appear only when relevant. Failures
before the repeater step (no device found, connect failed) exit 1 with the
error on stderr and **nothing on stdout**, so a wrapper should treat empty
output plus a non-zero exit as a failure.

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-name` | | companion name: what follows the first `-` of its Bluetooth name (`MeshCore-MyNode` is `MyNode`), or the full name |
| `-v` | off | log every raw protocol frame (`->` sent, `<-` received) |
| `-listen` | 15s | how long to wait for new messages (reconnects if the link drops); `0` syncs once and exits |
| `-contacts` | off | sync and list contacts; after the first download only changes are fetched |
| `-full-sync` | off | discard the contact cache and download every contact again (implies `-contacts`) |
| `-healthcheck` | | code to send on `#healthcheck`, then exit |
| `-healthcheck-wait` | 0 | wait this long for the code to appear on the channel |
| `-serial` | | connect over USB serial: the port, or `list` to show the ports |
| `-baud` | 115200 | with `-serial`: baud rate |
| `-repeater` | | repeater to administer: name, hex key prefix or full key |
| `-password` | | repeater admin password (or `MC_PASSWORD`) |
| `-set-name` | | set the name the companion advertises (max 31 bytes) |
| `-set-position` | | set the advertised position, `<lat>,<lon>` in degrees |
| `-add-channel` | | add a channel; `#hashtag` names need no secret |
| `-channel-secret` | | with `-add-channel`: 32 hex digits, or `random` |
| `-channel-slot` | -1 | with `-add-channel`: slot to use (default: first empty) |
| `-channel-overwrite` | off | with `-add-channel`: replace a channel already in the slot |
| `-vars` | off | list the companion's custom variables, which show what its board supports (GPS, sensors) |
| `-set-radio` | | set the radio: `<freq MHz>,<bw kHz>,<sf>,<cr>` (saved) |
| `-sync-clock` | off | set the companion's clock from this computer |
| `-clock` | off | print the companion's clock |
| `-rx-delay` | | receive delay base, 0..20 |
| `-airtime-factor` | | airtime factor, 0..9 |
| `-tuning` | off | print the receive delay and airtime factor |
| `-share-location` | | `share` or `none`: whether adverts carry the location |
| `-multi-acks` | | extra acks to send, 0..255 |
| `-telemetry` | off | print sensor readings: the repeater's with `-repeater`, otherwise the companion's own (battery, temperature, GPS) |
| `-gps` | | switch the companion's GPS `on` or `off` (builds with GPS only) |
| `-gps-interval` | -1 | GPS update interval in seconds, 0..86400 (only on builds that list `gps_interval`) |
| `-advert` | | make the companion advertise itself: `flood` or `zerohop` |
| `-dm` | | send a direct message to this contact; needs `-text` |
| `-text` | | the message for `-dm` |
| `-status` | off | print statistics: the repeater's with `-repeater`, otherwise the companion's own |
| `-watch` | 0 | read status every interval (for example `5m`) until Ctrl-C; the repeater's with `-repeater`, otherwise the companion's own |
| `-csv` | | with `-watch`: append each reading to this CSV file |
| `-cmd` | | CLI command to run on the repeater, or on the companion itself without `-repeater`; repeatable |
| `-reconnect-wait` | 2m | after a `-cmd=reboot` on the companion, how long to wait for it to come back; `0` doesn't reconnect |
| `-cmd-timeout` | 30s | how long to wait for each command's reply |
| `-safe` | **true** | only allow read-only commands; `-safe=false` allows anything |
| `-json` | off | JSON result on stdout, progress on stderr |
| `-min-battery` | 0 | alert below this many volts |
| `-max-noise` | 0 | alert above this noise floor in dBm |

## Things to know

- **Reading messages empties the node's queue.** Fetching a message removes it
  from the companion's queue, so a phone app connected later will not see it.
  The same applies to the replies `mc` collects while running commands.
- **Repeater commands can be disruptive.** `reboot`, `poweroff`, `set radio` and
  friends change a node you may not be able to reach again. That is why safe
  mode exists. See [CLI.md](CLI.md).
- **`-watch` is not for cron.** It runs until stopped, so use it under something
  like `launchd` or `tmux`. For a periodic check, run a one-shot `-status -json`
  from cron instead.
- **Login and status are slow.** They cross the radio, usually as a flood. Each
  waits 8+ seconds per attempt, with up to 3 attempts.
- **The protocol document is incomplete.** The companion protocol page
  (<https://docs.meshcore.io/companion_protocol/>) says it is still in
  development. It covers the handshake, `DEVICE_QUERY`, channels, channel
  messages and the four message frame types, which `mc` follows. It does not
  cover contacts, login, status or direct messages. Those layouts follow the
  official [meshcore_py](https://github.com/meshcore-dev/meshcore_py) library
  and are marked as such in the code. Use `-v` to see raw frames when something
  looks wrong.
- **Firmware differences.** Observed on a LilyGo T-Deck running wadamesh
  `beta_101` (firmware v27): a channel send is answered with a plain `OK`
  (`0x00`) instead of the documented `MSG_SENT` (`0x06`), so both are accepted.
  The duplicate counters in the status frame read 0 on a busy repeater, which
  may be a firmware quirk.

## Code layout

The module is `github.com/raff/meshgore`: a reusable library at the module root and
the `mc` command in `cmd/mc`.

| File | Contents |
| --- | --- |
| `protocol.go` | `Client`: frame routing, command exchange, handshake, channels, contacts, messages, channel send |
| `node.go` | configure the node itself: name, position, channels |
| `dm.go` | direct messages: send, acknowledgement, retry and path reset |
| `repeater.go` | repeater login, CLI commands, status, `IsReadOnlyCommand`, `FindContact` |
| `link.go` | `Scan`, `Dial`, and `Link`: connect with pairing retry, detect a dropped connection, reconnect with backoff |
| `log.go` | `Logf` and `Debug`, the library's only output |
| `status_test.go` | library tests: status parsing, JSON, thresholds, the read-only allowlist |
| `cmd/mc/main.go` | flags, output, JSON, exit codes, the run modes |
| `cmd/mc/setup.go` | the `-set-name`, `-set-position`, `-add-channel` and `-advert` actions |
| `cmd/mc/dm.go` | the `-dm` run mode |
| `cmd/mc/watch.go` | the `-watch` loop and CSV logging |
| `cmd/mc/contacts.go` | the on-disk contact cache and name lookup |
| `CLI.md` | repeater CLI reference |

Run the tests with `go test ./...`.

## Using the library

```go
package main

import (
	"fmt"
	"log"

	"github.com/raff/meshgore"
)

func main() {
	meshgore.Logf = func(f string, a ...any) { fmt.Printf(f+"\n", a...) } // optional

	if err := meshgore.Enable(); err != nil {
		log.Fatal(err)
	}
	found, err := meshgore.Scan("MyNode")
	if err != nil {
		log.Fatal(err)
	}
	link, err := meshgore.Dial(found.Address)
	if err != nil {
		log.Fatal(err)
	}
	defer link.Close()

	fmt.Println("node:", link.Self.Name)

	channels, _ := link.Client.Channels()
	msgs, _ := link.Client.SyncMessages()
	_, _ = channels, msgs
}
```

Things to know:

- **Silent by default.** The library never prints or exits. Progress messages (scan
  results, pairing prompts, retries) go to `meshgore.Logf`, which discards them
  until you set it. `meshgore.Debug = true` also logs every raw frame through it.
- **`Link` and reconnects.** `Link.Client` is replaced by `Link.Reconnect`, so read
  it from the link each time instead of holding on to an old one. Check
  `Link.Alive()` to see whether the connection dropped.
- **Direct messages.** `Client.SendMessageWithRetry(ctx, key, text)` sends and waits
  for the acknowledgement; `Client.SendMessage` only queues one. Read the result's
  `Acked`, since an unacknowledged message is not an error.
- **One command at a time.** `Client` serialises commands and routes replies, so
  it is safe to call from several goroutines, but they will take turns.
- **Unsolicited frames.** `Client.Push` delivers frames from `0x80` up. When one
  starts with `meshgore.PushMsgWaiting`, call `SyncMessages`.
- **Platform.** It uses [tinygo.org/x/bluetooth](https://github.com/tinygo-org/bluetooth),
  so it needs the same platform setup, and the macOS pairing behaviour described above.

The import path is `github.com/raff/meshgore`; the package name is `meshgore`.

## How the protocol works, briefly

The companion exposes the Nordic UART service `6E400001-B5A3-F393-E0A9-E50E24DCCA9E`.
The app writes frames to RX (`6E400002-...`) and receives frames as
notifications on TX (`6E400003-...`). One write or notification is one frame, the
first byte is the type, and integers are little-endian. Frames below `0x80` answer the
command in flight; frames from `0x80` up are unsolicited pushes (message waiting,
login result, status response, ...). `mc` sends one command at a time and
routes the two kinds to different channels.

## License

MIT; see [LICENSE](LICENSE).
