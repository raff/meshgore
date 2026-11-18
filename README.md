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
./mc -name=MyNode info
```

`mc [options] <command> [arguments]`. The options choose the node and how to
behave; the command says what to do. `mc help` lists the commands and
`mc help <command>` describes one. Flags of a command go before its other arguments
(`mc dm -h`, `mc watch -csv=out.csv 5m`).

`-name` is the part of the BLE name after the first `-`: the node advertising as
`MeshCore-MyNode` is `-name=MyNode`. The full advertised name (`-name=MeshCore-MyNode`)
works too, which is how to select a node with a different prefix. Nodes are
recognised by the Nordic UART service they advertise, or by the `MeshCore-` prefix.
`mc scan` lists the companions in range.

`go run ./cmd/mc <options> <command>` works the same.

## Interactive session

```
./mc -name=MyNode            # no command: an interactive session
./mc -serial=/dev/cu.usbmodem14301 repl
```

`mc` connects once and then runs the commands you type, so there is no reconnecting, pairing or
repeater login for each one:

```
mc MyNode> status
mc MyNode> dm Bob "on my way"
mc MyNode> repeater MyRepeater
Logged in. Type its commands; status, telemetry, safe, exit.
MyRepeater# get name
> MyRepeater
MyRepeater# exit
mc MyNode>
```

- Every command works as on the command line, with the same arguments and the same `-h`. The
  options (`-name`, `-safe`, ...) are the ones the session started with. Quote words with spaces.
- New messages are printed as they arrive, above the prompt. Anything you were typing
  disappears from view until the next keystroke, but it is still there. Messages are held back
  while a command runs or you are logged in to a repeater, and shown afterwards.
- Ctrl-C stops the command that is running (`watch`, `messages -listen`) and keeps the session.
  Ctrl-D or `exit` leaves it.
- Tab completes command names, the usual words after them and contact names. The up arrow
  recalls earlier lines; the history is kept in the user cache directory (`mc/history`).
- `repeater NAME`, with nothing after the name, logs in and sends what you type to the repeater
  (a password is asked for unless `-password` or `MC_PASSWORD` is given; `-password=` means
  none). Inside it, `status` and `telemetry` read the repeater, `exit` logs out, and safe mode
  applies. With more after the name (`repeater NAME status`) it is the ordinary command.
- `mc -name=MyNode repl -repeater MyRepeater` starts the session already logged in to that
  repeater, so there is no `repeater MyRepeater` to type. It takes `-password` (or `MC_PASSWORD`;
  `-password=` means none) and `-timeout`, and a failed login ends the session with an error.
  `exit` leaves the repeater and returns to the companion prompt; a second `exit` ends the session.
- `verbose` shows, `verbose on` and `verbose off` change the logging of every raw frame (the `-v` option).
- `safe` shows the read-only check for CLI commands, `safe off` and `safe on` change it; the
  prompt says `(unsafe)` while it is off.
- On a repeater console (`-serial=<port> -console`) the session works the same way, without
  messages. It also accepts piped input, so `printf 'status\nexit\n' | mc -name=MyNode` runs a script.
- A terminal narrower than the prompt plus 10 columns gets no line editing, only plain lines.

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
./mc ports                                          # show the serial ports
./mc -serial=/dev/cu.usbmodem14301 status
```

