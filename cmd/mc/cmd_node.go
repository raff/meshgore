package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/raff/meshgore"
)

// newActions returns node actions with nothing asked for. The zero values of some
// fields mean "set it", so they must start at -1.
func newActions(s *session) nodeActions {
	return nodeActions{interval: -1, slot: -1, node: s.node(), discoverWait: 8 * time.Second}
}

// withCompanion runs an action on the companion node.
func withCompanion(s *session, a nodeActions) int {
	l := s.companion()
	if l == nil {
		return exitFail
	}
	a.node = s.node()
	return runNodeActions(l, a)
}

func thresholdFlags(fs *flag.FlagSet, t *thresholds) {
	fs.Float64Var(&t.minBattery, "min-battery", 0, "exit 2 if the battery is below this many volts (0 = off)")
	fs.Float64Var(&t.maxNoise, "max-noise", 0, "exit 2 if the noise floor in dBm is above this, e.g. -100 (0 = off)")
}

func init() {
	register(
		&command{"info", "Node", "", "show the node's name, key, position, radio and firmware", cmdInfo},
		&command{"vars", "Node", "", "list the custom variables, which show what the board supports (GPS, sensors)", cmdVars},
		&command{"status", "Node", "[-min-battery V] [-max-noise DBM]", "battery, uptime, radio and packet statistics", cmdStatus},
		&command{"watch", "Node", "[-csv FILE] [-min-battery V] [-max-noise DBM] INTERVAL", "read the statistics every INTERVAL (e.g. 5m) until Ctrl-C", cmdWatch},
		&command{"telemetry", "Node", "", "the node's own sensors: battery, temperature, GPS position", cmdTelemetry},
		&command{"clock", "Node", "[sync]", "show the node's clock, or set it from this computer", cmdClock},
		&command{"radio", "Node", "[set FREQ,BW,SF,CR]", "show the radio settings, or change them (MHz, kHz, SF, CR)", cmdRadio},
		&command{"tuning", "Node", "[-rx-delay V] [-airtime-factor V]", "show or change the receive delay and airtime factor", cmdTuning},
		&command{"gps", "Node", "[on|off] [-interval SECONDS]", "show the GPS state, switch it, or set the update interval", cmdGPS},
		&command{"advert", "Node", "[flood|zerohop]", "make the node advertise itself", cmdAdvert},
		&command{"set", "Node", "name|position|share-location|multi-acks VALUE", "change the name, advertised position, location sharing or multi-acks", cmdSet},
		&command{"scope", "Node", "[set REGION | clear]", "show, set or clear the default flood scope (the region every flood is tagged with)", cmdScope},
		&command{"auto-add", "Node", "[-overwrite on|off] [-max-hops N] [show|all|none|TYPES]", "which adverts become contacts (types: chat,repeater,room,sensor)", cmdAutoAdd},
		&command{"path-hash", "Node", "[0|1|2]", "show or set the hash size in flood packets (mode+1 bytes)", cmdPathHash},
	)
}

func cmdInfo(s *session, args []string) int {
	fs := newFlags("info", "")
	if _, code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if s.g.console {
		if c := s.console(); c == nil {
			return exitFail
		}
		fmt.Printf("Repeater console on %s at %d baud\n", s.g.serial, s.g.baud)
		return exitOK
	}
	first := s.link == nil
	l := s.companion()
	if l == nil {
		return exitFail
	}
	if !first {
		printNodeInfo(l) // the first connection printed it already
	}
	return exitOK
}

