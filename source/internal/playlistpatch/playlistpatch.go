// Package playlistpatch adds the game's 10 unused multiplayer playlists to its
// PlaylistSet.
//
// NFSGame.sb (Frostbite 2 ebx) holds _c4/Gameplay/Playlist/PlaylistSet;
// PlaylistArrays[0] lists the 6 playlists of the Multiplayer menu.
// Nfs12Career/Playlists/ has 10 more complete playlists that no set references
// (European Elite, All American, Japan, Nissan Showdown, Porsche Unleashed,
// Classics, Insomnia, AWD, Ford, Lamborghini Untamed). The game looks
// playlists up by GUID only inside the set.
//
// The patch appends the 10 playlists to PlaylistArrays[0] (one import each,
// 10 more array elements), rewrites that ebx entry (size, SHA-1) and the
// bundle size in NFSGame.toc. The toc body is XOR-encoded with a key in its
// header and not signed; .sb entries are stored back to back, so the rest of
// the bundle just moves.
package playlistpatch

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// GUID is a Frostbite GUID as stored in ebx (little-endian first 3 groups).
type GUID [16]byte

func (g GUID) String() string {
	b := g[:]
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(b[0:4]),
		binary.LittleEndian.Uint16(b[4:6]), binary.LittleEndian.Uint16(b[6:8]), b[8:10], b[10:16])
}

// ParseGUID reads the usual text form.
func ParseGUID(s string) (GUID, error) {
	var g GUID
	h := strings.ReplaceAll(strings.TrimSpace(s), "-", "")
	if len(h) != 32 {
		return g, fmt.Errorf("bad GUID %q", s)
	}
	var raw [16]byte
	for i := 0; i < 16; i++ {
		var v byte
		if _, err := fmt.Sscanf(h[2*i:2*i+2], "%02x", &v); err != nil {
			return g, fmt.Errorf("bad GUID %q", s)
		}
		raw[i] = v
	}
	// text order -> ebx order
	binary.LittleEndian.PutUint32(g[0:4], binary.BigEndian.Uint32(raw[0:4]))
	binary.LittleEndian.PutUint16(g[4:6], binary.BigEndian.Uint16(raw[4:6]))
	binary.LittleEndian.PutUint16(g[6:8], binary.BigEndian.Uint16(raw[6:8]))
	copy(g[8:], raw[8:])
	return g, nil
}

// Playlist is one of the unused playlists.
type Playlist struct {
	Name     string // display name
	Path     string // ebx asset name (lower case, as in the bundle)
	Instance string // instance GUID = playListId
}

// Extra: the 10 playlists in NFSGame.sb that no PlaylistSet references.
var Extra = []Playlist{
	{"European Elite", "_c4/gameplay/playlist/nfs12career/playlists/playlist_european", "FE8A591E-47BF-4F81-B278-1E7E238624B5"},
	{"All American", "_c4/gameplay/playlist/nfs12career/playlists/playlist_allamerican", "9E6A682E-BBF5-4376-8DE0-F968478B8343"},
	{"Japan", "_c4/gameplay/playlist/nfs12career/playlists/playlist_japan", "BD970881-06F7-4A6C-96A2-0C2050FF02F3"},
	{"Nissan Showdown", "_c4/gameplay/playlist/nfs12career/playlists/playlist_nissan", "4AB933B4-9F7A-4CB4-9B0E-F789DB5E19EA"},
	{"Porsche Unleashed", "_c4/gameplay/playlist/nfs12career/playlists/playlist_porscheunleashed", "0C339FF1-DA88-4548-B90C-5063ECDA78FA"},
	{"Classics", "_c4/gameplay/playlist/nfs12career/playlists/playlist_classics", "AEAC6F79-E98A-44A6-AA69-EFA2614415FB"},
	{"Insomnia", "_c4/gameplay/playlist/nfs12career/playlists/playlist_insomnia", "3B46C82B-8F65-476E-8451-9DD5D5905E98"},
	{"AWD", "_c4/gameplay/playlist/nfs12career/playlists/playlist_awd", "BC4C430F-8990-498E-8F74-CB068183B544"},
	{"Ford", "_c4/gameplay/playlist/nfs12career/playlists/playlist_ford", "4803206B-D393-4556-A361-109198C620D1"},
	{"Lamborghini Untamed", "_c4/gameplay/playlist/nfs12career/playlists/playlist_lamborghini", "8BAE3D69-689F-48E8-AD52-0C3B674F31BE"},
}

