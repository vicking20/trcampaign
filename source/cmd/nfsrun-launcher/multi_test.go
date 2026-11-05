package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"nfsrun-server/internal/protocert"
)

// Needs the retail exe: NFSTR_EXE=<path to Need For Speed The Run.exe>.
func realExe(t *testing.T) (string, []byte) {
	src := os.Getenv("NFSTR_EXE")
	if src == "" {
		t.Skip("NFSTR_EXE not set")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "game.exe")
	os.WriteFile(exe, b, 0644)
	return exe, b
}

func TestCopyPortOnly(t *testing.T) {
	exe, orig := realExe(t)
	cp, _, err := multiCopy(exe, 3217, false, "")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := os.ReadFile(cp)
	o, _ := peOffset(d, vaLSXPort)
	if binary.LittleEndian.Uint32(d[o+1:]) != 3217 {
		t.Fatal("port not patched")
	}
	if n := diffBytes(orig, d); n != 2 { // port byte + mutex jump
		t.Fatalf("changed bytes: %d", n)
	}
	if cur, _ := os.ReadFile(exe); !bytes.Equal(cur, orig) {
		t.Fatal("the original exe changed")
	}
}

func TestCopyForServer(t *testing.T) {
	exe, orig := realExe(t)
	cp, notes, err := multiCopy(exe, 3216, true, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := os.ReadFile(cp)
	for _, c := range []struct {
		va   uint32
		want []byte
	}{
		{vaMutex, []byte{0xEB, 0x1E}},
		{vaOriginStartup, []byte{0xE8, 0x1F, 0x2D, 0x00, 0x00}},
		{vaOriginInstalled, []byte{0xEB, 0x4B}},
		{vaOriginRunning, []byte{0xEB, 0x0B}},
		{vaLocaleHive, []byte{0x68, 0x01, 0x00, 0x00, 0x80}},
	} {
		o, _ := peOffset(d, c.va)
		if !bytes.Equal(d[o:o+len(c.want)], c.want) {
			t.Errorf("0x%08X: % X", c.va, d[o:o+len(c.want)])
		}
	}
	_, domain, err := protocert.ExeHosts(cp)
	if err != nil || domain != "example.org" {
		t.Fatalf("Redirector %q %v", domain, err)
	}
	if !copyReadsHKCU(cp) || len(notes) < 4 {
		t.Fatalf("notes %q", notes)
	}
	// an exe the player already patched with the Python tools works too
	os.WriteFile(exe, d, 0644)
	if _, _, err := multiCopy(exe, 3216, true, "play.example.net"); err != nil {
		t.Fatal(err)
	}
	if _, domain, _ := protocert.ExeHosts(cp); domain != "play.example.net" {
		t.Fatalf("re-patched domain %q", domain)
	}
	if _, _, err := multiCopy(exe, 3216, true, "a-very-long-domain.example.org"); err == nil {
		t.Fatal("too long domain accepted")
	}
	_ = orig
}

func diffBytes(a, b []byte) int {
	n := 0
	for i := range a {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}
