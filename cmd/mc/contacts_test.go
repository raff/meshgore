package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/raff/meshgore"
)

func contact(b byte, name string, mod uint32) *meshgore.Contact {
	ct := &meshgore.Contact{Name: name, Type: 2, LastMod: mod, LastAdvert: time.Unix(1700000000, 0), Lat: 37.5, Lon: -122.25}
	ct.PublicKey[0] = b
	return ct
}

func TestMergeContacts(t *testing.T) {
	old := []*meshgore.Contact{contact(1, "a", 10), contact(2, "b", 10)}
	got := mergeContacts(old, []*meshgore.Contact{contact(2, "b2", 20), contact(3, "c", 20)})
	if len(got) != 3 || got[0].Name != "a" || got[1].Name != "b2" || got[2].Name != "c" {
		t.Fatalf("unexpected merge: %v %v %v", got[0].Name, got[1].Name, got[len(got)-1].Name)
	}
	if old[1].Name != "b" {
		t.Error("merge must not modify its input")
	}
}

func TestContactCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home+"/.cache")

	if c, since := loadContactCache("node"); c != nil || since != 0 {
		t.Fatalf("empty cache: %v, %d", c, since)
	}
	saveContactCache("node", []*meshgore.Contact{contact(1, "a", 10), contact(2, "b", 30)}, 30)
	got, since := loadContactCache("node")
	if since != 30 || len(got) != 2 || got[1].Name != "b" || got[1].LastMod != 30 ||
		got[0].Lat != 37.5 || got[0].LastAdvert.Unix() != 1700000000 {
		t.Fatalf("round trip lost data: since=%d %+v", since, got)
	}
}

func TestLegacyCacheIsRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home+"/.cache")
	path, _ := contactCachePath("node")
	writeFile(t, path, `[{"key":"`+hexKey(7)+`","name":"old","type":2}]`)

	got, since := loadContactCache("node")
	if since != 0 || len(got) != 1 || got[0].Name != "old" {
		t.Fatalf("legacy cache: since=%d %+v", since, got)
	}
}

func typed(b byte, name string, typ byte, heardAgo time.Duration, now time.Time) *meshgore.Contact {
	ct := contact(b, name, 1)
	ct.Type = typ
	if heardAgo >= 0 {
		ct.LastAdvert = now.Add(-heardAgo)
	} else {
		ct.LastAdvert = time.Unix(0, 0) // never heard
	}
	return ct
}

