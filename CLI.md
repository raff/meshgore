# Repeater CLI commands

Commands you can send to a MeshCore repeater (also room servers and sensors)
with `-cmd`. Source: the firmware's `docs/cli_commands.md`
(<https://github.com/meshcore-dev/MeshCore/blob/main/docs/cli_commands.md>).
What a node actually supports depends on its firmware version and build, so
treat this as the upper bound and check with `ver` first.

**Safe mode.** By default `mc` runs only the commands in the first table and
refuses everything else before it sends anything. Pass `-safe=false` to run the
others. The check is an allowlist (`IsReadOnlyCommand` in `repeater.go`): a command
it does not recognise counts as unsafe.

## Read-only (allowed in safe mode)

| Command | Reply |
| --- | --- |
| `ver` | firmware version |
| `board` | hardware name |
| `clock` | current time, UTC |
| `neighbors` | the 8 most recent adverts, one per line: `{pubkey-prefix}:{timestamp}:{snr*4}` |
| `get <setting>` | the setting's value; see the list below |
| `region` | list of regions |
| `region get <name>` | one region (`*` is the wildcard region) |
| `region list allowed` / `region list denied` | regions by flood permission |
| `region home` / `region default` | home or default-scope region (without a name) |
| `gps` | GPS state: `off`, or `on, active, fix, 7 sats` (only on builds with GPS) |
| `gps advert` | whether adverts include the location: `none`, `share` or `prefs` |
| `sensor list [start]` | the node's sensor settings as `name=value` lines, the repeater-side equivalent of `-vars` (only on builds with sensors or GPS) |
| `sensor get <key>` | one sensor setting |

`get <setting>` works for: `radio`, `tx`, `freq`, `radio.rxgain`,
`radio.fem.rxgain`, `radio.fem.txgain`, `name`, `lat`, `lon`, `owner.info`,
`adc.multiplier`, `public.key`, `role`, `repeat`, `path.hash.mode`,
`loop.detect`, `txdelay`, `direct.txdelay`, `rxdelay`, `dutycycle`, `af`,
`int.thresh`, `cad`, `agc.reset.interval`, `multi.acks`,
`flood.advert.interval`, `advert.interval`, `flood.max`, `flood.max.unscoped`,
`flood.max.advert`, `acl`, `allow.read.only`, `guest.password`, and the GPS,
sensor and bridge settings where compiled in. `get prv.key` is blocked in safe
mode because it prints the private key.

`get guest.password` prints a password. Be careful where you log it.

## Change state or transmit (need `-safe=false`)

| Command | Effect |
| --- | --- |
| `advert` | send a flood advert |
| `advert.zerohop` | send a zero-hop advert |
| `discover.neighbors` | scan for zero-hop neighbors |
| `clock sync` | set the repeater's clock from the companion's |
| `time <epoch_seconds>` | set the clock |
| `set <setting> <value>` | change any setting above that has a `get` |
| `set radio <freq>,<bw>,<sf>,<cr>` | radio parameters: MHz, kHz, SF 5-12, CR 5-8 |
| `set tx <dbm>` | transmit power, 1-22 dBm |
| `tempradio <freq>,<bw>,<sf>,<cr>,<minutes>` | change radio parameters, then revert after the timeout |
| `password <new_password>` | change the admin password |
| `set guest.password <password>` | change the guest password |
| `set owner.info <text>` | owner information |
| `setperm <pubkey> <permissions>` | add, update or remove a companion's permissions |
| `gps on` / `gps off` | switch the GPS hardware on or off |
| `gps sync` | set the clock from GPS time |
| `gps setloc` | set the node's location from the GPS fix |
| `gps advert <none\|share\|prefs>` | choose what location adverts carry |
| `sensor set <key> <value>` | change a sensor setting |
| `neighbor.remove <pubkey_prefix>` | remove neighbors matching the prefix; a single space removes all |
| `clear stats` | reset statistics |
| `log start` / `log stop` / `log erase` | capture the rx log to node storage |
| `powersaving on` / `powersaving off` | power saving (repeater only) |
| `region load`, `region save`, `region allowf <name>`, `region denyf <name>`, `region put <name> [parent]`, `region def ...`, `region remove <name>`, `region home <name>`, `region default <name>` | edit regions |

## Disruptive (need `-safe=false`, think first)

| Command | Effect |
| --- | --- |
| `reboot` | reboot; no reply is sent |
| `poweroff` / `shutdown` | power off; no reply is sent, and you need physical access to turn it back on |
| `clkreboot` | reset the clock and reboot; no reply is sent |
| `start ota` | start an over-the-air firmware update |

For commands that send no reply, `mc` waits for `-cmd-timeout` and then exits
with code 1. That is expected.

A bad `set radio`, `set freq` or `set repeat off` can cut the repeater off from
you, because the radio is the only way in. Use `tempradio` to try radio
changes: it reverts on its own.

## Serial only (do not work through a companion)

`stats-core`, `stats-radio`, `stats-packets`, `log` (print the captured log),
`erase` (factory reset, destructive) and `get prv.key`. For statistics over
the radio use `-status`, which uses a binary request instead.

## Version-dependent

`get radio.rxgain` needs v1.14.1 or later. GPS, sensor, bridge and Ethernet
commands exist only in builds that include them.