const (
	ebxMagic       = 0x0FB2D1CE
	bundleMagic    = 0x970D1C13
	playlistSetAss = "_c4/gameplay/playlist/playlistset"
)

// ---------------------------------------------------------------- bundle

type entry struct {
	name           string
	size, origSize uint32
	tableOff       int // offset of the 12-byte entry in the header
	dataOff        int
}

type bundle struct {
	d        []byte
	total    int
	ebx      []entry
	dataEnd  int // end of the ebx data
	shaStart int
}

func be32(b []byte, o int) uint32 { return binary.BigEndian.Uint32(b[o:]) }

func parseBundle(d []byte) (*bundle, error) {
	if len(d) < 0x24 || be32(d, 4) != bundleMagic {
		return nil, errors.New("not an NFS The Run bundle (.sb)")
	}
	hs := int(be32(d, 0))
	tot, ne := int(be32(d, 8)), int(be32(d, 12))
	so := int(be32(d, 24))
	b := &bundle{d: d, total: tot, shaStart: 0x24}
	tab := 0x24 + 20*tot
	strs := 4 + so
	off := 4 + hs
	if tab+12*ne > len(d) || off > len(d) {
		return nil, errors.New("bundle header out of range")
	}
	for i := 0; i < ne; i++ {
		t := tab + 12*i
		no := strs + int(be32(d, t))
		e := bytes.IndexByte(d[no:], 0)
		if e < 0 {
			return nil, errors.New("bad name table")
		}
		en := entry{name: string(d[no : no+e]), size: be32(d, t+4), origSize: be32(d, t+8), tableOff: t, dataOff: off}
		if en.size != en.origSize {
			return nil, fmt.Errorf("ebx %s is compressed; unexpected", en.name)
		}
		off += int(en.size)
		if off > len(d) {
			return nil, errors.New("ebx data out of range")
		}
		b.ebx = append(b.ebx, en)
	}
	b.dataEnd = off
	return b, nil
}

func (b *bundle) find(name string) (int, bool) {
	for i, e := range b.ebx {
		if strings.EqualFold(e.name, name) {
			return i, true
		}
	}
	return 0, false
}

func (b *bundle) data(i int) []byte {
	e := b.ebx[i]
	return b.d[e.dataOff : e.dataOff+int(e.size)]
}

// ---------------------------------------------------------------- ebx

type ebxFile struct {
	d                 []byte
	h                 [12]uint32 // magic absStr lenStrEOF numGUID numGUIDRep numInstRep numComplex numField lenName lenString numArrRep lenPayload
	fileGUID, primary GUID
	ext               [][2]GUID
	afterExt          int // start of the name table
	fieldsOff         int
	complexOff        int
	arrRepOff         int
	fields            []ebxField
	complex           []ebxComplex
	instRep           [][3]uint32
	arrRep            [][3]uint32 // offset, repetitions, complex
}

type ebxField struct {
	hash   uint32
	typ    uint16
	ref    uint16
	offset uint32
}

type ebxComplex struct {
	hash        uint32
	fieldStart  uint32
	numField    uint8
	align       uint8
	typ, size   uint16
	secondarySz uint16
}

func le32(b []byte, o int) uint32 { return binary.LittleEndian.Uint32(b[o:]) }

func djb(s string) uint32 {
	h := uint32(5381)
	for i := 0; i < len(s); i++ {
		h = (h * 33) ^ uint32(s[i])
	}
	return h
}

