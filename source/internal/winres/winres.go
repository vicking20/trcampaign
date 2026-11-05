// Package winres writes a COFF object (.syso) with Windows resources (icon,
// version information, manifest) for the Go linker. Standard library only.
package winres

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sort"
	"unicode/utf16"
)

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
	langEnUS    = 0x0409
)

// Machine types for the COFF header.
const (
	I386  = 0x014c
	AMD64 = 0x8664
)

// Version is the VS_FIXEDFILEINFO / StringFileInfo content.
type Version struct {
	Major, Minor, Patch, Build uint16
	Strings                    map[string]string // CompanyName, FileDescription, ..
}

type resource struct {
	typ, id uint32
	data    []byte
}

// Builder collects resources.
type Builder struct{ res []resource }

// AddIcon adds an icon group (ID groupID) with one image per size. Sizes
// below 256 are stored as 32-bit DIBs, 256 as PNG.
func (b *Builder) AddIcon(groupID uint32, imgs []image.Image) error {
	var grp bytes.Buffer
	binary.Write(&grp, binary.LittleEndian, [3]uint16{0, 1, uint16(len(imgs))})
	for i, im := range imgs {
		w := im.Bounds().Dx()
		var data []byte
		if w >= 256 {
			var buf bytes.Buffer
			if err := png.Encode(&buf, im); err != nil {
				return err
			}
			data = buf.Bytes()
		} else {
			data = dib(im)
		}
		id := uint32(i + 1)
		b.res = append(b.res, resource{rtIcon, id, data})
		bw := byte(w)
		if w >= 256 {
			bw = 0
		}
		grp.Write([]byte{bw, bw, 0, 0})
		binary.Write(&grp, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&grp, binary.LittleEndian, uint16(32)) // bit count
		binary.Write(&grp, binary.LittleEndian, uint32(len(data)))
		binary.Write(&grp, binary.LittleEndian, uint16(id))
	}
	b.res = append(b.res, resource{rtGroupIcon, groupID, grp.Bytes()})
	return nil
}

// dib: BITMAPINFOHEADER + bottom-up BGRA + empty AND mask.
func dib(im image.Image) []byte {
	r := im.Bounds()
	w, h := r.Dx(), r.Dy()
	maskRow := ((w + 31) / 32) * 4
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		ClrUsed, ClrImportant  uint32
	}{40, int32(w), int32(2 * h), 1, 32, 0, uint32(w*h*4 + maskRow*h), 0, 0, 0, 0})
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			n := color.NRGBAModel.Convert(im.At(r.Min.X+x, r.Min.Y+y)).(color.NRGBA)
			buf.Write([]byte{n.B, n.G, n.R, n.A})
		}
	}
	buf.Write(make([]byte, maskRow*h))
	return buf.Bytes()
}

// AddManifest adds the application manifest (ID 1).
func (b *Builder) AddManifest(xml string) {
	b.res = append(b.res, resource{rtManifest, 1, []byte(xml)})
}

// AddVersion adds VS_VERSIONINFO (ID 1).
func (b *Builder) AddVersion(v Version) {
	b.res = append(b.res, resource{rtVersion, 1, versionInfo(v)})
}

func utf16z(s string) []byte {
	var buf bytes.Buffer
	for _, c := range utf16.Encode([]rune(s)) {
		binary.Write(&buf, binary.LittleEndian, c)
	}
	buf.Write([]byte{0, 0})
	return buf.Bytes()
}

func pad4(buf *bytes.Buffer) {
	for buf.Len()%4 != 0 {
		buf.WriteByte(0)
	}
}

// block writes {wLength, wValueLength, wType, key, pad, value, pad, children}.
func block(key string, valueLen uint16, typ uint16, value []byte, children ...[]byte) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0}) // wLength, patched below
	binary.Write(&buf, binary.LittleEndian, valueLen)
	binary.Write(&buf, binary.LittleEndian, typ)
	buf.Write(utf16z(key))
	pad4(&buf)
	buf.Write(value)
	for _, c := range children {
		pad4(&buf)
		buf.Write(c)
	}
	out := buf.Bytes()
	binary.LittleEndian.PutUint16(out, uint16(len(out)))
	return out
}

func versionInfo(v Version) []byte {
	ms := uint32(v.Major)<<16 | uint32(v.Minor)
	ls := uint32(v.Patch)<<16 | uint32(v.Build)
	var fixed bytes.Buffer
	binary.Write(&fixed, binary.LittleEndian, [13]uint32{
		0xFEEF04BD, 0x00010000, ms, ls, ms, ls, 0x3F, 0, 0x00040004, 1, 0, 0, 0,
	})
	keys := make([]string, 0, len(v.Strings))
	for k := range v.Strings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var strs [][]byte
	for _, k := range keys {
		val := utf16z(v.Strings[k])
		strs = append(strs, block(k, uint16(len(val)/2), 1, val))
	}
	table := block("040904B0", 0, 1, nil, strs...)
	sfi := block("StringFileInfo", 0, 1, nil, table)
	var tr bytes.Buffer
	binary.Write(&tr, binary.LittleEndian, [2]uint16{langEnUS, 1200})
	vfi := block("VarFileInfo", 0, 1, nil, block("Translation", 4, 0, tr.Bytes()))
	return block("VS_VERSION_INFO", uint16(fixed.Len()), 0, fixed.Bytes(), sfi, vfi)
}

