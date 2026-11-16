# TODO

The command line is now `mc [options] <command>` (see the README). Entries below that were
written earlier name the old options (`-watch`, `-set-radio`, `-serial-cli`, ...); the
commands that replaced them are `watch`, `radio set`, `-console`, and so on.

What is left, in the order I would do it. Protocol notes say where a layout
comes from: the companion protocol doc, or `meshcore_py` (the official Python
client), which is the reference wherever the doc is silent. Untested items need
a run against a real node before they count as done.

## GPS (mostly done)

`-gps=on|off`, `-gps-interval`, `-vars`, `-share-location`, `-telemetry` (position when there
is a fix) and the position line in the node info are all in. What is left:

- `-gps-interval` works only on builds that list a `gps_interval` variable; the T1000-E on
  v1.18.0 does not, and answers `ILLEGAL_ARG`.
- No separate `-position` flag. `pos` in the node info is the position the node advertises
  (a GPS fix, or what `-set-position` wrote); the frame does not say which.

## Parity: repeater `-cmd` vs companion flags

Done on both: neighbour discovery (`discover.neighbors` / `-discover`), `path.hash.mode`
(`-path-hash-mode`), status and `-watch` (`-status`, `-watch`, `-csv`, thresholds), advert,
name, position, GPS on/off, clock (`clock` / `-clock`, `-sync-clock`), radio
(`set radio` / `-set-radio`; `set tx` through `-cmd`), tuning (`rxdelay`, `af` /
`-rx-delay`, `-airtime-factor`), advert location policy (`gps advert` /
`-share-location`), `multi.acks` (`-multi-acks`), reboot (`-cmd=reboot`), version and
board (printed on connect). `-telemetry` shows battery, temperature and the GPS
position the node reports.

Not done, and why:
- **`tempradio`**: the companion firmware has no temporary radio change. Bluetooth
  is unaffected by a bad radio setting, so `-set-radio` can always be undone.
- **Factory reset** (`CMD_FACTORY_RESET` `0x33`, payload the text `reset`): left out
  on purpose, since it formats the node.
- **`gps sync`, `gps setloc`, GPS satellites and fix state**: no companion command.
  `-telemetry` shows a position once there is a fix.
- **`get lat|lon|repeat|public.key`, `neighbors`, `gps`**: the companion CLI
  answers `Unknown command`; the dedicated flags above replace them.
- Repeater only by nature: `password`, `setperm`, `region`, `log`, `clear stats`,
  `powersaving`.
- Companion only by nature: messages, channels, contacts, `-dm`, `-healthcheck`,
  `-listen`.

Safe mode: `get <setting>` (one argument) is allowed on the companion too, so
`get tz.offset` works without `-safe=false`; `set` and `reboot` need it.

## Connection: other device names and other transports

1. **Other BLE name prefixes: done.** `Scan` now recognises a companion by the service
   UUID in its advertisement or by the `MeshCore-` prefix, and `-name` matches the part
   after the first `-` or the full name. Checked on macOS: the T1000-E's advertisement
   carries `6E400001-...`. Still open: a node that advertises the service only in a
   scan response may need a longer scan on other platforms, and Linux/Windows are
   untested. A non-MeshCore device with the same UART service is listed too (it is only
   selected if its name matches `-name`).
   Prefixes reported for stock builds (from a list given to me; accuracy unchecked):
   `MeshCore-`, `Whisper-`, `WisCore-`, `Seeed`, `Lilygo`, `HT-`, `LowMesh_MC_`, `NRF52`.
   The same source says to filter on the Nordic UART service UUID so forks with
   non-standard names are still found, which is what `Scan` does. I did not add these
   prefixes as a fallback: the UUID covers them, and `HT-` and `NRF52` are generic
   enough to match unrelated devices. Note that `Seeed`, `Lilygo` and `NRF52` may have
   no `-` in the name, so the short `-name` form does not apply to them; use the full
   advertised name (or add the prefix list if a node turns up that needs it).
