# TODO

What is left, and what was learned along the way. Protocol notes say where a layout comes
from: the companion protocol doc, the firmware source (`examples/companion_radio/MyMesh.cpp`),
or `meshcore_py` (the official Python client), which is the reference wherever the doc is silent.
An item counts as done only after a run against a real node.

## To build

1. **Healthcheck delivery without an echo.** `healthcheck -wait` works only if another node
   repeats the code (it does between the T1000-E and the T-Deck). Radio log pushes (`0x88`)
   might show repeaters rebroadcasting our packet; the doc gives no layout, so work it out from
   real frames first.
2. **Companion commands not used yet.** From the firmware's command list; `meshcore_py` uses the
   same numbers. Layouts are not checked, so confirm them in the firmware source before building:
   - `0x36` (54) a flood scope key for the next sends, in RAM only (a per-send override of the default
     scope; sub-mode 1 forces unscoped). The default scope itself (63/64) is done as `scope`.
   - `0x3C` (60) allowed repeat frequencies (read): which frequencies may use the "repeat"
     option of `SET_RADIO_PARAMS`.
   - `0x3E` (62) data on a channel, `0x41` (65) a raw packet, `0x39` (57) an anonymous request,
     `0x32` (50) a binary request, `0x34` (52) path discovery.
   - `0x33` (51) factory reset, payload the text `reset`: left out on purpose, since it formats
     the node.
3. **Wi-Fi (TCP) companion**, not wanted for now. Builds with `ENABLE_WIFI_INTERFACE` serve the
   same `<`/`>` framed protocol on a TCP port (default 5000, per the firmware source). With the
   transport interface in place it is small: `-tcp=host:port`, a transport over `net.Conn`,
   reusing `frameReader` and `frameForNode`.
4. **Buzzer.** Document the button gesture in the README (a triple click flips it on the T1000-E,
   still to confirm on the device). Nothing more is possible until the firmware has a command
   (see the findings below).

## REPL: open points

The `repl` command works (see the README). What is left:

- Whether a repeater console session treats unknown words as `cli`, so you would not type `cli`
  before everything. Undecided; review later.
- `github.com/gobs/cmd` instead of the small loop in `repl.go`, if scripting (`source`,
  variables) is wanted. Its commands are `Call func(line string) bool`; `runLine` with
  `splitArgs` is what each would call.
- A script file option (`mc -f file`); piped input already runs as a script.
- The line being typed is hidden (not lost) when a message prints above the prompt. `liner`
  cannot redraw it; `chzyer/readline` can, at the cost of a different library.
- Ctrl-C does not interrupt a console `cli` command, or a repeater command inside a repeater
  session, that is waiting for its reply.
- Completion knows contact names only from the cache, and does not offer channel names for `send`.
- Messages are held back while a command runs, so a long `watch` shows none until it stops.

## Still to test on hardware

- `set name` and `set position`: afterwards the `Node` line and `pos` should show them.
- Power-cycling the companion in the middle of a long `watch` or `messages -listen`, and a long
  idle session across a Bluetooth drop. Only the reboot-and-reconnect of a one-shot command has
  been tried.
- `discover` with a room server or a sensor in range. Only the repeater has answered so far, so
  the type filter is unproven for other types.
- The session and `cli` on a room server's console (it also echoes line feeds).
- Whether opening a serial port resets other boards (DTR/RTS), and Linux and Windows for both
  Bluetooth and serial.
- Whether the T1000-E sets its own clock from GPS; the repeater does, and keeps it across a
  reboot. `clock sync` sets it from this computer.
- `contacts` times are the advertisers' clocks. Whether the node's own `lastmod` (its receive time, but
  on the node's clock) is a better age is untested.
- The T1000-E may have an accelerometer, but this firmware's `telemetry` does not list one.

## Open questions

- **Status duplicate counters read 0** on a busy repeater. Either the firmware does not count them
  or the offsets are shifted. Compare with the MeshCore app.
- **Channel send is answered with `OK`**, not the documented `MSG_SENT`. Both are accepted, but it
  means channel sends have no ack tag to wait for.
- **`write()` retries only on the macOS error text** (`Insufficient Encryption`,
  `timeout on Write`). Other platforms may phrase it differently, and BlueZ pairs differently
  (it may need `bluetoothctl` first).
- **Library API is unstable.** `Link`, `Client` and the message types may still change.

## Decided against

- Setting the companion's clock at every connection. `clock sync` does it on request; revisit only
  if a node's wrong clock turns out to matter (it stamps outgoing messages).
- Keeping the old option-style command line (`-watch`, `-set-radio`, ...). The commands replaced it.

## Housekeeping

- A `CHANGELOG` or release notes once the library has users.
- The module is `github.com/raff/meshgore`, the same as the repository. It changes only if the
  repository is renamed (`go.mod` and the imports).
- The author's node name appears once in git history (commit `c1ddf87`, an old version of
  `scan_test.go`). Removing it means rewriting history and force-pushing.