func parseEbx(d []byte) (*ebxFile, error) {
	if len(d) < 0x50 || le32(d, 0) != ebxMagic {
		return nil, errors.New("not an ebx")
	}
	e := &ebxFile{d: d}
	for i := range e.h {
		e.h[i] = le32(d, 4*i)
	}
	if int(e.h[1]+e.h[2]) != len(d) {
		return nil, fmt.Errorf("ebx size %d, header says %d", len(d), e.h[1]+e.h[2])
	}
	copy(e.fileGUID[:], d[0x30:0x40])
	copy(e.primary[:], d[0x40:0x50])
	o := 0x50
	for i := 0; i < int(e.h[3]); i++ {
		var p [2]GUID
		copy(p[0][:], d[o:o+16])
		copy(p[1][:], d[o+16:o+32])
		e.ext = append(e.ext, p)
		o += 32
	}
	e.afterExt = o
	o += int(e.h[8])
	e.fieldsOff = o
	for i := 0; i < int(e.h[7]); i++ {
		e.fields = append(e.fields, ebxField{le32(d, o), binary.LittleEndian.Uint16(d[o+4:]), binary.LittleEndian.Uint16(d[o+6:]), le32(d, o+8)})
		o += 16
	}
	e.complexOff = o
	for i := 0; i < int(e.h[6]); i++ {
		e.complex = append(e.complex, ebxComplex{le32(d, o), le32(d, o+4), d[o+8], d[o+9],
			binary.LittleEndian.Uint16(d[o+10:]), binary.LittleEndian.Uint16(d[o+12:]), binary.LittleEndian.Uint16(d[o+14:])})
		o += 16
	}
	for i := 0; i < int(e.h[5]); i++ {
		e.instRep = append(e.instRep, [3]uint32{le32(d, o), le32(d, o+4), le32(d, o+8)})
		o += 12
	}
	for o%16 != 0 {
		o++
	}
	e.arrRepOff = o
	for i := 0; i < int(e.h[10]); i++ {
		e.arrRep = append(e.arrRep, [3]uint32{le32(d, o), le32(d, o+4), le32(d, o+8)})
		o += 12
	}
	if o > int(e.h[1]) {
		return nil, errors.New("ebx tables overlap the strings")
	}
	return e, nil
}

func (e *ebxFile) payload() int { return int(e.h[1] + e.h[9]) }
func (e *ebxFile) arrays() int  { return e.payload() + int(e.h[11]) }

// fieldOffset: offset of a named field inside complex ci (searching bases).
func (e *ebxFile) fieldOffset(ci int, name string) (int, bool) {
	want := djb(name)
	c := e.complex[ci]
	for fi := int(c.fieldStart); fi < int(c.fieldStart)+int(c.numField); fi++ {
		f := e.fields[fi]
		if f.hash == want {
			return int(f.offset), true
		}
	}
	return 0, false
}

func (e *ebxFile) complexIndex(name string) (int, bool) {
	want := djb(name)
	for i, c := range e.complex {
		if c.hash == want {
			return i, true
		}
	}
	return 0, false
}

// mainPlaylistArray returns the arrRep index of PlaylistArrays[0].Playlists.
func (e *ebxFile) mainPlaylistArray() (int, error) {
	ci, ok := e.complexIndex("PlaylistSet")
	if !ok || len(e.instRep) == 0 || int(e.instRep[0][2]) != ci {
		return 0, errors.New("the first instance is not a PlaylistSet")
	}
	fo, ok := e.fieldOffset(ci, "PlaylistArrays")
	if !ok {
		return 0, errors.New("PlaylistSet has no PlaylistArrays")
	}
	inst := e.payload() + 16 // the primary instance is preceded by its GUID
	ai := int(le32(e.d, inst+fo))
	if ai >= len(e.arrRep) || e.arrRep[ai][1] == 0 {
		return 0, errors.New("PlaylistArrays is empty")
	}
	elem := e.arrays() + int(e.arrRep[ai][0]) // PlaylistArrays[0]: struct PlaylistArray{Playlists}
	pa, ok := e.complexIndex("PlaylistArray")
	if !ok {
		return 0, errors.New("no PlaylistArray type")
	}
	po, ok := e.fieldOffset(pa, "Playlists")
	if !ok {
		return 0, errors.New("PlaylistArray has no Playlists")
	}
	pi := int(le32(e.d, elem+po))
	if pi >= len(e.arrRep) {
		return 0, errors.New("bad Playlists array index")
	}
	return pi, nil
}