2. **USB serial: works** (tried on a LilyGo T-Deck, fw v27; the T1000-E build is Bluetooth
   only). `Client` now sits on a `transport`
   interface (`transport.go`: Bluetooth; `serial.go`: serial), `-serial=<port|list>` and
   `-baud` select it, and `Link.Reconnect` reopens the port, so `-cmd=reboot` and
   `-watch` work the same. Framing, from the firmware's `ArduinoSerialInterface`: `<`
   from the app, `>` from the node, a 2-byte little-endian length, no checksum, at most
   176 bytes, 115200 baud. Open:
   - `-cmd=reboot -cmd ver` over serial works on the T-Deck: the first two reconnect
     attempts time out while it boots (about 6 s), the third succeeds. `-watch=20s` over
     serial works too.
   - Whether opening the port resets other boards (DTR/RTS), and whether
     `go.bug.st/serial` needs anything on Linux and Windows.
   - A node running both interfaces answers on both.
   **Repeater console over serial: works** (`console.go`, `-serial-cli`; tried on a
   SenseCAP Solar repeater, v1.18.0: `ver`, `board`, `get name`, `neighbors`, `stats-*`, `log`, `reboot` with reconnect). From the firmware source: 115200 baud, a command is a line ending
   in CR (a bare LF does nothing), input is echoed, the reply is `  -> <text>`, there is
   no prompt, and `stats-*`, `log`, `erase` and `get prv.key` work on serial only. Still to check:
   a much longer `log` dump (a 79-line one came back complete);
   a room server (it
   also echoes LF),.
   `-status`, `-watch`, `-csv` and the alert thresholds work over the console (they use
   the three JSON `stats-*` commands).
3. **Wi-Fi (TCP) companion**, lowest priority. Builds with `ENABLE_WIFI_INTERFACE` serve
   the same `<`/`>` framed protocol on a TCP port (default 5000, per the firmware source).
   With the transport interface in place this is a small addition: `-tcp=host:port`
   with a transport over `net.Conn`, reusing `frameReader` and `frameForNode`.

## Buzzer, LED and vibration (notifications)

What the firmware source says (checked on `main`, 2026-11-16; the variant `target.h` files
and the last part of `MyMesh.cpp` were not read, and it is not known which UI the T1000-E
builds):

- **There is no remote control.** No companion command (`CMD_*`, codes 1-66 checked) and no
  CLI command (companion or serial) reads or writes the buzzer, LED or vibration.
  `CMD_SET_OTHER_PARAMS` has only manual-add, telemetry, advert location and multi-acks.
  `CMD_SET_CUSTOM_VAR` handles only `gps` and `gps_interval`. So `-vars` showing only `gps`
  on the T1000-E is expected, and `-cmd` cannot do it either.
- **One persisted flag.** `NodePrefs.buzzer_quiet` (default 0, buzzer on), saved with the
  prefs, applied at boot. A second flag, `vibe_quiet`, is only used by the older `ui-orig`
  UI on boards with a haptic driver (`HAS_DRV2605`). Muting also silences the startup and
  shutdown tones.
- **It is toggled on the device**: a triple click of the user button flips the buzzer
  (an acknowledge beep plays when it turns on); on `ui-orig` boards with a haptic driver it
  cycles buzz+vibe, buzz only, vibe only, silent. A double click sends an advert.
- **No per-event settings** (new message, DM, channel). The buzzer sounds for direct and
  channel messages, with different melodies, plus startup, shutdown and acks; a new contact
  or a room message makes no sound. Vibration triggers on every notification except none.
- **Only while no app is connected.** In the new UI the message sounds and vibration play
  only when `!hasConnection()`: with a phone (or `mc`) connected over Bluetooth the device
  stays quiet, and the app decides what to notify. That is why the app's notification
  settings (new messages, DMs, channels) can only be phone-side; nothing in the firmware or
  docs lets the app send them to the device. The app's source was not available to check.
- **Checked against the client libraries too** (`meshcore_py`; `meshcore.js` could not be
  fetched). The firmware's full command list runs to 66 (`CMD_RUN_CLI_COMMAND`), the push
  codes to `0x90`, and `meshcore_py` has the same numbers. None of it is about buzzer, sound,
  alerts, mute, vibration or LED. The only notification-like items are pushes the device
  always sends: `0x83` (message waiting) and `0x90` (contacts full).
- **LED**: a status LED blinks faster when messages are waiting (`ui-new`); an RGB one with
  colours for battery, unread messages and connection on `ui-orig`. Not controllable.

