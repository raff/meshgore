package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/raff/meshgore"
)

// cachedContact is a contact as stored in the cache file.
type cachedContact struct {
	Key        string  `json:"key"`
	Name       string  `json:"name"`
	Type       byte    `json:"type"`
	Flags      byte    `json:"flags,omitempty"`
	LastAdvert int64   `json:"last_advert,omitempty"`
	Lat        float64 `json:"lat,omitempty"`
	Lon        float64 `json:"lon,omitempty"`
	LastMod    uint32  `json:"lastmod,omitempty"`
	PathLen    *int    `json:"path_len,omitempty"` // hops of the stored route; absent means flood or unknown
}

// cacheFile is the on-disk format. LastMod is what the next incremental sync
// asks for. (Older versions wrote a bare array of contacts; it is still read.)
type cacheFile struct {
	LastMod  uint32          `json:"lastmod"`
	Contacts []cachedContact `json:"contacts"`
}

// contactCachePath returns the cache file for a companion node.
func contactCachePath(node string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mc", "contacts-"+node+".json"), nil
}

// loadContactCache returns the cached contacts and the LastMod to sync from.
// A missing or unreadable cache gives nothing and 0.
func loadContactCache(node string) ([]*meshgore.Contact, uint32) {
	path, err := contactCachePath(node)
	if err != nil {
		return nil, 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0
	}
	var f cacheFile
	if json.Unmarshal(data, &f) != nil {
		var legacy []cachedContact
		if json.Unmarshal(data, &legacy) != nil {
			return nil, 0
		}
		f = cacheFile{Contacts: legacy} // LastMod 0: the next sync refetches everything
	}
	var out []*meshgore.Contact
	for _, e := range f.Contacts {
		key, err := hex.DecodeString(e.Key)
		if err != nil || len(key) != 32 {
			continue
		}
		ct := &meshgore.Contact{
			Name: e.Name, Type: e.Type, Flags: e.Flags, OutPathLen: -1,
			LastAdvert: time.Unix(e.LastAdvert, 0), Lat: e.Lat, Lon: e.Lon, LastMod: e.LastMod,
		}
		if e.PathLen != nil {
			ct.OutPathLen = *e.PathLen
		}
		copy(ct.PublicKey[:], key)
		out = append(out, ct)
	}
	return out, f.LastMod
}

// saveContactCache writes contacts to the cache; failure only costs speed.
func saveContactCache(node string, contacts []*meshgore.Contact, lastMod uint32) {
	path, err := contactCachePath(node)
	if err != nil {
		return
	}
	f := cacheFile{LastMod: lastMod, Contacts: make([]cachedContact, len(contacts))}
	for i, ct := range contacts {
		f.Contacts[i] = cachedContact{
			Key: hex.EncodeToString(ct.PublicKey[:]), Name: ct.Name, Type: ct.Type, Flags: ct.Flags,
			LastAdvert: ct.LastAdvert.Unix(), Lat: ct.Lat, Lon: ct.Lon, LastMod: ct.LastMod,
		}
		if ct.OutPathLen >= 0 {
			n := ct.OutPathLen
			f.Contacts[i].PathLen = &n
		}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Println("could not write contact cache:", err)
	}
}

// mergeContacts applies fresh on top of old: known contacts are updated in
// place, new ones are appended.
func mergeContacts(old, fresh []*meshgore.Contact) []*meshgore.Contact {
	out := append([]*meshgore.Contact(nil), old...)
	index := make(map[[32]byte]int, len(out))
	for i, ct := range out {
		index[ct.PublicKey] = i
	}
	for _, ct := range fresh {
		if i, ok := index[ct.PublicKey]; ok {
			out[i] = ct
			continue
		}
		index[ct.PublicKey] = len(out)
		out = append(out, ct)
	}
	return out
}

// syncContacts brings the cache up to date and returns every contact. It only
// asks the device for contacts changed since the last sync; full discards the
// cache and downloads everything, which is also how contacts deleted on the
// device disappear from the cache. changed is how many contacts were fetched.
func syncContacts(client *meshgore.Client, node string, full bool) (all []*meshgore.Contact, changed int, err error) {
	var cached []*meshgore.Contact
	var since uint32
	if !full {
		cached, since = loadContactCache(node)
	}
	if since > 0 {
		fmt.Printf("Syncing contacts changed since %s (%d cached)...\n", time.Unix(int64(since), 0).Format(time.DateTime), len(cached))
	} else {
		fmt.Println("Downloading all contacts (slow)...")
	}
	fresh, lastMod, err := client.GetContactsSince(since)
	if err != nil {
		return nil, 0, err
	}
	all = mergeContacts(cached, fresh)
	saveContactCache(node, all, lastMod)
	return all, len(fresh), nil
}