`-serial=<port>` connects over USB instead of Bluetooth: no scanning, no pairing and
no `-name` needed (the node's own name is used). Every command works the same,
including `cli reboot`, which waits for the port to come back.

The node must run a build with its USB serial interface enabled
(`ENABLE_USB_INTERFACE`). A build that has only Bluetooth does not answer, and `mc`
reports that the handshake timed out. `-baud` sets the speed (default 115200, which a
native USB port ignores). Tried on a LilyGo T-Deck (wadamesh fork, fw v27) over USB; the
Seeed T1000-E build is Bluetooth only. Opening a port can reset some boards
(ESP32 boards with a USB-serial chip reset when DTR toggles).

### Repeater or room server on USB

A repeater's USB port is a text console, not the companion protocol, so `-serial` alone
fails on it. Add `-console`:

```
./mc -serial=/dev/cu.usbmodem14201 -console cli ver
./mc -serial=/dev/cu.usbmodem14201 -console cli -c ver -c stats-core
./mc -serial=/dev/cu.usbmodem14201 -console -safe=false cli reboot
./mc -serial=/dev/cu.usbmodem14201 -console status -min-battery=3.6
./mc -serial=/dev/cu.usbmodem14201 -console watch -csv=repeater.csv 5m
```

`status` and `watch` run `stats-core`, `stats-radio` and `stats-packets` and print the same
statistics as for a companion (`-json`, `-csv`, `-min-battery` and `-max-noise` work, with exit
code 2 on an alert). Reading all three takes about 2 seconds, so a `watch` period shorter
than that is stretched. Commands that need a companion (messages, contacts, channels, ...) say so.

There is no login or password, and no radio hop; commands run on the node itself, so
the serial-only ones work: `stats-core`, `stats-radio`, `stats-packets` and `log` are
allowed in safe mode, `erase` and `get prv.key` need `-safe=false` (see `CLI.md`).
The console has no prompt or reply marker, so a reply ends after about 0.6 s of
silence; a command that never answers (`reboot`) takes 3 s. `reboot` waits for the
port to come back (`-reconnect-wait`). Tried on a Seeed SenseCAP Solar repeater
(v1.18.0): `ver`, `board`, `get name`, `neighbors` and the three `stats-*` commands work
(the `stats-*` replies are JSON), and `log` returns the whole captured log ending in
`EOF`. Each `log` call dumps the full log again; it does not clear it (`log start`, `log stop` and
`log erase` do work, and need `-safe=false`). `reboot` works (the port comes back and later commands run).

## Usage

### Look at the companion

```
./mc -name=MyNode info                 # name, key, position, radio, firmware
./mc -name=MyNode channels
./mc -name=MyNode messages             # print what is waiting
./mc -name=MyNode messages -listen=10m # ... and keep listening for new ones
./mc -name=MyNode contacts             # list contacts (only changes are fetched after the first time)
./mc -name=MyNode contacts -full       # discard the cache and download every contact again
./mc -name=MyNode contacts repeater,room                 # only some node types
./mc -name=MyNode contacts -sort advert -max-age 2d      # heard in the last two days, newest first
./mc -name=MyNode contacts -match solar -sort type       # names containing "solar"
```

`contacts` takes a list of node types (`chat`, `repeater`, `room`, `sensor`), `-match TEXT` for part
of a name (ignoring case), `-max-age 90m|36h|7d` to hide contacts not heard from for that long,
and `-sort name|advert|type` (the default is by name). A contact the node never heard has no advert
time and counts as stale. The times are the advertising nodes' **own clocks**: a node with a
wrong clock can look old, or be dated in the future, and those are shown as "its clock is ahead"
(kept by `-max-age`, since it was clearly heard, and ranked as recent when sorting by `advert`).
With `-json` the list is `{"total": N, "contacts": [{"name", "type", "key", "last_advert", "age_s", ...}]}`.

Messages are printed as `[time] channel: sender: text`; direct messages show the
sender's name when it is in the contact cache (`contacts` fills it) and the key prefix if not.

### Send a healthcheck

```
./mc -name=MyNode healthcheck MHC-1234
./mc -name=MyNode healthcheck -wait=60s MHC-1234
```

Sends the code as a message on the channel named `#healthcheck` and exits.
With `-wait`, it then waits for a message containing the code to
appear on that channel and exits 1 if none does. That only works if something
else repeats the code back: a node never receives its own channel message and
channel messages have no acknowledgement. `send CHANNEL TEXT...` sends any text on
any channel (by name or slot). A channel message holds at most 133 bytes (the node adds your name
in front); a longer one is refused instead of being cut off silently. Direct messages have no
limit enforced, since theirs is not confirmed.

### Set up a fresh node

A reflashed node has no name, no position and none of your channels:

```
./mc -name=MyNode set name "MyNode"
./mc -name=MyNode set position 48.8584,2.2945
./mc -name=MyNode channel add "#healthcheck"
./mc -name=MyNode advert flood
```

- `set name`: the name the node advertises. The device keeps at most 31 bytes and
  silently cuts off more, so `mc` refuses a longer name.
- `set position <lat>,<lon>`: in degrees. A node with GPS enabled may replace it
  with its own fix.
- `channel add <name>`: a `#hashtag` channel needs nothing else, since its secret
  comes from its name (the first 16 bytes of the SHA-256 of the name, `#`
  included) and anyone who knows the name can join. A private channel needs
  `-secret=<32 hex digits>`, or `-secret=random` to make one,
  which `mc` prints once. Keep it: others need it to join and it cannot be shown
  again (the node never reveals it).
- `-slot=<n>` picks the slot; the default is the first empty one. A used
  slot is only replaced with `-overwrite`.
- Adding a channel that is already there with the same secret does nothing, so the
  command is safe to repeat.

### See what the node supports

```
./mc -name=MyNode vars
```

Lists the node's custom variables (`GET_CUSTOM_VARS`): settings that exist only if
the board has the hardware, such as `gps` and `gps_interval` on a node with GPS.
The output says whether a `gps` variable is there, which is the way to tell if GPS
support was compiled in.

### Companion statistics

```
./mc -name=MyNode status
./mc -json -name=MyNode status
./mc -name=MyNode watch 5m
```

Battery, uptime, storage, noise floor, last RSSI and SNR, airtime and packet counts.
A part the firmware does not answer is left out. `watch` takes `-csv`, `-min-battery`
and `-max-noise` as for a repeater, and reconnects if the link drops.

### CLI commands on the companion

```
./mc -name=MyNode cli ver
./mc -name=MyNode cli get radio
./mc -name=MyNode cli -c ver -c "get tx"
./mc -name=MyNode -safe=false cli set tx 20
```

`cli` runs a CLI command on the companion itself (`CMD_RUN_CLI_COMMAND`, firmware
v14+), with the same safe-mode check as for a repeater. The words after the options
are one command; use `-c` for several. The companion's command set is much smaller;
unknown commands reply `Unknown command`. Tried on a T1000-E (v1.18.0): `ver`, `board`,
`get name`, `get radio`, `get tx`, `get freq`, `get radio.rxgain`, `get rxdelay`,
`get af` and `get tz.offset` work, and so does `set tx` (needs `-safe=false`).
`clock`, `gps`, `neighbors`, `get lat`, `get lon`, `get repeat` and `get public.key` do not.
Replies from `get` start with `> `.

`cli reboot` (with `-safe=false`) waits for the companion to come back (up to
`-reconnect-wait`, default 2 minutes) and then runs any later `-c` commands.

### Clock, radio and other companion settings

```
./mc -name=MyNode clock                         # show
./mc -name=MyNode clock sync                    # set from this computer
./mc -name=MyNode radio set 910.525,62.5,7,5    # freq MHz, bw kHz, SF, CR
./mc -name=MyNode tuning -rx-delay=0 -airtime-factor=1
./mc -name=MyNode set share-location share
./mc -name=MyNode set multi-acks 1
./mc -name=MyNode telemetry
```

- A T1000-E has no battery-backed clock, so its time drifts back after every
  reboot; `clock sync` fixes that. The firmware refuses to move a clock backwards.
- `radio set` is saved by the node. Nodes on different radio settings cannot hear
  each other, but the Bluetooth link is unaffected, so you can always set it back.
  The companion has no `tempradio` (no automatic revert).
- `telemetry` shows the node's own sensors: battery, temperature and, with a GPS
  fix, the position (`0,0` means no fix yet). For a repeater use
  `repeater NAME telemetry`.
- `set share-location` and `set multi-acks` are written together by the firmware;
  `mc` reads the current values first so the other one is kept.

### Contacts, channels and neighbours

```
./mc -name=MyNode auto-add show
./mc -name=MyNode auto-add -max-hops=2 -overwrite=on repeater,room
./mc -name=MyNode auto-add all                 # the default: save every advert
./mc -name=MyNode path-hash 1
./mc -name=MyNode discover repeater            # who is in direct range?
./mc -name=MyNode contact remove SomeNode
./mc -name=MyNode channel remove '#test'       # by name or slot number
```

- `auto-add` chooses which adverts the node saves as contacts: `all`, `none`, or a list of
  `chat`, `repeater`, `room`, `sensor`. A list or `none` turns on the node's "manual add" switch
  (otherwise the type list is ignored); `all` turns it off. `-max-hops` limits it to
  nodes within that many hops (0 = any), and `-overwrite=on` lets a full node
  replace its oldest contact. Adverts that are not saved still show up as "new advert" pushes.
- `path-hash` sets how many bytes of each hop's hash flood packets carry (mode + 1).
  The current mode is in `info`.
- `path CONTACT` sends a flood request to the contact and prints the repeaters it went through
  on the way out and the ones the reply came back through, named from the contact cache (a hash
  that matches several repeaters shows all of them). A repeater or room server answers only a
  node that has logged in, so give `-password P` (or `MC_PASSWORD`; a guest password is enough).
  An empty guest password is `-password=` (an empty `MC_PASSWORD` works too); with neither, no login is sent.
  Reaching it at all, with a path printed, is also a check that the repeater is up and in range.
- `discover` sends a one-hop request and lists the nodes that answer, with the SNR each
  heard the other at. Names come from the contact cache. Repeaters answer at most 4 times in
  2 minutes, so repeat it sparingly.
- `contact remove` deletes a contact from the node and from the local cache. The node saves
  it again when it hears another advert, if auto-add allows that type.
- `channel remove` empties a slot. The firmware has no delete: it accepts an empty name and
  zero secret and then ignores the slot. Slot 0 (Public) needs `-overwrite`.

### GPS on the companion

```
./mc -name=MyNode gps            # show
./mc -name=MyNode gps on
./mc -name=MyNode gps off
./mc -name=MyNode gps -interval=60
```

Needs a build with GPS; `vars` shows a `gps` variable if there is one. The
T1000-E on v1.18.0 has `gps` but rejects `gps_interval` (`ILLEGAL_ARG`), so
`-interval` works only on builds that list that variable.

### Flood scope (regions)

```
./mc -name=MyNode scope                  # show
./mc -name=MyNode scope set bay-area     # tag every flood this node sends with the region "bay-area"
./mc -name=MyNode scope clear            # back to unscoped floods
```

A region is a lowercase name (letters, digits and `-`, no `#`, up to 29 bytes). With a default scope
set, the node tags all its floods (adverts, direct messages and logins whose path is unknown, channel
messages) with it, and a repeater that has regions configured forwards a scoped flood only if it has
that region. **Check the repeaters you rely on before setting one**: your own repeater needs
`region put <name>` (and, on firmware before 1.15, `region allowf <name>`), or it will stop
forwarding what your companion sends. By default a node sends unscoped floods, which every repeater
forwards unless it has been told (`region denyf *`) to refuse them. Needs a firmware with the
default-scope commands (the T1000-E on v1.18.0 and the T-Deck on wadamesh v27 have them).

### Advertise the companion

```
./mc -name=MyNode advert flood
./mc -name=MyNode advert zerohop
```

Makes the companion broadcast its own advertisement, which is how other nodes
learn it exists. A flood advert is repeated by repeaters across the mesh; a
zero-hop advert is heard only by nodes in direct radio range.

### Send a direct message

```
./mc -name=MyNode dm "Some Contact" hello there
```

Sends a private message to a contact (name, hex key prefix, or full key; the same
lookup as `repeater`) and waits for the recipient's acknowledgement. Unlike
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
./mc -name=MyNode repeater -password=... MyRepeater status
./mc -name=MyNode repeater -password=... MyRepeater cmd ver
./mc -name=MyNode repeater -password=... -c ver -c "get name" MyRepeater
./mc -name=MyNode repeater -password=... MyRepeater status telemetry cmd get name
```

`mc` logs in through the companion, runs what you asked for, and logs out.
After the repeater's name come the things to do, in order: `status`, `telemetry`,
`watch <interval>` and `cmd <text...>` (the rest of the line is one command; use `-c`
before the name for several). With none of them, `status`.
The password is the **repeater's admin password**, not the BLE PIN. A wrong
password gets no reply at all, and `mc` reports "wrong password or repeater out
of range" after about 25 seconds. Set `MC_PASSWORD` instead of `-password` to
keep it out of the process list.

The repeater is a contact name, a hex key prefix (6+ hex digits) or a full
64-digit key. A full key needs no lookup. Otherwise the repeater has to be in
the node's contact list, which is slow to download the first time. `mc` keeps a
cache per node in `~/Library/Caches/mc/contacts-<name>.json` (`os.UserCacheDir`),
together with the timestamp of the last sync. When a name is not in the cache it
syncs again, and a sync only asks the node for contacts added or changed since
that timestamp, so after the first download it takes seconds. `contacts` does
the same sync on demand and lists everything.

An incremental sync cannot learn that a contact was *deleted* on the node. If
the cache drifts, or the node was reset, run `contacts -full` to discard it and
download everything again.

Each command waits for its reply, up to `-timeout` (default 30 s) and a quiet period of 3 s
after the last reply line. The available commands are in [CLI.md](CLI.md).

**Safe mode is on by default.** Only known read-only commands are accepted
(`ver`, `get ...`, `neighbors`, `clock`, ...). Anything else, including
`set`, `advert`, `reboot` and unknown commands, is refused before anything is
sent. Use `-safe=false` (an option before the command) to allow changes.

### Watch a repeater

```
MC_PASSWORD=... ./mc -name=MyNode repeater -csv=repeater.csv MyRepeater watch 5m
```

`watch <interval>` stays connected and reads the repeater's status every
interval until you press Ctrl-C. It logs in once, so there is no pairing or
login on each reading. Each reading prints one line; the thresholds
(`-min-battery`, `-max-noise`) print an `ALERT:` line when crossed, and the
program keeps running. `watch` cannot be combined with commands.

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
may disconnect after inactivity). `watch` and `messages -listen` check the link
and, when it drops, reconnect with exponential backoff (2 s, 4 s, ... up to 1
minute) until it works or you press Ctrl-C. After a reconnect a repeater `watch` logs in
to the repeater again, and `messages -listen` fetches the messages it missed.

### GPS on a repeater

A repeater with a GPS stores its position for adverts; the GPS only needs to run
long enough to get a fix. With the repeater outdoors:

```
./mc -name=MyNode -safe=false repeater -password=... MyRepeater cmd gps on
./mc -name=MyNode repeater -password=... MyRepeater cmd gps          # wait for "on, active, fix, N sats"
./mc -name=MyNode -safe=false repeater -password=... -c "gps setloc" -c "get lat" -c "get lon" -c "gps off" MyRepeater
```

`gps setloc` copies the fix into the repeater's stored location (`get lat` and
`get lon` read that stored value, not the live GPS), and the stored location stays
after `gps off`. `sensor list` shows which sensor settings the build has, the
repeater's counterpart of `vars`. See [CLI.md](CLI.md).

### Monitoring and cron

```
MC_PASSWORD=... ./mc -json -name=MyNode repeater -min-battery=3.6 -max-noise=-100 MyRepeater status
```

- `status` reads the repeater's statistics: battery, uptime, noise floor,
  last RSSI/SNR, packet counts, duplicates, receive errors and airtime.
- `-json` (an option before the command) prints one JSON object on stdout; all
  progress text goes to stderr.
- `-min-battery=<volts>` and `-max-noise=<dBm>` raise an alert when crossed
  (0 turns a limit off).

Exit codes:

| Code | Meaning |
| --- | --- |
| 0 | everything worked and no limit was crossed |
| 1 | failure: not found, connect or pairing failed, login failed, no reply, command refused |
| 2 | the run worked but a limit was crossed (see `alerts`) |
| 64 | wrong usage: unknown command, missing or bad arguments |

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

## Options and commands

Options come before the command.

| Option | Default | Meaning |
| --- | --- | --- |
| `-name` | | companion name: what follows the first `-` of its Bluetooth name (`MeshCore-MyNode` is `MyNode`), or the full name |
| `-serial` | | connect over USB serial instead: the port (see `mc ports`) |
| `-baud` | 115200 | with `-serial`: baud rate |
| `-console` | off | with `-serial`: the port is a repeater or room server's text console |
| `-json` | off | JSON result on stdout, progress on stderr |
| `-safe` | **true** | only run CLI commands known to be read-only; `-safe=false` allows anything |
| `-reconnect-wait` | 2m | after a reboot, how long to wait for the node to come back; `0` doesn't reconnect |
| `-cmd-timeout` | 30s | how long to wait for each CLI command's reply |
| `-v` | off | log every raw protocol frame (`->` sent, `<-` received) |

| Command | What it does |
| --- | --- |
| `info`, `vars`, `status`, `watch`, `telemetry` | look at the node |
| `clock`, `radio`, `tuning`, `gps`, `advert`, `set`, `auto-add`, `path-hash`, `scope` | read or change its settings |
| `messages`, `dm`, `send`, `healthcheck` | messages |
| `contacts`, `contact`, `channels`, `channel` | contacts and channels |
| `discover`, `path`, `repeater`, `cli` | the network, the route to a contact, a repeater, a CLI command on the node |
| `scan`, `ports`, `help`, `repl` | find nodes, list serial ports, help, an interactive session |

`mc help` has the arguments of each; `mc help <command>` its options.

## Things to know

- **Reading messages empties the node's queue.** Fetching a message removes it
  from the companion's queue, so a phone app connected later will not see it.
  The same applies to the replies `mc` collects while running commands.
- **Repeater commands can be disruptive.** `reboot`, `poweroff`, `set radio` and
  friends change a node you may not be able to reach again. That is why safe
  mode exists. See [CLI.md](CLI.md).
- **`watch` is not for cron.** It runs until stopped, so use it under something
  like `launchd` or `tmux`. For a periodic check, run a one-shot `status -json`
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
| `push.go` | the push dispatcher: `Subscribe`, one copy of each unsolicited frame per subscriber |
| `transport.go`, `serial.go`, `console.go` | Bluetooth and USB serial transports, and a repeater's text console |
| `link.go` | `Scan`, `Dial`, and `Link`: connect with pairing retry, detect a dropped connection, reconnect with backoff |
| `log.go` | `Logf` and `Debug`, the library's only output |
| `status_test.go` | library tests: status parsing, JSON, thresholds, the read-only allowlist |
| `cmd/mc/main.go` | the options, and running one command |
| `cmd/mc/session.go` | `session`: the connection a command runs on, and `splitArgs` |
| `cmd/mc/commands.go` | the command registry, flag helpers, `help` |
| `cmd/mc/cmd_node.go`, `cmd_mesh.go` | the commands |
| `cmd/mc/repl.go` | the interactive session: prompt, live messages, repeater mode, completion |
| `cmd/mc/ops.go` | what the commands call: repeater run, CLI on the node, statistics |
| `cmd/mc/setup.go` | the node actions (name, position, channels, radio, auto-add, discovery, ...) |
| `cmd/mc/dm.go` | direct messages |
| `cmd/mc/watch.go` | the `watch` loops and CSV logging |
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
- **Unsolicited frames.** `Client.Subscribe(codes...)` (or `Link.Subscribe`) returns a
  `*Sub` whose channel `C` delivers pushes (frames from `0x80` up) with those codes, or all
  of them if you give none; call `Close` when done. Every subscriber gets its own copy, so a
  live message display and a login in progress do not take frames from each other. A
  subscriber that stops reading loses frames (`Dropped` counts them) and never holds up
  the others. Subscribe before sending the request whose answer you wait for. A `Link`
  subscription survives `Reconnect`. When a frame starts with `meshgore.PushMsgWaiting`,
  call `SyncMessages`.
- **Platform.** It uses [tinygo.org/x/bluetooth](https://github.com/tinygo-org/bluetooth),
  so it needs the same platform setup, and the macOS pairing behaviour described above.

The import path is `github.com/raff/meshgore`; the package name is `meshgore`.

## How the protocol works, briefly

The companion exposes the Nordic UART service `6E400001-B5A3-F393-E0A9-E50E24DCCA9E`.
The app writes frames to RX (`6E400002-...`) and receives frames as
notifications on TX (`6E400003-...`). One write or notification is one frame, the
first byte is the type, and integers are little-endian. Frames below `0x80` answer the
command in flight; frames from `0x80` up are unsolicited pushes (message waiting,
login result, status response, ...). `mc` sends one command at a time, routes answers to the
command in flight, and hands pushes to a hub that gives every subscriber its own copy.

## License

MIT; see [LICENSE](LICENSE).
