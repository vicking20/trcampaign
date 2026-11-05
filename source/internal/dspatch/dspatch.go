// Package dspatch turns the game exe into the dedicated race server exe.
//
// Ghidra, PC exe 1.1.0.0 (docs/MULTIPLAYER_DEDICATED.md):
//
//	0x0041835D  88 5C 24 18 C6 44 24 19 01  serverCapable = 0 (hard-coded)
//	         -> 66 C7 44 24 18 01 01 90 90  serverCapable = 1 (-dedicated works)
//	0x0041826D  74 1E                       single-instance mutex
//	         -> EB 1E                       never created/checked: the server runs
//	                                        next to the game and next to other servers
//	0x00416D92  83 E1 03                    and ecx,3: platform = dedicated ? 3 ("Linux") : 0
//	         -> 83 E1 00                    and ecx,0: always "Win32", so -dedicated loads the
//	                                        PC data (initfs_Win32) instead of EA's Linux server
//	                                        data, which the PC game does not have
package dspatch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"nfsrun-server/internal/protocert"
	"os"
)

type patch struct {
	Name     string
	VA       uint32
	Old, New []byte
}

var Patches = []patch{
	{"server mode (-dedicated)", 0x0041835D, []byte{0x88, 0x5C, 0x24, 0x18, 0xC6, 0x44, 0x24, 0x19, 0x01}, []byte{0x66, 0xC7, 0x44, 0x24, 0x18, 0x01, 0x01, 0x90, 0x90}},
	{"no single-instance lock", 0x0041826D, []byte{0x74, 0x1E}, []byte{0xEB, 0x1E}},
	{"Win32 data with -dedicated", 0x00416D92, []byte{0x83, 0xE1, 0x03}, []byte{0x83, 0xE1, 0x00}},
}

// FileOffset maps a VA of the PE image to a file offset.
func FileOffset(data []byte, va uint32) (int, error) {
	if len(data) < 0x40 || data[0] != 'M' || data[1] != 'Z' {
		return 0, errors.New("not a PE file")
	}
	pe := int(binary.LittleEndian.Uint32(data[0x3C:]))
	if pe+24 > len(data) {
		return 0, errors.New("bad PE header")
	}
	nsec := int(binary.LittleEndian.Uint16(data[pe+6:]))
	optsz := int(binary.LittleEndian.Uint16(data[pe+20:]))
	opt := pe + 24
	base := binary.LittleEndian.Uint32(data[opt+28:])
	for i := 0; i < nsec; i++ {
		o := opt + optsz + i*40
		if o+40 > len(data) {
			break
		}
		sva := binary.LittleEndian.Uint32(data[o+12:])
		rsize := binary.LittleEndian.Uint32(data[o+16:])
		raw := binary.LittleEndian.Uint32(data[o+20:])
		if va >= base+sva && va < base+sva+rsize {
			return int(raw + (va - base - sva)), nil
		}
	}
	return 0, fmt.Errorf("VA 0x%08X not in the file", va)
}

// Apply patches data in place. It returns what it did per patch; bytes that
// are neither original nor patched are an error (different exe build).
func Apply(data []byte) ([]string, error) {
	var notes []string
	for _, p := range Patches {
		off, err := FileOffset(data, p.VA)
		if err != nil {
			return notes, err
		}
		cur := data[off : off+len(p.Old)]
		switch {
		case bytes.Equal(cur, p.New):
			notes = append(notes, p.Name+": already patched")
		case bytes.Equal(cur, p.Old):
			copy(cur, p.New)
			notes = append(notes, p.Name+": patched")
		default:
			return notes, fmt.Errorf("%s: unexpected bytes % X at 0x%08X (different exe build)", p.Name, cur, p.VA)
		}
	}
	return notes, nil
}

// Ensure writes dst: src with the patches and, when domain is set, the
// Redirector hosts pointed at it (so the host needs no patch tool). dst is
// rewritten only when it differs.
func Ensure(src, dst, domain string) ([]string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	notes, err := Apply(data)
	if err != nil {
		return notes, err
	}
	if domain != "" {
		if err := protocert.PatchRedirector(data, domain); err != nil {
			return notes, err
		}
		notes = append(notes, "Redirector hosts -> "+domain)
	}
	if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, data) {
		return notes, nil
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0755); err != nil {
		return notes, err
	}
	return append(notes, "written "+dst), os.Rename(tmp, dst)
}
