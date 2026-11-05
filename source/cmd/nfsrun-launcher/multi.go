package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"nfsrun-server/internal/protocert"
	"os"
	"path/filepath"
)

// The launcher runs NFSTR_lsx<port>.exe, a copy of the player's game exe
// (PC 1.1.0.0) patched for the server; the original is never changed.
//
//	0x00838BA6  68 90 0C 00 00 -> 68 <port>        LSX port given to the Origin SDK
//	0x0041826D  74 1E -> EB 1E                     no single-instance mutex
//	0x018AEFBC  E8 EE 23 95 00 -> E8 1F 2D 00 00   the SDK's startup call reaches
//	                                               0x018B1CE0 (the retail exe calls an
//	                                               empty thunk: "sign in to Origin")
//	0x018B207B  75 4B -> EB 4B                     Origin "installed": always
//	0x018B20D8  75 0B -> EB 0B                     never start Origin / the EA app
//	0x0188F2E6  68 02 00 00 80 -> 68 01 00 00 80   game language (Locale) from HKCU
//	file 0x02234EA0..  Redirector hosts -> nfs. / scert-nfs. / stest-nfs. /
//	                   redirector.<domain> (the domain built into the launcher)
//
// NO_ORIGIN_START=0 keeps the Origin checks (only port and mutex patches).
const (
	vaLSXPort         = 0x00838BA6
	vaMutex           = 0x0041826D
	vaOriginStartup   = 0x018AEFBC
	vaOriginInstalled = 0x018B207B
	vaOriginRunning   = 0x018B20D8
	vaLocaleHive      = 0x0188F2E6
)

// patchBytes replaces from with to at va; already to is fine.
func patchBytes(d []byte, va uint32, from, to []byte) error {
	o, ok := peOffset(d, va)
	if !ok || o+len(from) > len(d) {
		return fmt.Errorf("0x%08X is not in the exe", va)
	}
	switch {
	case bytes.Equal(d[o:o+len(from)], from):
		copy(d[o:], to)
	case bytes.Equal(d[o:o+len(to)], to):
	default:
		return fmt.Errorf("unexpected bytes % X at 0x%08X: not NFS The Run PC 1.1.0.0", d[o:o+len(from)], va)
	}
	return nil
}

// patchCopy applies every patch of the game copy; notes says what it did.
func patchCopy(d []byte, port int, noOrigin bool, domain string) (notes []string, err error) {
	o, ok := peOffset(d, vaLSXPort)
	if !ok || o+5 > len(d) || d[o] != 0x68 {
		return nil, fmt.Errorf("LSX port constant not found: not NFS The Run PC 1.1.0.0")
	}
	if cur := binary.LittleEndian.Uint32(d[o+1:]); cur != 0xC90 && (cur < 3217 || cur > 3300) {
		return nil, fmt.Errorf("unexpected LSX port constant %d", cur)
	}
	binary.LittleEndian.PutUint32(d[o+1:], uint32(port))
	notes = append(notes, fmt.Sprintf("LSX port %d", port))
	if err := patchBytes(d, vaMutex, []byte{0x74, 0x1E}, []byte{0xEB, 0x1E}); err != nil {
		return nil, err
	}
	notes = append(notes, "no single-instance lock")
	if !noOrigin {
		return notes, nil
	}
	for _, p := range []struct {
		va       uint32
		from, to []byte
	}{
		{vaOriginStartup, []byte{0xE8, 0xEE, 0x23, 0x95, 0x00}, []byte{0xE8, 0x1F, 0x2D, 0x00, 0x00}},
		{vaOriginInstalled, []byte{0x75, 0x4B}, []byte{0xEB, 0x4B}},
		{vaOriginRunning, []byte{0x75, 0x0B}, []byte{0xEB, 0x0B}},
		{vaLocaleHive, []byte{0x68, 0x02, 0x00, 0x00, 0x80}, []byte{0x68, 0x01, 0x00, 0x00, 0x80}},
	} {
		if err := patchBytes(d, p.va, p.from, p.to); err != nil {
			return nil, err
		}
	}
	notes = append(notes, "Origin SDK talks to the launcher, never starts Origin/the EA app", "language from HKCU")
	if domain != "" {
		if err := protocert.PatchRedirector(d, domain); err != nil {
			return nil, err
		}
		notes = append(notes, "Redirector redirector."+domain)
	}
	return notes, nil
}

// multiCopy writes NFSTR_lsx<port>.exe next to gameExe when it differs from
// what the patches give.
func multiCopy(gameExe string, port int, noOrigin bool, domain string) (string, []string, error) {
	dst := filepath.Join(filepath.Dir(gameExe), fmt.Sprintf("NFSTR_lsx%d.exe", port))
	d, err := os.ReadFile(gameExe)
	if err != nil {
		return "", nil, err
	}
	notes, err := patchCopy(d, port, noOrigin, domain)
	if err != nil {
		return "", nil, err
	}
	if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, d) {
		return dst, notes, nil
	}
	if err := os.WriteFile(dst+".tmp", d, 0755); err != nil {
		return "", nil, err
	}
	_ = os.Remove(dst)
	return dst, notes, os.Rename(dst+".tmp", dst)
}

// copyReadsHKCU: the game copy reads Locale from HKCU.
func copyReadsHKCU(exe string) bool {
	d, err := os.ReadFile(exe)
	if err != nil {
		return false
	}
	o, ok := peOffset(d, vaLocaleHive)
	return ok && o+5 <= len(d) && d[o] == 0x68 && d[o+1] == 0x01 && d[o+4] == 0x80
}

func peOffset(d []byte, va uint32) (int, bool) {
	if len(d) < 0x40 || d[0] != 'M' || d[1] != 'Z' {
		return 0, false
	}
	pe := int(binary.LittleEndian.Uint32(d[0x3C:]))
	if pe+24 > len(d) {
		return 0, false
	}
	n := int(binary.LittleEndian.Uint16(d[pe+6:]))
	optSize := int(binary.LittleEndian.Uint16(d[pe+20:]))
	opt := pe + 24
	base := binary.LittleEndian.Uint32(d[opt+28:])
	for i := 0; i < n; i++ {
		o := opt + optSize + i*40
		if o+40 > len(d) {
			break
		}
		va0 := base + binary.LittleEndian.Uint32(d[o+12:])
		rsize := binary.LittleEndian.Uint32(d[o+16:])
		raw := binary.LittleEndian.Uint32(d[o+20:])
		if va >= va0 && va < va0+rsize {
			return int(raw + (va - va0)), true
		}
	}
	return 0, false
}

// lsxPortOf returns the LSX port compiled into a game exe.
func lsxPortOf(exe string) (int, error) {
	d, err := os.ReadFile(exe)
	if err != nil {
		return 0, err
	}
	o, ok := peOffset(d, vaLSXPort)
	if !ok || o+5 > len(d) || d[o] != 0x68 {
		return 0, fmt.Errorf("LSX port constant not found: not NFS The Run PC 1.1.0.0")
	}
	return int(binary.LittleEndian.Uint32(d[o+1:])), nil
}
