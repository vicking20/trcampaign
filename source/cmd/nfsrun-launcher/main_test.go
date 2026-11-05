package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientKeyIsEd25519SeedAndSignsAuthMessage(t *testing.T) {
	seed := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	pub, priv, err := launcherKeyPair(seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(pub) != ed25519.PublicKeySize || len(priv) != ed25519.PrivateKeySize {
		t.Fatalf("key sizes pub=%d priv=%d", len(pub), len(priv))
	}
	msg := launcherAuthMessage("abcd", "0123", "RacerOne", hex.EncodeToString(pub), "ABCDE-12345", "")
	if !ed25519.Verify(pub, msg, ed25519.Sign(priv, msg)) {
		t.Fatal("signature did not verify")
	}
}

func TestFirstStartWritesMinimalINIAndKeyFile(t *testing.T) {
	dir := t.TempDir()
	c, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	ini, _ := os.ReadFile(filepath.Join(dir, iniName))
	for _, bad := range []string{"CLIENT_KEY", "SERVER_HTTP", "REGISTRY", "NO_ORIGIN_START"} {
		if strings.Contains(string(ini), bad) {
			t.Errorf("default ini has %s", bad)
		}
	}
	for _, want := range []string{"PLAYER_NAME=", "INVITE_CODE=", "LANGUAGE=auto", "LSX_LISTEN=127.0.0.1:3216"} {
		if !strings.Contains(string(ini), want) {
			t.Errorf("default ini lacks %s", want)
		}
	}
	if err := ensureClientKey(dir, &c); err != nil || len(c.ClientKey) != 64 {
		t.Fatalf("key %q %v", c.ClientKey, err)
	}
	again, err := loadConfig(dir)
	if err != nil || again.ClientKey != c.ClientKey {
		t.Fatalf("key not kept: %q vs %q (%v)", again.ClientKey, c.ClientKey, err)
	}
}

func TestOldINIMigrates(t *testing.T) {
	dir := t.TempDir()
	key := strings.Repeat("ab", 32)
	old := "PERSONA=RacerOne\r\nCLIENT_KEY=" + key + "\r\nCLAIM_CODE=ABCDE-12345\r\nLOCALE=ru_RU\r\nREGISTRY=auto\r\nLSX_LISTEN=127.0.0.1:3218\r\nSOLO_ONLINE=0\r\nEXTRA_PLAYLISTS=1\r\n"
	os.WriteFile(filepath.Join(dir, iniName), []byte(old), 0644)
	c, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientKey != key || c.Persona != "RacerOne" || c.InviteCode != "ABCDE-12345" || c.Language != "ru_RU" || c.LSXListen != "127.0.0.1:3218" || c.SoloOnline || !c.ExtraPlaylists {
		t.Fatalf("migrated %+v", c)
	}
	ini, _ := os.ReadFile(filepath.Join(dir, iniName))
	s := string(ini)
	for _, want := range []string{"PLAYER_NAME=RacerOne", "INVITE_CODE=ABCDE-12345", "LANGUAGE=ru_RU", "LSX_LISTEN=127.0.0.1:3218", "SOLO_ONLINE=0", "EXTRA_PLAYLISTS=1"} {
		if !strings.Contains(s, want) {
			t.Errorf("new ini lacks %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, key) || strings.Contains(s, "REGISTRY") || strings.Contains(s, "PERSONA=") {
		t.Errorf("old keys left:\n%s", s)
	}
	if k, _, _ := readKeyFile(dir); k != key {
		t.Fatalf("key file %q", k)
	}
	if _, err := os.Stat(filepath.Join(dir, iniName+".old")); err != nil {
		t.Fatal("no backup of the old ini")
	}
	// second start: nothing to migrate, same values
	if c2, err := loadConfig(dir); err != nil || c2.ClientKey != key || c2.Persona != "RacerOne" {
		t.Fatalf("reload %+v %v", c2, err)
	}
}

func TestClearInviteCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, iniName)
	os.WriteFile(path, []byte("PLAYER_NAME=Player\nINVITE_CODE=ABCDE-12345\n"), 0644)
	c := config{InviteCode: "ABCDE-12345"}
	if err := clearInviteCode(dir, &c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if c.InviteCode != "" || string(data) != "PLAYER_NAME=Player\r\nINVITE_CODE=\r\n" {
		t.Fatalf("code not cleared: %q", data)
	}
}

func TestInvalidKeyIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	c := config{ClientKey: "broken-key"}
	if err := ensureClientKey(dir, &c); err == nil {
		t.Fatal("invalid key silently replaced")
	}
	if _, err := os.Stat(filepath.Join(dir, keyName)); err == nil {
		t.Fatal("key file written for an invalid key")
	}
}

type confirmUI struct {
	consoleUI
	asked  int
	answer bool
}

func (u *confirmUI) Confirm(title, text string) bool { u.asked++; return u.answer }

func TestConsentAskedOncePerItem(t *testing.T) {
	dir := t.TempDir()
	c := config{ClientKey: strings.Repeat("cd", 32)}
	ui := &confirmUI{answer: false}
	items := []consentItem{{"copy", "a"}, {"language", "b"}}
	if ensureConsent(ui, dir, &c, items) || ui.asked != 1 {
		t.Fatal("no should stop")
	}
	ui.answer = true
	if !ensureConsent(ui, dir, &c, items) || ui.asked != 2 {
		t.Fatal("yes should pass")
	}
	_, allowed, _ := readKeyFile(dir)
	c2 := config{ClientKey: c.ClientKey, Allowed: allowed}
	if !ensureConsent(ui, dir, &c2, items) || ui.asked != 2 {
		t.Fatal("asked again for the same items")
	}
	if !ensureConsent(ui, dir, &c2, append(items, consentItem{"playlists", "c"})) || ui.asked != 3 {
		t.Fatal("a new item must be asked")
	}
}

func TestClientKeyForInstance(t *testing.T) {
	base := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got := clientKeyForInstance(base, 1); got != base {
		t.Fatalf("slot 1 changed: %q", got)
	}
	a, b := clientKeyForInstance(base, 2), clientKeyForInstance(base, 3)
	if len(a) != 64 || len(b) != 64 || a == b || a == base {
		t.Fatalf("derived keys: %q %q", a, b)
	}
}