// refs of array ai (4-byte class references).
func (e *ebxFile) refs(ai int) []uint32 {
	a := e.arrRep[ai]
	var out []uint32
	for i := 0; i < int(a[1]); i++ {
		out = append(out, le32(e.d, e.arrays()+int(a[0])+4*i))
	}
	return out
}

// PlaylistsInSet lists the instance GUIDs of every playlist in the set
// (all PlaylistArrays), and of PlaylistArrays[0] separately.
func playlistsInSet(e *ebxFile) (all, main []GUID, err error) {
	mi, err := e.mainPlaylistArray()
	if err != nil {
		return nil, nil, err
	}
	pa, _ := e.complexIndex("PlaylistArray")
	for ai, a := range e.arrRep {
		if int(a[2]) == pa || a[1] == 0 {
			continue
		}
		c := e.complex[a[2]]
		if c.size != 4 {
			continue
		}
		for _, r := range e.refs(ai) {
			if r>>31 == 1 && int(r&0x7fffffff) < len(e.ext) {
				g := e.ext[r&0x7fffffff][1]
				all = append(all, g)
				if ai == mi {
					main = append(main, g)
				}
			}
		}
	}
	return all, main, nil
}

// addToMain returns a new ebx with imports appended to PlaylistArrays[0].
func addToMain(e *ebxFile, imports [][2]GUID) ([]byte, error) {
	mi, err := e.mainPlaylistArray()
	if err != nil {
		return nil, err
	}
	n := len(imports)
	grow := 32 * n
	// new array section: every non-empty array laid out again in offset
	// order as [count u32][elements], 4-byte aligned (the game's own layout)
	type arr struct {
		idx  int
		data []byte
	}
	order := make([]int, 0, len(e.arrRep))
	for i := range e.arrRep {
		order = append(order, i)
	}
	for i := 1; i < len(order); i++ { // insertion sort by offset
		for j := i; j > 0 && e.arrRep[order[j]][0] < e.arrRep[order[j-1]][0]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	newRep := make([][3]uint32, len(e.arrRep))
	copy(newRep, e.arrRep)
	var sec bytes.Buffer
	base := e.arrays()
	for _, ai := range order {
		a := e.arrRep[ai]
		if a[1] == 0 && ai != mi {
			newRep[ai] = [3]uint32{0, 0, a[2]}
			continue
		}
		esz := int(e.complex[a[2]].size)
		elems := append([]byte(nil), e.d[base+int(a[0]):base+int(a[0])+esz*int(a[1])]...)
		count := a[1]
		if ai == mi {
			if esz != 4 {
				return nil, errors.New("Playlists elements are not references")
			}
			for k := 0; k < n; k++ {
				var r [4]byte
				binary.LittleEndian.PutUint32(r[:], 0x80000000|uint32(len(e.ext)+k))
				elems = append(elems, r[:]...)
			}
			count += uint32(n)
		}
		for sec.Len()%4 != 0 {
			sec.WriteByte(0)
		}
		var c [4]byte
		binary.LittleEndian.PutUint32(c[:], count)
		sec.Write(c[:])
		newRep[ai] = [3]uint32{uint32(sec.Len()), count, a[2]}
		sec.Write(elems)
	}
	for sec.Len()%16 != 0 {
		sec.WriteByte(0)
	}
	// assemble
	var out bytes.Buffer
	h := e.h
	h[1] += uint32(grow)
	h[3] += uint32(n)
	h[2] = h[9] + h[11] + uint32(sec.Len())
	for _, v := range h {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		out.Write(b[:])
	}
	out.Write(e.d[0x30:e.afterExt])
	for _, p := range imports {
		out.Write(p[0][:])
		out.Write(p[1][:])
	}
	out.Write(e.d[e.afterExt:e.arrRepOff])
	for _, a := range newRep {
		for _, v := range a {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], v)
			out.Write(b[:])
		}
	}
	out.Write(e.d[e.arrRepOff+12*len(e.arrRep) : e.arrays()]) // padding, strings, payload
	out.Write(sec.Bytes())
	if out.Len() != int(h[1]+h[2]) {
		return nil, fmt.Errorf("internal: ebx %d bytes, header %d", out.Len(), h[1]+h[2])
	}
	return out.Bytes(), nil
}