// resolveContact finds target without touching the radio when it can: a full
// 64-char hex key is used as is, then the cache, and only then a (incremental)
// contact sync.
func resolveContact(client *meshgore.Client, node, target string) (*meshgore.Contact, error) {
	if target == "-" { // "-" stands for the repeater in MC_REPEATER
		if target = os.Getenv("MC_REPEATER"); target == "" {
			return nil, errors.New(`"-" means the repeater in MC_REPEATER, which is not set`)
		}
	}
	if key, err := hex.DecodeString(target); err == nil && len(key) == 32 {
		ct := &meshgore.Contact{Name: "(by key)", Type: 2}
		copy(ct.PublicKey[:], key)
		return ct, nil
	}
	cached, _ := loadContactCache(node)
	if ct, err := meshgore.FindContact(cached, target); err == nil {
		return ct, nil
	} else if !errors.Is(err, meshgore.ErrNoMatch) {
		return nil, err // ambiguous: fetching would not help
	}
	fmt.Println("Not in contact cache.")
	all, _, err := syncContacts(client, node, false)
	if err != nil {
		return nil, err
	}
	return meshgore.FindContact(all, target)
}

// contactView says which contacts to list and in what order.
type contactView struct {
	types  byte          // advert types to show, one bit each (1<<type); 0 means all
	match  string        // show only names containing this, ignoring case
	maxAge time.Duration // hide contacts not heard from for longer than this; 0 means no limit
	sortBy string        // "name" (the default), "advert" (most recently heard first), "type" or "distance"
	from   *position     // where we are, for distances and sorting by them; nil if unknown
}

// position is a point on the earth, in degrees.
type position struct{ lat, lon float64 }

// positionOf returns where a node is, or nil if it has no position (0,0 means none).
func positionOf(lat, lon float64) *position {
	if lat == 0 && lon == 0 {
		return nil
	}
	return &position{lat, lon}
}

// distanceKm is the great-circle distance between two positions.
func distanceKm(a, b position) float64 {
	const r = 6371.0
	rad := math.Pi / 180
	dLat, dLon := (b.lat-a.lat)*rad, (b.lon-a.lon)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(a.lat*rad)*math.Cos(b.lat*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}

// distanceFrom is how far ct is from v.from in km; ok is false if either has no position.
func (v contactView) distanceFrom(ct *meshgore.Contact) (km float64, ok bool) {
	p := positionOf(ct.Lat, ct.Lon)
	if v.from == nil || p == nil {
		return 0, false
	}
	return distanceKm(*v.from, *p), true
}

// distanceString formats a distance in km, or in miles.
func distanceString(km float64, miles bool) string {
	if miles {
		return fmt.Sprintf("%.1f mi", km/1.609344)
	}
	return fmt.Sprintf("%.1f km", km)
}

// hopsString describes a contact's stored route; empty when there is none (flood).
func hopsString(ct *meshgore.Contact) string {
	switch {
	case ct.OutPathLen < 0:
		return ""
	case ct.OutPathLen == 0:
		return "direct"
	case ct.OutPathLen == 1:
		return "1 hop"
	}
	return fmt.Sprintf("%d hops", ct.OutPathLen)
}

// clockSlack is how far ahead of ours a node's advert time can be before we call its clock wrong.
const clockSlack = 5 * time.Minute

// clockAhead reports an advert time in the future. The time is the advertiser's own, so a node
// with a wrong clock shows up here; how long ago it really advertised is then unknown.
func clockAhead(ct *meshgore.Contact, now time.Time) bool {
	return heard(ct) && ct.LastAdvert.After(now.Add(clockSlack))
}

// heard says whether a contact has an advert time at all. A contact added by hand, or
// one the node never heard, has none.
func heard(ct *meshgore.Contact) bool { return ct.LastAdvert.Unix() > 0 }

// apply returns the contacts that pass the filters, in order. It leaves cs alone.
func (v contactView) apply(cs []*meshgore.Contact, now time.Time) []*meshgore.Contact {
	var out []*meshgore.Contact
	for _, ct := range cs {
		if v.types != 0 && (ct.Type > 7 || v.types&(1<<ct.Type) == 0) {
			continue
		}
		if v.match != "" && !strings.Contains(strings.ToLower(ct.Name), strings.ToLower(v.match)) {
			continue
		}
		if v.maxAge > 0 && (!heard(ct) || (!clockAhead(ct, now) && now.Sub(ct.LastAdvert) > v.maxAge)) {
			continue // one never heard counts as stale; one with its clock ahead was clearly heard
		}
		out = append(out, ct)
	}
	byName := func(a, b *meshgore.Contact) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	}
	when := func(ct *meshgore.Contact) time.Time {
		if clockAhead(ct, now) {
			return now // its time is wrong; it was heard, so rank it with the recent ones
		}
		return ct.LastAdvert
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch v.sortBy {
		case "advert":
			if ta, tb := when(a), when(b); !ta.Equal(tb) {
				return ta.After(tb)
			}
		case "type":
			if a.Type != b.Type {
				return a.Type < b.Type
			}
		case "distance":
			da, oka := v.distanceFrom(a)
			db, okb := v.distanceFrom(b)
			if oka != okb {
				return oka // those with a distance come first
			}
			if oka && da != db {
				return da < db
			}
		}
		return byName(a, b) < 0
	})
	return out
}