## Findings to keep

### Parity with the repeater CLI

Both sides have: neighbour discovery (`discover`), the hash size (`path-hash`), statistics and
`watch` with CSV and alert thresholds, advert, name, position, GPS on/off, the clock, the radio
(`radio set`; `set tx` through `cli`), tuning, the advert location policy (`set share-location`),
multi-acks, reboot (`cli reboot`), version and board, telemetry.

Not available on a companion:
- **`tempradio`**: the companion firmware has no temporary radio change. Bluetooth is unaffected
  by a bad radio setting, so `radio set` can always be undone.
- **`gps sync`, `gps setloc`, satellites and fix state**: no companion command. `telemetry` shows a
  position once there is a fix.
- **`get lat|lon|repeat|public.key`, `neighbors`, `gps`, `clock`**: the companion CLI answers
  `Unknown command`; the dedicated commands replace them.
- Repeater only by nature: `password`, `setperm`, `region`, `log`, `clear stats`, `powersaving`.
- Companion only by nature: messages, channels, contacts, `dm`, `healthcheck`.

Safe mode: `get <setting>` (one argument) is allowed on the companion too, so `get tz.offset` works
without `-safe=false`; `set` and `reboot` need it.

### GPS

`gps on|off`, `gps -interval`, `vars`, `set share-location`, `telemetry` and the position in `info`
are in. `gps -interval` works only on builds that list a `gps_interval` variable; the T1000-E on
v1.18.0 does not and answers `ILLEGAL_ARG`. `pos` in `info` is the position the node advertises
(a GPS fix, or what `set position` wrote); the frame does not say which. On the SenseCAP Solar
repeater, enabling GPS was enough to set its clock: with a fix (12 satellites) `clock` matched this
computer, and it kept the time across a reboot.

### Bluetooth names

`Scan` recognises a companion by the Nordic UART service in its advertisement or by the
`MeshCore-` prefix, and `-name` matches what follows the first `-` or the full name. Checked on
macOS, where the T1000-E's advertisement carries `6E400001-...`. A node that advertises the
service only in a scan response may need a longer scan on other platforms. A non-MeshCore device
with the same UART service is listed too (it is selected only if its name matches `-name`).

Prefixes reported for stock builds (from a list given to me; accuracy unchecked): `MeshCore-`,
`Whisper-`, `WisCore-`, `Seeed`, `Lilygo`, `HT-`, `LowMesh_MC_`, `NRF52`. They are not a fallback in
`Scan`: the UUID covers them, and `HT-` and `NRF52` are generic enough to match unrelated devices.
`Seeed`, `Lilygo` and `NRF52` may have no `-`, so the short `-name` form does not apply to them;
use the full advertised name, or add the list if a node turns up that needs it.

### USB serial

`Client` sits on a `transport` interface (`transport.go` for Bluetooth, `serial.go` for serial).
Framing, from the firmware's `ArduinoSerialInterface`: `<` from the app, `>` from the node, a
2-byte little-endian length, no checksum, at most 176 bytes, 115200 baud (a native USB port ignores
it). A node running both interfaces answers on both. Tried on a LilyGo T-Deck (fw v27): the
handshake, `status`, `watch`, live messages, `cli reboot` (the first two reconnect attempts time out
while it boots, about 6 s, and the third succeeds). The T1000-E build is Bluetooth only.

The repeater or room server console (`console.go`, `-console`) is a text console, not the companion
protocol: 115200 baud, a command is a line ending in CR (a bare LF does nothing), input is echoed,
the reply is `  -> <text>`, there is no prompt, and `stats-*`, `log`, `erase` and `get prv.key` work
on serial only. Tried on the SenseCAP Solar repeater (v1.18.0): `ver`, `board`, `get name`,
`neighbors`, `stats-*` (JSON, used for `status` and `watch`), `log` (a 79-line dump came back
complete; `log start`, `stop` and `erase` also work), `reboot` with reconnect, and the session.

### Buzzer, LED and vibration

From the firmware source (`main`, 2026-11-16; the variant `target.h` files and the last part of
`MyMesh.cpp` were not read, and it is not known which UI the T1000-E builds):