// ---------------------------------------------------------------- toc

const tocKeyOff, tocBody = 0x128, 0x22c

func tocDecode(t []byte) (body []byte, encoded bool) {
	if len(t) >= tocBody && binary.LittleEndian.Uint32(t) == 0x00CED100 {
		key := t[tocKeyOff : tocKeyOff+257]
		body = make([]byte, len(t)-tocBody)
		for i := range body {
			body[i] = key[i%257] ^ t[tocBody+i] ^ 0x7b
		}
		return body, true
	}
	return append([]byte(nil), t...), false
}

func tocEncode(orig, body []byte, encoded bool) []byte {
	if !encoded {
		return body
	}
	out := append([]byte(nil), orig[:tocBody]...)
	key := orig[tocKeyOff : tocKeyOff+257]
	for i, c := range body {
		out = append(out, key[i%257]^c^0x7b)
	}
	return out
}

// tocSizeField finds the "size" of the only bundle entry: int64 (type 0x09,
// base data) or int32 (type 0x08, Update/Patch data). Returns its offset and
// width.
func tocSizeField(body []byte) (int, int, error) {
	for _, k := range []struct {
		key   string
		width int
	}{{"\x09size\x00", 8}, {"\x08size\x00", 4}} {
		i := bytes.Index(body, []byte(k.key))
		if i < 0 {
			continue
		}
		if bytes.Index(body[i+1:], []byte(k.key)) >= 0 {
			return 0, 0, errors.New("NFSGame.toc: more than one bundle")
		}
		return i + len(k.key), k.width, nil
	}
	return 0, 0, errors.New("NFSGame.toc: no bundle size")
}

func getSize(body []byte, o, w int) uint64 {
	if w == 8 {
		return binary.LittleEndian.Uint64(body[o:])
	}
	return uint64(binary.LittleEndian.Uint32(body[o:]))
}

// tocDelta: offset of the "delta" bool of the bundle entry (-1: none).
func tocDelta(body []byte) int {
	i := bytes.Index(body, []byte("\x06delta\x00"))
	if i < 0 {
		return -1
	}
	return i + 7
}

// tocSetBundle checks the bundle size (wantOld) and writes newSize; with
// clearDelta the bundle stops being a delta of the base data.
func tocSetBundle(toc []byte, wantOld, newSize int, clearDelta bool) ([]byte, error) {
	body, enc := tocDecode(toc)
	so, w, err := tocSizeField(body)
	if err != nil {
		return nil, err
	}
	if got := getSize(body, so, w); got != uint64(wantOld) {
		return nil, fmt.Errorf("NFSGame.toc says the bundle is %d bytes, NFSGame.sb is %d", got, wantOld)
	}
	if w == 4 {
		if newSize > 0x7fffffff {
			return nil, errors.New("bundle too large")
		}
		binary.LittleEndian.PutUint32(body[so:], uint32(newSize))
	} else {
		binary.LittleEndian.PutUint64(body[so:], uint64(newSize))
	}
	if clearDelta {
		if d := tocDelta(body); d >= 0 {
			body[d] = 0
		}
	}
	return tocEncode(toc, body, enc), nil
}

// IsDeltaTOC: is this toc's bundle a delta of the base data (Update/Patch)?
func IsDeltaTOC(toc []byte) bool {
	body, _ := tocDecode(toc)
	d := tocDelta(body)
	return d >= 0 && d < len(body) && body[d] != 0
}

