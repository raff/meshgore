package main

import (
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