func cmdVars(s *session, args []string) int {
	fs := newFlags("vars", "")
	if _, code, ok := parseFlags(fs, args); !ok {
		return code
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	return runVars(l)
}

func cmdStatus(s *session, args []string) int {
	fs := newFlags("status", "[-min-battery V] [-max-noise DBM]")
	var t thresholds
	thresholdFlags(fs, &t)
	if _, code, ok := parseFlags(fs, args); !ok {
		return code
	}
	src := s.stats()
	if src == nil {
		return exitFail
	}
	return printStats(src, t.limits(), s.g.json, s.out)
}

func cmdWatch(s *session, args []string) int {
	fs := newFlags("watch", "[-csv FILE] [-min-battery V] [-max-noise DBM] INTERVAL")
	var t thresholds
	thresholdFlags(fs, &t)
	csvPath := fs.String("csv", "", "append each reading to this CSV file")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		return usageError(fs, "wants one interval, for example 5m")
	}
	every, err := time.ParseDuration(rest[0])
	if err != nil || every <= 0 {
		return usageError(fs, "%q is not a duration like 30s or 5m", rest[0])
	}
	src := s.stats()
	if src == nil {
		return exitFail
	}
	return watchCompanion(s.ctx, src, every, *csvPath, t.limits(), s.g.json, s.out)
}

func cmdTelemetry(s *session, args []string) int {
	fs := newFlags("telemetry", "")
	if _, code, ok := parseFlags(fs, args); !ok {
		return code
	}
	a := newActions(s)
	a.showTele = true
	return withCompanion(s, a)
}

func cmdClock(s *session, args []string) int {
	fs := newFlags("clock", "[sync]")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	a := newActions(s)
	switch {
	case len(rest) == 0:
	case len(rest) == 1 && rest[0] == "sync":
		a.syncClock = true
	default:
		return usageError(fs, "unknown argument %q", strings.Join(rest, " "))
	}
	a.showClock = true
	return withCompanion(s, a)
}

func cmdRadio(s *session, args []string) int {
	fs := newFlags("radio", "[set FREQ,BW,SF,CR]")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) == 0 {
		l := s.companion()
		if l == nil {
			return exitFail
		}
		self := l.Self
		fmt.Printf("radio %.3f MHz BW %.1f kHz SF%d CR%d\n", self.FreqMHz, self.BandwidthKHz, self.SF, self.CR)
		return exitOK
	}
	if len(rest) != 2 || rest[0] != "set" {
		return usageError(fs, "wants nothing, or: set FREQ,BW,SF,CR")
	}
	a := newActions(s)
	a.radio = rest[1]
	return withCompanion(s, a)
}

func cmdTuning(s *session, args []string) int {
	fs := newFlags("tuning", "[-rx-delay V] [-airtime-factor V]")
	rx := fs.String("rx-delay", "", "receive delay base, 0..20")
	af := fs.String("airtime-factor", "", "airtime factor, 0..9")
	if _, code, ok := parseFlags(fs, args); !ok {
		return code
	}
	a := newActions(s)
	a.rxDelay, a.airtime = *rx, *af
	a.showTuning = true
	return withCompanion(s, a)
}

func cmdGPS(s *session, args []string) int {
	fs := newFlags("gps", "[on|off] [-interval SECONDS]")
	interval := fs.Int("interval", -1, "how often the GPS updates the position, 0..86400 (0 = no automatic updates)")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	a := newActions(s)
	a.interval = *interval
	switch {
	case len(rest) == 0:
	case len(rest) == 1 && (rest[0] == "on" || rest[0] == "off"):
		a.gps = rest[0]
	default:
		return usageError(fs, "wants on or off")
	}
	if a.gps == "" && a.interval < 0 { // show
		l := s.companion()
		if l == nil {
			return exitFail
		}
		vars, err := l.Client.CustomVars()
		if err != nil {
			fmt.Println("error:", err)
			return exitFail
		}
		found := false
		for _, v := range vars {
			if strings.HasPrefix(v.Name, "gps") {
				fmt.Printf("  %-14s %s\n", v.Name, v.Value)
				found = true
			}
		}
		if !found {
			fmt.Println("this build has no GPS")
		}
		return exitOK
	}
	return withCompanion(s, a)
}