// ApplyDelta rebuilds the full bundle from an Update/Patch delta bundle
// (FileSuperBundleManager delta runs; PC BufferMultiplexer read 0x00512F30):
// header {u32 runTableSize, u32 magic, u64}, then runTableSize/16 runs
// {u32 size, i32 source, u64 offset} (big endian), then the delta's own data.
// source 0: next bytes of the delta data (sequential); 1: the base bundle at
// offset; -1: Update/Patch/Data/common.dat at offset.
func ApplyDelta(delta, base, common []byte) ([]byte, error) {
	if len(delta) < 16 {
		return nil, errors.New("delta bundle too short")
	}
	rt := int(be32(delta, 0))
	if rt%16 != 0 || 16+rt > len(delta) {
		return nil, errors.New("bad delta run table")
	}
	pd := 16 + rt
	var out bytes.Buffer
	for o := 16; o < 16+rt; o += 16 {
		sz := int(be32(delta, o))
		src := int32(be32(delta, o+4))
		off := int(binary.BigEndian.Uint64(delta[o+8:]))
		var from []byte
		switch src {
		case 0:
			if pd+sz > len(delta) {
				return nil, errors.New("delta data out of range")
			}
			from = delta[pd : pd+sz]
			pd += sz
		case 1:
			if off+sz > len(base) {
				return nil, errors.New("delta refers past the end of the base bundle (base data already modified?)")
			}
			from = base[off : off+sz]
		case -1:
			if common == nil || off+sz > len(common) {
				return nil, errors.New("delta refers to common.dat, which is missing or too short")
			}
			from = common[off : off+sz]
		default:
			return nil, fmt.Errorf("unknown delta source %d", src)
		}
		out.Write(from)
	}
	if pd != len(delta) {
		return nil, fmt.Errorf("delta data: %d of %d bytes used", pd, len(delta))
	}
	return out.Bytes(), nil
}

// ---------------------------------------------------------------- API

// Status describes the playlists in a NFSGame.sb.
type Status struct {
	Main    []string // instance GUIDs in PlaylistArrays[0]
	Missing []Playlist
}

// Inspect reports which extra playlists are not in the set yet.
func Inspect(sb []byte) (Status, error) {
	var st Status
	b, err := parseBundle(sb)
	if err != nil {
		return st, err
	}
	i, ok := b.find(playlistSetAss)
	if !ok {
		return st, errors.New("NFSGame.sb has no " + playlistSetAss)
	}
	e, err := parseEbx(b.data(i))
	if err != nil {
		return st, err
	}
	all, main, err := playlistsInSet(e)
	if err != nil {
		return st, err
	}
	in := map[GUID]bool{}
	for _, g := range all {
		in[g] = true
	}
	for _, g := range main {
		st.Main = append(st.Main, g.String())
	}
	for _, p := range Extra {
		g, _ := ParseGUID(p.Instance)
		if !in[g] {
			st.Missing = append(st.Missing, p)
		}
	}
	return st, nil
}

// patchBundle adds the missing extra playlists to the PlaylistSet of a full
// (non-delta) bundle. added == nil: nothing to do, sb returned unchanged.
func patchBundle(sb []byte) (newSB []byte, added []string, err error) {
	b, err := parseBundle(sb)
	if err != nil {
		return nil, nil, err
	}
	si, ok := b.find(playlistSetAss)
	if !ok {
		return nil, nil, errors.New("NFSGame.sb has no " + playlistSetAss)
	}
	e, err := parseEbx(b.data(si))
	if err != nil {
		return nil, nil, err
	}
	all, _, err := playlistsInSet(e)
	if err != nil {
		return nil, nil, err
	}
	in := map[GUID]bool{}
	for _, g := range all {
		in[g] = true
	}
	var imports [][2]GUID
	for _, p := range Extra {
		g, _ := ParseGUID(p.Instance)
		if in[g] {
			continue
		}
		pi, ok := b.find(p.Path)
		if !ok {
			return nil, nil, fmt.Errorf("NFSGame.sb has no %s", p.Path)
		}
		pe, err := parseEbx(b.data(pi))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %v", p.Path, err)
		}
		if pe.primary != g {
			return nil, nil, fmt.Errorf("%s: instance %s, expected %s", p.Path, pe.primary, g)
		}
		if ci, ok := pe.complexIndex("Playlist"); !ok || int(pe.instRep[0][2]) != ci {
			return nil, nil, fmt.Errorf("%s is not a Playlist", p.Path)
		}
		imports = append(imports, [2]GUID{pe.fileGUID, pe.primary})
		added = append(added, p.Name)
	}
	if len(imports) == 0 {
		return sb, nil, nil
	}
	ne, err := addToMain(e, imports)
	if err != nil {
		return nil, nil, err
	}
	en := b.ebx[si]
	var out bytes.Buffer
	out.Grow(len(sb) + len(ne))
	out.Write(sb[:en.dataOff])
	out.Write(ne)
	out.Write(sb[en.dataOff+int(en.size):])
	newSB = out.Bytes()
	binary.BigEndian.PutUint32(newSB[en.tableOff+4:], uint32(len(ne)))
	binary.BigEndian.PutUint32(newSB[en.tableOff+8:], uint32(len(ne)))
	sum := sha1.Sum(ne)
	copy(newSB[b.shaStart+20*si:], sum[:]) // SHA-1 table: ebx first, same order
	return newSB, added, nil
}

