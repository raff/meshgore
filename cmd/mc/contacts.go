package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
