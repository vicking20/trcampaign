package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nfsrun-server/internal/config"
	"nfsrun-server/internal/protocert"
)

func fakeGameExe(t *testing.T, dir, domain string) string {
	t.Helper()
	b := make([]byte, protocert.RedirectorSlots[3].Offset+64)
	copy(b, "MZ")
	b[0x3c] = 0x80
	copy(b[0x80:], "PE\x00\x00")
	for _, s := range protocert.RedirectorSlots {
		v := s.Retail
		if domain != "" {
			v = s.Prefix + domain
		}
		copy(b[s.Offset:s.Offset+s.Size], v)
	}
	p := filepath.Join(dir, "Need For Speed The Run.exe")
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCertTargetPrefersThePatchedExe(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.DSGameDir = dir
	c.BaseDomain = "other.org"

	fakeGameExe(t, dir, "example.net")
	d, hosts, from, note, err := certTarget(c, "", "")
	if err != nil || d != "example.net" || len(hosts) != 4 || !strings.HasPrefix(from, "game exe") || !strings.Contains(note, "BASE_DOMAIN") {
		t.Fatal(d, hosts, from, note, err)
	}
	if _, _, _, _, err := certTarget(c, "x.org", ""); err == nil {
		t.Fatal("--domain contradicting the exe must fail")
	}

	// retail exe in DS_GAME_DIR: fall back to BASE_DOMAIN, with a note
	fakeGameExe(t, dir, "")
	d, _, from, note, err = certTarget(c, "", "")
	if err != nil || d != "other.org" || !strings.Contains(from, "BASE_DOMAIN") || note == "" {
		t.Fatal(d, from, note, err)
	}
	// ...but an explicit --game-exe that is retail is an error
	if _, _, _, _, err := certTarget(c, "", filepath.Join(dir, "Need For Speed The Run.exe")); !errors.Is(err, protocert.ErrRetailExe) {
		t.Fatal(err)
	}
	c.BaseDomain = ""
	if _, _, _, _, err := certTarget(c, "", ""); err == nil {
		t.Fatal("no domain anywhere must fail")
	}
}

func TestMakeAndCheckCert(t *testing.T) {
	dir := t.TempDir()
	game := filepath.Join(dir, "game")
	os.MkdirAll(game, 0755)
	fakeGameExe(t, game, "example.net")
	crt := filepath.Join(dir, "certs", "protossl_redirector.crt")
	key := filepath.Join(dir, "certs", "protossl_redirector.key")
	env := filepath.Join(dir, "server.env")
	os.WriteFile(env, []byte("CERT_FILE="+crt+"\nKEY_FILE="+key+"\nDS_GAME_DIR="+game+"\nBASE_DOMAIN=example.net\n"), 0644)

	if _, code := runCertCommand(env, "", false, true, false, "", ""); code != 1 {
		t.Fatal("missing certificate must fail the check, code", code)
	}
	if _, code := runCertCommand(env, "", true, false, false, "", ""); code != 0 {
		t.Fatal("make-cert", code)
	}
	if _, code := runCertCommand(env, "", false, true, false, "", ""); code != 0 {
		t.Fatal("fresh certificate must pass, code", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "certs", "protossl_redirector.info.txt")); err != nil {
		t.Fatal(err)
	}
	// a certificate for another domain is refused and --fix replaces it
	other, otherKey, _ := protocert.Generate("unused.example", time.Now())
	os.WriteFile(crt, other, 0644)
	os.WriteFile(key, otherKey, 0600)
	if _, code := runCertCommand(env, "", false, true, false, "", ""); code != 1 {
		t.Fatal("wrong domain must fail, code", code)
	}
	if _, code := runCertCommand(env, "", false, true, true, "", ""); code != 0 {
		t.Fatal("--fix", code)
	}
	a, _ := os.ReadFile(crt)
	b, _ := os.ReadFile(key)
	if p := protocert.Problems(a, b, protocert.HostsFor("example.net"), time.Now()); len(p) != 0 {
		t.Fatal(p)
	}
	olds, _ := filepath.Glob(crt + ".old-*")
	if len(olds) == 0 {
		t.Fatal("old certificate not kept")
	}
	if h, code := runCertCommand(env, filepath.Join(game, "Need For Speed The Run.exe"), false, false, false, "", ""); !h || code != 0 {
		t.Fatal("--exe-domain", code)
	}
	if h, _ := runCertCommand(env, "", false, false, false, "", ""); h {
		t.Fatal("no cert flag must not be handled")
	}
}