// PatchDelta: the Update\Patch case. full is the bundle the delta builds
// (ApplyDelta); the delta keeps its runs, only the header bytes and the
// PlaylistSet change (see patchDelta). Returns the new delta and toc.
func PatchDelta(delta, toc, full, base, common []byte) (newDelta, newTOC []byte, added []string, err error) {
	nsb, added, err := patchBundle(full)
	if err != nil || added == nil {
		return delta, toc, nil, err
	}
	b, _ := parseBundle(full)
	si, _ := b.find(playlistSetAss)
	en := b.ebx[si]
	grow := len(nsb) - len(full)
	repl := nsb[en.dataOff : en.dataOff+int(en.size)+grow]
	hdr := b.ebx[0].dataOff // header = everything before the first entry
	newDelta, err = patchDelta(delta, full, nsb, hdr, en.dataOff, en.dataOff+int(en.size), repl)
	if err != nil {
		return nil, nil, nil, err
	}
	rebuilt, err := ApplyDelta(newDelta, base, common)
	if err != nil {
		return nil, nil, nil, err
	}
	if !bytes.Equal(rebuilt, nsb) {
		return nil, nil, nil, errors.New("internal: the new delta does not rebuild the patched bundle")
	}
	newTOC, err = tocSetBundle(toc, len(delta), len(newDelta), false)
	if err != nil {
		return nil, nil, nil, err
	}
	return newDelta, newTOC, added, nil
}

// Patch returns new NFSGame.sb / NFSGame.toc bytes (full bundle, no Update
// delta) with the missing extra playlists added to the menu set. added ==
// nil: nothing to do, inputs returned unchanged.
func Patch(sb, toc []byte) (newSB, newTOC []byte, added []string, err error) {
	newSB, added, err = patchBundle(sb)
	if err != nil || added == nil {
		return sb, toc, nil, err
	}
	newTOC, err = tocSetBundle(toc, len(sb), len(newSB), false)
	if err != nil {
		return nil, nil, nil, err
	}
	return newSB, newTOC, added, nil
}

// Verify parses a patched pair again: the set must contain every extra
// playlist and the toc must match the bundle size.
func Verify(sb, toc []byte) error {
	st, err := Inspect(sb)
	if err != nil {
		return err
	}
	if len(st.Missing) > 0 {
		return fmt.Errorf("%d playlist(s) still missing", len(st.Missing))
	}
	body, _ := tocDecode(toc)
	so, w, err := tocSizeField(body)
	if err != nil {
		return err
	}
	if got := getSize(body, so, w); got != uint64(len(sb)) {
		return fmt.Errorf("toc size %d, bundle %d", got, len(sb))
	}
	if IsDeltaTOC(toc) {
		return errors.New("toc still marks the bundle as a delta")
	}
	b, err := parseBundle(sb)
	if err != nil {
		return err
	}
	if b.dataEnd > len(sb) {
		return errors.New("bundle data out of range")
	}
	for i := range b.ebx {
		if sha1.Sum(b.data(i)) != *(*[20]byte)(sb[b.shaStart+20*i:]) {
			return fmt.Errorf("SHA-1 of %s does not match", b.ebx[i].name)
		}
	}
	return nil
}

func sha1Of(b []byte) [20]byte { return sha1.Sum(b) }
