package dspatch

import (
	"encoding/binary"
	"testing"
)

// fakePE: one section mapping VA 0x401000.. to file offset 0x400.
func fakePE() []byte {
	d := make([]byte, 0x400+0x20000)
	d[0], d[1] = 'M', 'Z'
	pe := 0x80
	binary.LittleEndian.PutUint32(d[0x3C:], uint32(pe))
	binary.LittleEndian.PutUint16(d[pe+6:], 1)
	binary.LittleEndian.PutUint16(d[pe+20:], 0xE0)
	opt := pe + 24
	binary.LittleEndian.PutUint32(d[opt+28:], 0x400000)
	s := opt + 0xE0
	binary.LittleEndian.PutUint32(d[s+12:], 0x1000)
	binary.LittleEndian.PutUint32(d[s+16:], 0x20000)
	binary.LittleEndian.PutUint32(d[s+20:], 0x400)
	for _, p := range Patches {
		off, _ := FileOffset(d, p.VA)
		copy(d[off:], p.Old)
	}
	return d
}

func TestApply(t *testing.T) {
	d := fakePE()
	if n, err := Apply(d); err != nil || len(n) != len(Patches) {
		t.Fatal(n, err)
	}
	n, err := Apply(d)
	if err != nil || n[0] != "server mode (-dedicated): already patched" {
		t.Fatal(n, err)
	}
	off, _ := FileOffset(d, 0x0041826D)
	d[off] = 0x90
	if _, err := Apply(d); err == nil {
		t.Fatal("unknown bytes accepted")
	}
}
