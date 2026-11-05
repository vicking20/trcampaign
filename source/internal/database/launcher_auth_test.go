package database

import (
	"encoding/hex"
	"testing"
)

func TestLauncherTokenShape(t *testing.T) {
	a, err := launcherToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := launcherToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 48 || len(b) != 48 || a == b {
		t.Fatalf("unexpected launcher tokens: %q %q", a, b)
	}
	if raw, err := hex.DecodeString(a); err != nil || len(raw) != 24 {
		t.Fatalf("invalid launcher token %q: bytes=%d err=%v", a, len(raw), err)
	}
}