Possible work, in order of effort:

1. Document the button gesture in the README (triple click toggles the buzzer on the
   T1000-E, still to be confirmed on the device).
2. Show the state if it can be known: nothing exposes it today, so this needs a firmware
   change.
3. Propose to MeshCore a custom variable (for example `buzzer` and `vibe`, 0/1, through
   `CMD_SET_CUSTOM_VAR`) or a CLI command; `-vars` and `-cmd` would then work with no
   change here. Once a firmware has it, `-vars` lists it.
4. Re-check after firmware updates: `mc -vars` and `mc -cmd "get <name>"` on a new build.

## Companion commands not used yet

From the firmware's command list (and `meshcore_py`, which uses the same numbers). Layouts
are not checked; confirm against the firmware source before building. Auto-add config (58/59),
path hash mode (61) and control data for discovery (55) are done:

- `0x3F`/`0x40` (63/64) **default flood scope** set/get, and `0x36` (54) flood scope key:
  the repeater's `region default` / `region home`.
- `0x3C` (60) **allowed repeat frequencies** (read): which radio frequencies may use the
  "repeat" option of `SET_RADIO_PARAMS`.
- `0x3E` (62) send non-text **data on a channel**, `0x41` (65) send a **raw packet**,
  `0x39` (57) **anonymous request**, `0x32` (50) **binary request**, `0x34` (52) path
  discovery.
- `0x33` (51) factory reset, `0x13` (19) reboot (needs the text `reboot`); `-cmd=reboot`
  already covers the second.
- Differences seen: `meshcore_py` sends the TX power as a 4-byte integer where the firmware
  reads one signed byte; `SET_TUNING_PARAMS` has two extra zero bytes there (`mc` sends 9
  bytes and the T1000-E accepts it). Not used by `mc`: TX power goes through `-cmd "set tx"`.

## Still to test on hardware

- `-discover` and `-remove-channel`, `-auto-add*`, `-path-hash-mode`: tried on the T1000-E.
  `-discover` found the repeater with both SNR values; only repeaters answer in our tests
  (a room server or sensor in range would show whether the type filter works for them).
- Repeater clock from GPS: on the SenseCAP Solar repeater (v1.18.0), enabling GPS
  (`gps on`) was enough: with a fix (12 satellites) `clock` matched this computer. The
  clock had read May 2024 before. Not checked: whether it is set again after a reboot,
  and how long that takes. A companion has no `gps sync`; whether the T1000-E sets its
  own clock from GPS is untested (`-sync-clock` sets it from this computer).
- The T1000-E may have an accelerometer, but this firmware's `-telemetry` does not
  list one.
- `-set-name`: afterwards the `Node "..."` line should show the new name.
- `-set-position`: afterwards `pos` should show the new position.
- `-watch` and a long `-listen`: power-cycle the companion while running.
- `-add-channel` works (checked with `#healthcheck`); the SHA-256 rule for the
  hashtag secret was checked indirectly, since `-healthcheck` works on the new node.

## Still open from set-up

- Nothing left from set-up.

## Ideas we discussed

- **Interactive shell** for repeaters: `-repeater=...` with no `-cmd` gives a
  prompt, so several commands share one login. Safe mode still applies.
- **Message length check.** The doc says channel messages are limited to 133
  characters. Check it in `SendChannelMessage` and say so. The limit for direct
  messages is unconfirmed, so do not enforce one there.
- **Healthcheck delivery without an echo.** `-healthcheck-wait` only works if
  something repeats the code. Radio log pushes (`0x88`) might show repeaters
  rebroadcasting our packet. The doc gives no layout; work it out from real
  frames before building on it.
- **Show the contact list in a useful way:** sort, filter by type
  (`-contacts=repeater`), hide stale ones.

## Decided against

- Setting the companion's clock at every connection. `-sync-clock` does it on request; revisit
  only if a node's wrong clock turns out to matter (it stamps outgoing messages).

## Open questions and checks

- **Status duplicate counters read 0** on a busy repeater. Either the firmware
  does not count them or the offsets are shifted. Compare with the MeshCore app.
