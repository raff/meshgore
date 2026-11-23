# meshgore

A Go library for talking to [MeshCore](https://meshcore.io) nodes, and `mc`, a
command-line tool built on it.

- **Library** (`github.com/raff/meshgore`): connect to a companion node over
  Bluetooth LE or USB serial, then read its info, channels, contacts and messages,
  send messages, and log in to and administer repeaters and room servers through
  it. See [Using the library](#using-the-library).
- **Tool** (`mc`, in `cmd/mc`): a command-line client and interactive session
  for the same things, with JSON output and exit codes for use from cron. The rest
  of this README describes `mc`.

`mc` talks to a **companion** node over Bluetooth LE. Besides reading your own node it can
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
  none). Inside it, `status` and `telemetry` read the repeater, `help [COMMAND]` describes them, `exit` logs out, and safe mode
  applies. With more after the name (`repeater NAME status`) it is the ordinary command.
- `mc -name=MyNode repl -repeater MyRepeater` starts the session already logged in to that
  repeater, so there is no `repeater MyRepeater` to type (`MC_REPEATER` sets the default for
  `-repeater`, and `MC_NAME` the one for `-name`; wherever a contact is named, `-` stands for
  the `MC_REPEATER` repeater, as in `mc repeater - status`). It takes `-password` (or `MC_PASSWORD`;
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
`-sort name|advert|type|distance` (the default is by name; distance is nearest first), and `-miles`. Each contact with a stored route shows `direct` or `N hops`, and one with a position shows its distance from this node (when this node has a position too); `-json` has `hops` and `distance_km`. A contact the node never heard has no advert
time and counts as stale. The times are the advertising nodes' **own clocks**: a node with a
wrong clock can look old, or be dated in the future, and those are shown as "its clock is ahead"
(kept by `-max-age`, since it was clearly heard, and ranked as recent when sorting by `advert`).
With `-json` the list is `{"total": N, "contacts": [{"name", "type", "key", "last_advert", "age_s", ...}]}`.

Messages are printed as `[time] channel: sender: text`; direct messages show the
sender's name when it is in the contact cache (`contacts` fills it) and the key prefix if not.

### Send a message

```
./mc -name=MyNode send '#healthcheck' MHC-1234
./mc -name=MyNode send 1 "hello"                 # a channel by slot number
```

`send CHANNEL TEXT...` sends text on a channel, by name or by slot number, and exits. A node never
receives its own channel message and channel messages have no acknowledgement, so a plain `send`
only tells you that the node accepted it. A channel message holds at most 133 bytes (the node adds
your name in front); a longer one is refused instead of being cut off silently. Direct messages have
no limit enforced, since theirs is not confirmed. For a healthcheck, `send` the code on `#healthcheck`
and look it up on the healthcheck page; see "Aliases" below for a `healthcheck` shortcut.

### Send and wait for the answers

```
./mc -name=MyNode send -wait 45s -only -match '$node' '#test' test    # who answers a test?
```

`send -wait DURATION` sends, then listens on the same channel for that long and ends with
`OK: 5 matching message(s) from A, B, ...`, or `FAILED` (exit 1) if nothing came. `-match TEXT`
only counts messages containing TEXT (any case; `-match '$node'` is this node's own name, to see just the answers that mention you), `-first` stops at the first one, and `-only`
hides the other messages that arrive meanwhile (it says how many; they are read from the node,
not kept). It needs something to answer: the bots on a `#test` channel do, but the observers of a
healthcheck only listen, so a healthcheck is sent without `-wait`. The bots may answer a sender only
once in a while (10 to 15 minutes looked right), so a test sent twice in a row can come back empty.

### Aliases

```
./mc alias test = send -wait 45s -only -match '$node' '#test' test
./mc alias healthcheck 'send #healthcheck $1'
./mc alias                  # list them
./mc alias -d healthcheck   # remove one
./mc -name=MyNode test
./mc -name=MyNode healthcheck MHC-1234
```

An alias names a command line. They are kept in `mc/aliases` in your config directory (on macOS,
`~/Library/Application Support/mc/aliases`), one `name = commands` per line, so you can also edit
the file. `$1`..`$9` and `$*` stand for the words typed after the alias; with none in the
definition, the words are added to the end of the last command. `$node` (or `$nodename`) is the
name of the node mc is connected to, as other nodes see it, so `-match $node` keeps only the
answers that mention you; mc connects to look it up when an alias uses it. Commands are chained with `;`
and stop at the first one that fails. An alias cannot replace a built-in command or use another alias.
Nothing is defined by default. Aliases work in the interactive session too, and complete there.
In a shell, quote the definition so that the shell does not expand `$1` or `$node`.

The two used most, for the Bay Area mesh (the channels must be on the node: `channel add '#test'`
and `channel add '#healthcheck'`):

| alias | definition | use |
| --- | --- | --- |
| `test` | `send -wait 45s -only -match $node #test test` | send `test` on `#test`, then list the nodes that answered you (exit 1 if none) |
| `healthcheck` | `send #healthcheck $1` | send a healthcheck code: `healthcheck MHC-1234` |

### Set up a fresh node

A reset or reflashed node starts over: no name, no position, none of your channels but
`Public`, no contacts, and a new identity (a new key, unless you saved the old private key
and restore it). The other nodes know it again only after it advertises. Do these, in order:

| step | command | why |
| --- | --- | --- |
| name | `set name "MyNode"` | the name the node advertises and signs its channel messages with |
| position | `set position 48.8584,2.2945` | shown on other nodes' maps and used for the distances in `contacts` |
| clock | `clock sync` | a node without a clock stamps its adverts and messages wrongly |
| radio | `radio set 910.525,62.5,7,5` | the settings of your mesh (frequency MHz, bandwidth kHz, SF, CR); nodes on other settings cannot hear you |
| channels | `channel add '#healthcheck'`, `channel add '#test'` | `Public` is already in slot 0 (key `8b3387e9c5cdea6ac9e5edbaa115cd72`); a `#hashtag` channel needs no secret |
| contacts | `auto-add all` (or only some types) | which heard adverts the node saves as contacts; `auto-add show` says what it does now |
| reboot | power-cycle, or `-safe=false cli reboot` | the Bluetooth name (`MeshCore-MyNode`) is built at boot, so it keeps the old one until then |
| announce | `advert flood` | the others learn the new name and key |
| refill contacts | `discover -save`, `adverts -save -wait 5m` | a reset empties the contact list; until a node advertises again its name is unknown. `discover -save` takes the ones in direct range now |

Then, once per computer (aliases belong to the computer, not to the node; see "Aliases"):

```
./mc alias test = send -wait 45s -only -match '$node' '#test' test
./mc alias healthcheck 'send #healthcheck $1'
```

All together, as a script to copy and adapt (`sh init-node.sh MyNode`):

```
#!/bin/sh
# First-time setup of a node, given its Bluetooth name. Edit the values for your mesh.
mc="./mc -name=$1"
$mc set name "$1"
$mc set position 48.8584,2.2945
$mc clock sync
$mc radio set 910.525,62.5,7,5
$mc channel add '#healthcheck'
$mc channel add '#test'
$mc auto-add all
$mc -safe=false cli reboot          # waits for the node to come back
$mc advert flood
$mc discover -save                  # the repeaters in range, so their names show up
```

Also worth a look on a new node, not part of the script because it depends on your mesh:
`path-hash` (the hop-hash size must suit the repeaters around you), `scope set REGION` (if your
mesh tags floods with a region), `gps on` (a board with GPS), `set share-location share` and
`set multi-acks`. `info` shows what the node is set to now, and `channels` and `contacts` the rest.
A first-use script that `mc` runs by itself is on the list of things to build (`TODO.md`).

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