func cmdAdvert(s *session, args []string) int {
	fs := newFlags("advert", "[flood|zerohop]")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	kind := "flood"
	if len(rest) == 1 {
		kind = rest[0]
	} else if len(rest) > 1 {
		return usageError(fs, "wants flood or zerohop")
	}
	if kind != "flood" && kind != "zerohop" {
		return usageError(fs, "%q is not flood or zerohop", kind)
	}
	a := newActions(s)
	a.advert = kind
	return withCompanion(s, a)
}

func cmdSet(s *session, args []string) int {
	fs := newFlags("set", "name|position|share-location|multi-acks VALUE")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) < 2 {
		return usageError(fs, "wants a setting and a value")
	}
	value := strings.Join(rest[1:], " ")
	a := newActions(s)
	switch rest[0] {
	case "name":
		a.name = value
	case "position":
		a.position = value
	case "share-location":
		a.shareLoc = value
	case "multi-acks":
		a.multiAcks = value
	default:
		return usageError(fs, "cannot set %q", rest[0])
	}
	return withCompanion(s, a)
}

func cmdAutoAdd(s *session, args []string) int {
	fs := newFlags("auto-add", "[-overwrite on|off] [-max-hops N] [show|all|none|TYPES]")
	overwrite := fs.String("overwrite", "", "on or off: replace the oldest contact when the list is full")
	hops := fs.String("max-hops", "", "only auto-add nodes up to this many hops away (0..64, 0 = no limit)")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	a := newActions(s)
	a.autoOverwrite, a.autoHops = *overwrite, *hops
	switch {
	case len(rest) == 1:
		a.autoAdd = rest[0]
	case len(rest) == 0 && (*overwrite != "" || *hops != ""):
	case len(rest) == 0:
		a.autoAdd = "show"
	default:
		return usageError(fs, "wants one of show, all, none or a list of types")
	}
	return withCompanion(s, a)
}

func cmdPathHash(s *session, args []string) int {
	fs := newFlags("path-hash", "[0|1|2]")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	if len(rest) == 0 {
		l := s.companion()
		if l == nil {
			return exitFail
		}
		if d := l.Client.Info; d != nil && d.HasPathHash {
			fmt.Printf("path hash mode %d (%d-byte hashes)\n", d.PathHashMode, d.PathHashMode+1)
		} else {
			fmt.Println("this firmware does not report the path hash mode")
		}
		return exitOK
	}
	if _, err := strconv.Atoi(rest[0]); err != nil || len(rest) > 1 {
		return usageError(fs, "wants 0, 1 or 2")
	}
	a := newActions(s)
	a.pathHash = rest[0]
	return withCompanion(s, a)
}

func cmdScope(s *session, args []string) int {
	fs := newFlags("scope", "[set REGION | clear]")
	rest, code, ok := parseFlags(fs, args)
	if !ok {
		return code
	}
	var region string
	clear := false
	switch {
	case len(rest) == 0:
	case len(rest) == 1 && rest[0] == "clear":
		clear = true
	case len(rest) == 2 && rest[0] == "set":
		region = rest[1]
		if err := meshgore.ValidScopeName(region); err != nil {
			return usageError(fs, "%v", err)
		}
	default:
		return usageError(fs, "wants nothing, set REGION or clear")
	}
	l := s.companion()
	if l == nil {
		return exitFail
	}
	fail := func(err error) int { fmt.Println("error:", err); return exitFail }
	switch {
	case clear:
		if err := l.Client.ClearDefaultScope(); err != nil {
			return fail(err)
		}
	case region != "":
		if err := l.Client.SetDefaultScope(region); err != nil {
			return fail(err)
		}
	}
	sc, err := l.Client.DefaultScope()
	if err != nil {
		return fail(err)
	}
	switch {
	case !sc.Set:
		fmt.Println("default flood scope: none (floods go out unscoped)")
	default:
		fmt.Printf("default flood scope: %s (key %x)\n", sc.Name, sc.Key)
		if region != "" {
			fmt.Println("Repeaters forward a scoped flood only if they have that region (region put " + sc.Name + " on a repeater). Check yours before relying on it.")
		}
	}
	return exitOK
}