// parseAge reads a duration such as 90m, 36h or 7d (days and weeks, which
// time.ParseDuration lacks).
func parseAge(s string) (time.Duration, error) {
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if strings.HasSuffix(s, suffix) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, suffix), 64)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("%q is not an age like 90m, 36h or 7d", s)
			}
			return time.Duration(n * float64(unit)), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%q is not an age like 90m, 36h or 7d", s)
	}
	return d, nil
}

// ageString says how long ago something was, briefly: "5m", "14h", "9d".
func ageString(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}

// contactCells returns the columns of a contact's row: name, type, key, last advert, route, distance.
// The last three are empty when unknown.
func contactCells(ct *meshgore.Contact, now time.Time, v contactView, miles bool) []string {
	when := "no advert time"
	switch {
	case clockAhead(ct, now):
		when = fmt.Sprintf("%s (its clock is ahead)", ct.LastAdvert.Format(time.DateTime))
	case heard(ct):
		when = fmt.Sprintf("%s (%s)", ct.LastAdvert.Format(time.DateTime), ageString(now.Sub(ct.LastAdvert)))
	}
	dist := ""
	if km, ok := v.distanceFrom(ct); ok {
		dist = distanceString(km, miles)
	}
	return []string{ct.Name, ct.TypeName(), fmt.Sprintf("%x", ct.PublicKey[:6]), when, hopsString(ct), dist}
}

// contactLine formats one contact on a line, without a header.
func contactLine(ct *meshgore.Contact, now time.Time) string {
	return strings.Join(contactCells(ct, now, contactView{}, false), "  ")
}

// printContacts writes the contacts as a table with a header. The ROUTE and
// DISTANCE columns are left out when no contact has anything to put in them.
func printContacts(w io.Writer, cs []*meshgore.Contact, now time.Time, v contactView, miles bool) {
	header := []string{"NAME", "TYPE", "KEY", "LAST ADVERT", "ROUTE", "DISTANCE"}
	rows := [][]string{header}
	for _, ct := range cs {
		rows = append(rows, contactCells(ct, now, v, miles))
	}
	cols := len(header)
	for c := cols - 1; c >= 4; c-- { // drop an empty trailing column
		empty := true
		for _, r := range rows[1:] {
			if r[c] != "" {
				empty = false
			}
		}
		if !empty {
			break
		}
		cols--
	}
	width := make([]int, cols)
	for _, r := range rows {
		for c := 0; c < cols; c++ {
			if n := utf8.RuneCountInString(r[c]); n > width[c] {
				width[c] = n
			}
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			cell := r[c]
			if cell == "" && r[0] != "NAME" {
				cell = "-"
			}
			if c == cols-1 {
				b.WriteString(cell)
			} else {
				fmt.Fprintf(&b, "%-*s  ", width[c], cell)
			}
		}
		fmt.Fprintln(w, "  "+b.String())
	}
}