func names(cs []*meshgore.Contact) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestContactView(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	cs := []*meshgore.Contact{
		typed(1, "bob", 1, 2*time.Hour, now),
		typed(2, "Alpha repeater", 2, 3*24*time.Hour, now),
		typed(3, "carol", 1, 10*time.Minute, now),
		typed(4, "Desert Room", 3, 30*time.Hour, now),
		typed(5, "ghost", 2, -1, now), // no advert time
		typed(6, "sensor9", 4, 5*time.Hour, now),
		typed(7, "future", 2, -2*time.Hour, now), // its clock is ahead: advert 2 hours from now
	}
	cs[6].LastAdvert = now.Add(2 * time.Hour)
	orig := names(cs)
	for _, c := range []struct {
		name string
		v    contactView
		want []string
	}{
		{"by name, ignoring case", contactView{}, []string{"Alpha repeater", "bob", "carol", "Desert Room", "future", "ghost", "sensor9"}},
		{"newest first", contactView{sortBy: "advert"}, []string{"future", "carol", "bob", "sensor9", "Desert Room", "Alpha repeater", "ghost"}},
		{"by type then name", contactView{sortBy: "type"}, []string{"bob", "carol", "Alpha repeater", "future", "ghost", "Desert Room", "sensor9"}},
		{"repeaters", contactView{types: meshgore.AutoAddRepeater}, []string{"Alpha repeater", "future", "ghost"}},
		{"repeaters and rooms", contactView{types: meshgore.AutoAddRepeater | meshgore.AutoAddRoom}, []string{"Alpha repeater", "Desert Room", "future", "ghost"}},
		{"name contains", contactView{match: "ROOM"}, []string{"Desert Room"}},
		{"heard within a day (a clock ahead was heard)", contactView{maxAge: 24 * time.Hour}, []string{"bob", "carol", "future", "sensor9"}},
		{"within a week hides the one never heard", contactView{maxAge: 7 * 24 * time.Hour, sortBy: "advert"}, []string{"future", "carol", "bob", "sensor9", "Desert Room", "Alpha repeater"}},
		{"combined", contactView{types: meshgore.AutoAddChat, maxAge: time.Hour}, []string{"carol"}},
		{"nothing matches", contactView{match: "zzz"}, nil},
	} {
		got := names(c.v.apply(cs, now))
		if len(got) != len(c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %q, want %q", c.name, got, c.want)
				break
			}
		}
	}
	if got := names(cs); len(got) != len(orig) || got[0] != orig[0] || got[5] != orig[5] {
		t.Error("apply changed the order of its input")
	}
}

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"90m": 90 * time.Minute, "36h": 36 * time.Hour, "7d": 7 * 24 * time.Hour,
		"1.5d": 36 * time.Hour, "2w": 14 * 24 * time.Hour, "0": 0,
	} {
		if got, err := parseAge(in); err != nil || got != want {
			t.Errorf("%q: got %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "soon", "-3d", "d", "3x"} {
		if _, err := parseAge(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAgeString(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "now", 5 * time.Minute: "5m ago", 90 * time.Minute: "1h ago",
		47 * time.Hour: "47h ago", 48 * time.Hour: "2d ago", 10 * 24 * time.Hour: "10d ago",
	} {
		if got := ageString(d); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
}

func TestContactLineClockAhead(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	ahead := typed(1, "ahead", 2, 0, now)
	ahead.LastAdvert = now.Add(48 * time.Hour)
	if got := contactLine(ahead, now); !strings.Contains(got, "its clock is ahead") || strings.Contains(got, "now") {
		t.Errorf("%q", got)
	}
	slightly := typed(2, "slightly", 2, 0, now)
	slightly.LastAdvert = now.Add(2 * time.Minute) // within the slack: fine
	if got := contactLine(slightly, now); strings.Contains(got, "ahead") || !strings.Contains(got, "(now)") {
		t.Errorf("%q", got)
	}
	never := typed(3, "never", 2, -1, now)
	if got := contactLine(never, now); !strings.Contains(got, "no advert time") {
		t.Errorf("%q", got)
	}
}

func TestHopsAndDistance(t *testing.T) {
	for n, want := range map[int]string{-1: "", 0: "direct", 1: "1 hop", 3: "3 hops"} {
		if got := hopsString(&meshgore.Contact{OutPathLen: n}); got != want {
			t.Errorf("hopsString(%d) = %q, want %q", n, got, want)
		}
	}
	// San Francisco to Los Angeles is about 559 km.
	d := distanceKm(position{37.7749, -122.4194}, position{34.0522, -118.2437})
	if d < 555 || d > 563 {
		t.Errorf("distance = %.1f km, want about 559", d)
	}
	if positionOf(0, 0) != nil {
		t.Error("0,0 should mean no position")
	}

	far, near, none := contact(1, "far", 1), contact(2, "near", 1), contact(3, "none", 1)
	far.Lat, far.Lon = 34.0522, -118.2437
	near.Lat, near.Lon = 37.8, -122.4
	none.Lat, none.Lon = 0, 0
	v := contactView{sortBy: "distance", from: positionOf(37.7749, -122.4194)}
	got := v.apply([]*meshgore.Contact{none, far, near}, time.Now())
	if got[0].Name != "near" || got[1].Name != "far" || got[2].Name != "none" {
		t.Errorf("sort by distance: %s %s %s", got[0].Name, got[1].Name, got[2].Name)
	}
	near.OutPathLen = 2
	var out strings.Builder
	printContacts(&out, []*meshgore.Contact{near, none}, time.Now(), v, false)
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "NAME") || !strings.Contains(lines[0], "DISTANCE") {
		t.Fatalf("table:\n%s", out.String())
	}
	if !strings.Contains(lines[1], "2 hops") || !strings.Contains(lines[1], " km") {
		t.Errorf("row lacks hops or distance: %s", lines[1])
	}
	if strings.Contains(lines[2], "km") {
		t.Errorf("a contact with no position shows a distance: %s", lines[2])
	}
	// the key column lines up in every row
	if strings.Index(lines[0], "KEY") != strings.Index(lines[1], fmt.Sprintf("%x", near.PublicKey[:6])) {
		t.Errorf("columns do not line up:\n%s", out.String())
	}
	out.Reset()
	none.OutPathLen = -1
	printContacts(&out, []*meshgore.Contact{none}, time.Now(), contactView{}, false)
	if strings.Contains(out.String(), "DISTANCE") || strings.Contains(out.String(), "ROUTE") {
		t.Errorf("empty columns are shown:\n%s", out.String())
	}
}

func TestCachePathLen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	direct, flood := contact(1, "direct", 1), contact(2, "flood", 1)
	direct.OutPathLen, flood.OutPathLen = 0, -1
	saveContactCache("pathnode", []*meshgore.Contact{direct, flood}, 1)
	got, _ := loadContactCache("pathnode")
	if len(got) != 2 || got[0].OutPathLen != 0 || got[1].OutPathLen != -1 {
		t.Errorf("path lengths lost in the cache: %+v", got)
	}
}