// COFF writes the .syso for the given machine.
func (b *Builder) COFF(machine uint16) ([]byte, error) {
	res := append([]resource(nil), b.res...)
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})
	// group by type
	type group struct {
		typ   uint32
		items []resource
	}
	var groups []group
	for _, r := range res {
		if len(groups) == 0 || groups[len(groups)-1].typ != r.typ {
			groups = append(groups, group{typ: r.typ})
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, r)
	}
	// layout: root dir, type dirs, name dirs (one per resource, one lang
	// entry each), data entries, data
	dirSize := func(n int) int { return 16 + 8*n }
	root := 0
	off := dirSize(len(groups))
	typeDirs := make([]int, len(groups))
	for i, g := range groups {
		typeDirs[i] = off
		off += dirSize(len(g.items))
	}
	langDirs := map[[2]uint32]int{}
	for _, g := range groups {
		for _, r := range g.items {
			langDirs[[2]uint32{r.typ, r.id}] = off
			off += dirSize(1)
		}
	}
	dataEntries := map[[2]uint32]int{}
	for _, g := range groups {
		for _, r := range g.items {
			dataEntries[[2]uint32{r.typ, r.id}] = off
			off += 16
		}
	}
	dataOff := map[[2]uint32]int{}
	for _, g := range groups {
		for _, r := range g.items {
			for off%8 != 0 {
				off++
			}
			dataOff[[2]uint32{r.typ, r.id}] = off
			off += len(r.data)
		}
	}
	for off%4 != 0 {
		off++
	}
	sec := make([]byte, off)
	putDir := func(at int, n int) {
		binary.LittleEndian.PutUint16(sec[at+14:], uint16(n)) // NumberOfIdEntries
	}
	putEntry := func(at, i int, id uint32, target uint32) {
		binary.LittleEndian.PutUint32(sec[at+16+8*i:], id)
		binary.LittleEndian.PutUint32(sec[at+16+8*i+4:], target)
	}
	putDir(root, len(groups))
	var relocs []uint32
	for gi, g := range groups {
		putEntry(root, gi, g.typ, 0x80000000|uint32(typeDirs[gi]))
		putDir(typeDirs[gi], len(g.items))
		for ii, r := range g.items {
			k := [2]uint32{r.typ, r.id}
			putEntry(typeDirs[gi], ii, r.id, 0x80000000|uint32(langDirs[k]))
			putDir(langDirs[k], 1)
			putEntry(langDirs[k], 0, langEnUS, uint32(dataEntries[k]))
			de := dataEntries[k]
			binary.LittleEndian.PutUint32(sec[de:], uint32(dataOff[k])) // RVA, relocated
			binary.LittleEndian.PutUint32(sec[de+4:], uint32(len(r.data)))
			relocs = append(relocs, uint32(de))
			copy(sec[dataOff[k]:], r.data)
		}
	}
	var relType uint16
	switch machine {
	case I386:
		relType = 7 // IMAGE_REL_I386_DIR32NB
	case AMD64:
		relType = 3 // IMAGE_REL_AMD64_ADDR32NB
	default:
		return nil, fmt.Errorf("machine 0x%x", machine)
	}
	const hdr, shdr = 20, 40
	rawPtr := hdr + shdr
	relPtr := rawPtr + len(sec)
	symPtr := relPtr + 10*len(relocs)
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, struct {
		Machine              uint16
		NumberOfSections     uint16
		TimeDateStamp        uint32
		PointerToSymbolTable uint32
		NumberOfSymbols      uint32
		SizeOfOptionalHeader uint16
		Characteristics      uint16
	}{machine, 1, 0, uint32(symPtr), 1, 0, 0})
	var name [8]byte
	copy(name[:], ".rsrc")
	binary.Write(&out, binary.LittleEndian, struct {
		Name                 [8]byte
		VirtualSize          uint32
		VirtualAddress       uint32
		SizeOfRawData        uint32
		PointerToRawData     uint32
		PointerToRelocations uint32
		PointerToLinenumbers uint32
		NumberOfRelocations  uint16
		NumberOfLinenumbers  uint16
		Characteristics      uint32
	}{name, 0, 0, uint32(len(sec)), uint32(rawPtr), uint32(relPtr), 0, uint16(len(relocs)), 0, 0x40000040})
	out.Write(sec)
	for _, r := range relocs {
		binary.Write(&out, binary.LittleEndian, r)
		binary.Write(&out, binary.LittleEndian, uint32(0)) // symbol 0 = .rsrc
		binary.Write(&out, binary.LittleEndian, relType)
	}
	// symbol table: .rsrc, section 1, static
	out.Write(name[:])
	binary.Write(&out, binary.LittleEndian, uint32(0))
	binary.Write(&out, binary.LittleEndian, int16(1))
	binary.Write(&out, binary.LittleEndian, uint16(0))
	out.Write([]byte{3, 0})                            // storage class static, no aux
	binary.Write(&out, binary.LittleEndian, uint32(4)) // empty string table
	return out.Bytes(), nil
}
