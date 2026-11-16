package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hexKey returns a 64-digit hex key whose first byte is b.
func hexKey(b byte) string {
	var k [32]byte
	k[0] = b
	return hex.EncodeToString(k[:])
}