- **Channel send is answered with `OK`**, not the documented `MSG_SENT`. Both are
  accepted, but it means channel sends have no ack tag to wait for.
- **Linux and Windows** are untested. The pairing behaviour in `link.go` is written
  around macOS; BlueZ pairs differently (it may need `bluetoothctl` first).
- **`-watch` and long `-listen` reconnect** has only been exercised by reading the
  code. Test it by power-cycling the companion while it runs.
- **`write()` retries only on the macOS error text** (`Insufficient Encryption`,
  `timeout on Write`). Other platforms may phrase it differently.
- **Library API is unstable.** `Link`, `Client` and the message types may still
  change. Decide on a real module path before anyone imports it.

## REPL (first version done)

Done: the push dispatcher (`push.go`); the subcommand command line on a `session` and a command
registry; the `repl` command (`cmd/mc/repl.go`) with `github.com/peterh/liner` for editing,
history and completion; live messages above the prompt; Ctrl-C stopping a running command;
`repeater NAME` as a session mode, also from the command line (`repl -repeater NAME`); `safe on|off`; piped input as a script. Tried through a
pseudo-terminal on the T1000-E: Ctrl-C in `watch`, a message arriving while idle, tab
completion, history, a repeater session (command, safe-mode refusal, exit), Ctrl-D.

Open:
- Whether a repeater console session treats unknown words as `cli` (so you would not type `cli`
  before everything). Undecided; review later.
- `github.com/gobs/cmd` instead of the small loop in `repl.go`, if scripting (`source`, variables)
  turns out to be wanted. Its commands are `Call func(line string) bool`, and `runLine` with
  `splitArgs` is what each would call.
- A script file option (`mc -f file`); piped input already works.
- The line being typed is hidden (not lost) when a message prints above the prompt. liner cannot
  redraw it; `chzyer/readline` can, at the cost of a different library.
- Ctrl-C does not interrupt a console `cli` command that is waiting for its reply, or a
  repeater command waiting for its reply inside a repeater session.
- Completion knows contact names only from the cache; channel names are not offered for `send`.
- Messages are held back while a command runs; a long `watch` shows none until it stops.
- Not tried: a long idle session across a Bluetooth drop. The session on a repeater console
  works (status, `cli`, `safe off`, `cli reboot` and the commands after it).

## Housekeeping

- A `CHANGELOG` or release notes once the library has users.
- Rename the module from `meshgore` to its final path.
- `README.md`: add a "set up a new node" walkthrough once items 1-3 exist
  (flash, pair, set name and position, add channels, advert).

## Done

- Bluetooth scan, pairing retry, reconnect with backoff
- Handshake, device and node info, channels, contacts, message sync
- `-auto-add*`, `-path-hash-mode`, `-discover`, `-remove-channel`, `-remove-contact`
- Channel messages and `-healthcheck`, with optional wait for an echo
- Incremental contact sync with an on-disk cache
- Direct messages with acknowledgement, retry and path reset; node-reported
  round-trip time
- `-advert` (flood or zero-hop)
- `-add-channel` (works), `-set-name` and `-set-position` (written, untested)
- `-vars` (works): shows the companion's custom variables, such as `gps`
- Companion statistics: `-status`, `-watch`, `-csv`, alert thresholds, `-json`
- Companion settings: `-gps`, `-set-radio`, `-clock`/`-sync-clock`, `-rx-delay`,
  `-airtime-factor`, `-share-location`, `-multi-acks`, `-telemetry`
- `-cmd` on the companion itself (`CMD_RUN_CLI_COMMAND`), `-cmd=reboot` with reconnect
  (`-reconnect-wait`), repeater `-telemetry`
- Finding companions by service UUID as well as the `MeshCore-` prefix; `Scan` times out
- USB serial (`-serial`, `-baud`): companions, and repeaters and room servers with
  `-serial-cli` (`-cmd`, `-status`, `-watch`, `reboot`)
- Repeater GPS through the CLI (`gps on`, `gps`, `gps setloc`, `get lat`): works; the
  safe-mode allowlist now covers the read forms of `gps` and `sensor`
- Repeater login, CLI commands (safe mode on by default), status, JSON output,
  thresholds and exit codes, `-watch` with CSV logging
- Split into the `meshgore` library and `cmd/mc`