- **There is no remote control.** No companion command (`CMD_*`, codes 1-66 checked) and no CLI
  command, companion or serial, reads or writes the buzzer, LED or vibration. `SET_OTHER_PARAMS`
  has only manual-add, telemetry, advert location and multi-acks, and `SET_CUSTOM_VAR` handles
  only `gps` and `gps_interval`. So `vars` showing only `gps` on the T1000-E is expected.
  `meshcore_py` has the same command numbers and nothing about alerts (`meshcore.js` could not be
  fetched).
- **One persisted flag**, `NodePrefs.buzzer_quiet` (default 0, buzzer on), applied at boot. A second,
  `vibe_quiet`, exists only in the older `ui-orig` UI on boards with a haptic driver. Muting also
  silences the startup and shutdown tones.
- **It is toggled on the device**: a triple click of the user button flips the buzzer (a beep plays
  when it turns on); on `ui-orig` boards with a haptic driver it cycles buzz+vibe, buzz only, vibe
  only, silent. A double click sends an advert.
- **No per-event settings.** The buzzer sounds for direct and channel messages with different
  melodies, plus startup, shutdown and acks; a new contact or room message makes no sound.
- **Only while no app is connected.** In the new UI the message sounds and vibration play only when
  there is no connection: with a phone or `mc` connected over Bluetooth the device stays quiet and
  the app decides what to notify. So the app's notification settings can only be phone-side;
  nothing in the firmware or docs lets the app send them to the device. The app's source was not
  available to check.
- **LED**: a status LED blinks faster when messages are waiting (`ui-new`); an RGB one with colours
  for battery, unread messages and connection on `ui-orig`. Not controllable. The only
  notification-like pushes are ones the device always sends: `0x83` (message waiting) and `0x90`
  (contacts full).

A way forward would be to propose a custom variable (for example `buzzer` and `vibe`, 0/1) through
`CMD_SET_CUSTOM_VAR` to MeshCore; `vars` and `cli` would then work with no change here. Re-check
with `mc vars` after firmware updates.

### Why the Bay Area healthcheck did not hear the nodes

Measured with the repeater indoors, then outside (not very high):
- Companion to repeater was fine: +13 dB, relayed one second later (repeater log).
- Indoors, the repeater heard the rest of the mesh only at the edge: 15 of 16 packets at -6 to -8 dB SNR
  (SF7 stops decoding near -7.5 dB), noise floor -112 dBm against -119 dBm at the companion.
- Outside: noise floor -119/-120 dBm, about 17 packets/min received against about 5 indoors, and 55%
  of them duplicates (the same flood heard from several repeaters; indoors it was about none).
  The counts are rough: different times of day, and the indoor figure is an average over two hours.
- Everything heard was a plain (unscoped) flood, so a scope requirement does not look like the cause.
  The Bay Area healthcheck site refuses automated fetches (403), so what its observers require is
  unconfirmed. The next check is a fresh healthcheck code from that page sent with `mc healthcheck`.
- Region scope, from the MeshCore blog (Jan and Apr 2026): repeaters since v1.10.0 do not forward a
  scoped flood for a region they do not have; `region denyf *` makes a repeater refuse unscoped floods
  ("not advised at this stage"); the default scope is a v1.15.0 feature. Region names are lowercase
  letters, digits and `-`, matched exactly (the list is flat, there is no inheritance).

### Protocol differences seen

`meshcore_py` sends the TX power as a 4-byte integer where the firmware reads one signed byte, and
`SET_TUNING_PARAMS` there has two extra zero bytes (`mc` sends 9 bytes and the T1000-E accepts it).
`mc` does not use the TX power command: `cli set tx` does it.

## Done

- Bluetooth scan (by service UUID or prefix, with a timeout), pairing retry, reconnect with backoff
- Handshake, node and device info, channels, contacts with an incremental on-disk cache, messages
- `scope`: the default flood scope (a region name and its key, SHA-256 of `#`+name)
- Direct messages with acknowledgement, retry and path reset; `send` (refuses over 133 bytes);
  `healthcheck`; the contact
  list with type, name and age filters, sorting and JSON
- Node settings: `set name|position|share-location|multi-acks`, `radio`, `tuning`, `clock`, `gps`,
  `advert`, `auto-add`, `path-hash`; `discover`, `contact remove`, `channel add|remove`
- `status`, `watch`, `telemetry` with CSV, JSON and alert thresholds, for a companion, a repeater
  (over the radio, or on its console) and a repeater's own sensors
- Repeater login, CLI commands (safe mode on by default), `cli` on the node itself with reboot and
  reconnect
- USB serial for companions, and the text console for repeaters and room servers
- The push dispatcher (`Subscribe`), the session and command registry, the subcommand command
  line, and the `repl` command with `liner`, live messages, Ctrl-C, repeater mode and `safe`
- The library (`meshgore`) and the command (`cmd/mc`)
