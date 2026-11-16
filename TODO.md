# TODO

What is left, in the order I would do it. Protocol notes say where a layout
comes from: the companion protocol doc, or `meshcore_py` (the official Python
client), which is the reference wherever the doc is silent. Untested items need
a run against a real node before they count as done.

## Next: GPS

For nodes with a GPS (the T-Deck, for instance). Checked against the companion
firmware source (`examples/companion_radio/MyMesh.cpp`); `meshcore_py` has nothing
GPS-specific beyond the generic custom variables.

- **Enable it** with custom variables: `CMD_SET_CUSTOM_VAR` = `0x29`, payload
  `key:value`. The variables are `gps` (`1` or `0`) and `gps_interval` (seconds,
  0..86400, 0 meaning no automatic updates). They exist only on builds with GPS
  compiled in (`ENV_INCLUDE_GPS`); anything else gets `ILLEGAL_ARG`. The factory
  default is GPS off.
- **List what a node supports** with `CMD_GET_CUSTOM_VARS` = `0x28`. The reply is
  code `0x15` (`RESP_CODE_CUSTOM_VARS`) followed by text `name:value,name:value,...`.
  The names come from the node's sensor manager, so this is the way to find out
  which variables a given board has.
- **Read the position** from `SELF_INFO`: the latitude and longitude we already parse
  and print as `pos`. The firmware fills them from `sensors.node_lat/lon`, the same
  values `SET_ADVERT_LATLON` writes and the GPS updates. I have not read the GPS
  driver, so confirm by enabling GPS and watching `pos` change.
- **Share it in adverts** with the advertisement location policy (the byte after the
  multi-acks byte in `SELF_INFO`): `0` is none, `1` is share. It is set with
  `CMD_SET_OTHER_PARAMS` = `0x26`, whose payload is `manual_add_contacts`, the
  telemetry bitfield, the policy and `multi_acks`. That command overwrites all four,
  so read the current ones from `SELF_INFO` first and send them back unchanged.
- **Self telemetry** (`CMD_SEND_TELEMETRY_REQ` with no destination) returns sensor
  readings in Cayenne LPP format, which includes GPS if the node reports it. Larger
  to parse; only worth doing if you want battery, temperature and so on too.

Proposed commands:

- `-gps=on|off` and `-gps-interval=<seconds>`
- `-vars` to list the custom variables
- `-position` to print the current position (and whether it is a GPS fix or a
  manually set one, if the frame says)
- `-share-location=on|off` for the advert policy

## Parity: repeater `-cmd` vs companion flags

Done on both: status and `-watch` (`-status`, `-watch`, `-csv`, thresholds), advert,
name, position, GPS on/off, clock (`clock` / `-clock`, `-sync-clock`), radio
(`set radio` / `-set-radio`; `set tx` through `-cmd`), tuning (`rxdelay`, `af` /
`-rx-delay`, `-airtime-factor`), advert location policy (`gps advert` /
`-share-location`), `multi.acks` (`-multi-acks`), reboot (`-cmd=reboot`), version and
board (printed on connect). `-telemetry` shows battery, temperature and the GPS
position the node reports.

Not done, and why:
- **`tempradio`**: the companion firmware has no temporary radio change. Bluetooth
  is unaffected by a bad radio setting, so `-set-radio` can always be undone.
- **`discover.neighbors`**: the companion would use `CMD_SEND_CONTROL_DATA` (`0x37`),
  but its payload is defined outside `MyMesh.cpp`; find it in the firmware's
  control-data code before building on it. `-contacts` already lists heard adverts.
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

## Still to test on hardware

- `-set-name`: afterwards the `Node "..."` line should show the new name.
- `-set-position`: afterwards `pos` should show the new position.
- `-watch` and a long `-listen`: power-cycle the companion while running.
- `-add-channel` works (checked with `#healthcheck`); the SHA-256 rule for the
  hashtag secret was checked indirectly, since `-healthcheck` works on the new node.

## Still open from set-up
- Not yet possible: **removing a channel**. The firmware refuses an all-zero secret
  through this command, so it is not clear how to clear a slot. Find out whether
  `SET_CHANNEL` with an empty name does it.
- The companion can also run CLI commands on itself (`CMD_RUN_CLI_COMMAND` = `0x42`,
  v14+, but not on every firmware). Not used yet.

## Ideas we discussed

- **Interactive shell** for repeaters: `-repeater=...` with no `-cmd` gives a
  prompt, so several commands share one login. Safe mode still applies.
- **`SET_DEVICE_TIME`** on the companion at startup, since it sets message
  timestamps. The doc lists it but gives no layout; take it from `meshcore_py`.
  Probably `0x06` plus a uint32 timestamp.
- **Message length check.** The doc says channel messages are limited to 133
  characters. Check it in `SendChannelMessage` and say so. The limit for direct
  messages is unconfirmed, so do not enforce one there.
- **Healthcheck delivery without an echo.** `-healthcheck-wait` only works if
  something repeats the code. Radio log pushes (`0x88`) might show repeaters
  rebroadcasting our packet. The doc gives no layout; work it out from real
  frames before building on it.
- **Show the contact list in a useful way:** sort, filter by type
  (`-contacts=repeater`), hide stale ones.
- **Remove a contact** from the node (`REMOVE_CONTACT` = `0x0F`; the firmware source confirms the number), and prune it from the
  cache, which an incremental sync cannot do. Today only `-full-sync` prunes.

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

## Housekeeping

- A `CHANGELOG` or release notes once the library has users.
- Rename the module from `meshgore` to its final path.
- `README.md`: add a "set up a new node" walkthrough once items 1-3 exist
  (flash, pair, set name and position, add channels, advert).

## Done

- Bluetooth scan, pairing retry, reconnect with backoff
- Handshake, device and node info, channels, contacts, message sync
- Channel messages and `-healthcheck`, with optional wait for an echo
- Incremental contact sync with an on-disk cache
- Direct messages with acknowledgement, retry and path reset; node-reported
  round-trip time
- `-advert` (flood or zero-hop)
- `-add-channel` (works), `-set-name` and `-set-position` (written, untested)
- `-vars` (works): shows the companion's custom variables, such as `gps`
- Repeater GPS through the CLI (`gps on`, `gps`, `gps setloc`, `get lat`): works; the
  safe-mode allowlist now covers the read forms of `gps` and `sensor`
- Repeater login, CLI commands (safe mode on by default), status, JSON output,
  thresholds and exit codes, `-watch` with CSV logging
- Split into the `meshgore` library and `cmd/mc`
