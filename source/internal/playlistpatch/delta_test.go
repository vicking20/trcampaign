package playlistpatch

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The retail 1.1 Update delta (no common.dat here: the 81 entries it supplies
// are zeros, both before and after, so the comparison still holds).
func TestPatchRealDelta(t *testing.T) {
	delta, err := os.ReadFile(filepath.Join(realUpd, "NFSGame.sb"))
	if err != nil {
		t.Skip("update data not here")
	}
	toc, _ := os.ReadFile(filepath.Join(realUpd, "NFSGame.toc"))
	base, err := os.ReadFile(filepath.Join(realDir, "NFSGame.sb"))
	if err != nil {
		t.Skip("game data not here")
	}
	if !IsDeltaTOC(toc) {
		t.Fatal("retail update toc should be a delta")
	}
	common := make([]byte, 4<<20)
	if c, err := os.ReadFile(filepath.Join(realUpd, "../../../common.dat")); err == nil {
		common = c
	}
	full, err := ApplyDelta(delta, base, common)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Inspect(full)
	if err != nil || len(st.Main) != 6 || len(st.Missing) != 10 {
		t.Fatal(st, err)
	}
	nd, nt, added, err := PatchDelta(delta, toc, full, base, common)
	if err != nil || len(added) != 10 {
		t.Fatal(added, err)
	}
	if !IsDeltaTOC(nt) {
		t.Fatal("must stay a delta")
	}
	body, _ := tocDecode(nt)
	so, w, _ := tocSizeField(body)
	if w != 4 || int(binary.LittleEndian.Uint32(body[so:])) != len(nd) {
		t.Fatal("toc size")
	}
	rebuilt, err := ApplyDelta(nd, base, common)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := Inspect(rebuilt); len(st.Main) != 16 {
		t.Fatal("not 16", st)
	}
	if len(rebuilt) != len(full)+352 || len(nd) != len(delta)+32+1552 {
		t.Fatalf("sizes: bundle %d->%d, delta %d->%d", len(full), len(rebuilt), len(delta), len(nd))
	}
}

// Ensure on an install with an Update delta, starting from a patched base file
// state (base patched). A synthetic delta (header as delta data, the rest a
// base run) stands in for the retail one, so no common.dat is needed.
func TestEnsureWithUpdate(t *testing.T) {
	base, err := os.ReadFile(filepath.Join(realDir, "NFSGame.sb"))
	if err != nil {
		t.Skip("game data not here")
	}
	btoc, _ := os.ReadFile(filepath.Join(realDir, "NFSGame.toc"))
	utoc, err := os.ReadFile(filepath.Join(realUpd, "NFSGame.toc"))
	if err != nil {
		t.Skip("update data not here")
	}
	b, _ := parseBundle(base)
	hdr := b.ebx[0].dataOff
	delta := writeRuns(make([]byte, 16), []run{{size: hdr, src: 0, data: base[:hdr]}, {size: len(base) - hdr, src: 1, off: hdr}})
	copy(delta[4:8], []byte{0xde, 0x17, 0xaa, 0xaa})
	utoc2, err := tocSetBundle(utoc, int(func() uint64 { bd, _ := tocDecode(utoc); o, w, _ := tocSizeField(bd); return getSize(bd, o, w) }()), len(delta), false)
	if err != nil {
		t.Fatal(err)
	}
	game := t.TempDir()
	bd, ud := filepath.Join(game, RelDir), filepath.Join(game, UpdateRelDir)
	os.MkdirAll(bd, 0755)
	os.MkdirAll(ud, 0755)
	// base patched, originals as .orig
	psb, ptoc, _, _ := Patch(base, btoc)
	os.WriteFile(filepath.Join(bd, "NFSGame.sb"), psb, 0644)
	os.WriteFile(filepath.Join(bd, "NFSGame.toc"), ptoc, 0644)
	os.WriteFile(filepath.Join(bd, "NFSGame.sb.orig"), base, 0644)
	os.WriteFile(filepath.Join(bd, "NFSGame.toc.orig"), btoc, 0644)
	os.WriteFile(filepath.Join(ud, "NFSGame.sb"), delta, 0644)
	os.WriteFile(filepath.Join(ud, "NFSGame.toc"), utoc2, 0644)
	os.WriteFile(filepath.Join(game, CommonRel), []byte{0}, 0644)

	r, err := Ensure(game, true)
	if err != nil || !r.Changed || len(r.Added) != 10 {
		t.Fatal(r, err)
	}
	now, _ := os.ReadFile(filepath.Join(bd, "NFSGame.sb"))
	if string(now) != string(base) {
		t.Fatal("base not restored")
	}
	nd, _ := os.ReadFile(filepath.Join(ud, "NFSGame.sb"))
	full, err := ApplyDelta(nd, base, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := Inspect(full); len(st.Main) != 16 {
		t.Fatal("update not patched", st)
	}
	if r, err := Ensure(game, true); err != nil || r.Changed {
		t.Fatal("second run", r, err)
	}
	if r, err := Ensure(game, false); err != nil || !r.Changed {
		t.Fatal("off", r, err)
	}
	back, _ := os.ReadFile(filepath.Join(ud, "NFSGame.sb"))
	if string(back) != string(delta) {
		t.Fatal("update not restored")
	}
}
