package playlistpatch

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RelDir: where NFSGame.sb / .toc live inside the game folder.
var RelDir = filepath.Join("Data", "Win32", "_c4", "GameConfigurations")

// UpdateRelDir / CommonRel: the 1.1 patch. Its NFSGame bundle is a delta of
// the base one (runs copying base ranges by absolute offset, plus its own data
// and common.dat), so the base file must never change size.
var (
	UpdateRelDir = filepath.Join("Update", "Patch", "Data", "Win32", "_c4", "GameConfigurations")
	CommonRel    = filepath.Join("Update", "Patch", "Data", "common.dat")
)

// IsExtra: is this playListId one of the 10 added playlists?
func IsExtra(id string) bool {
	id = strings.ToUpper(strings.TrimSpace(id))
	for _, p := range Extra {
		if p.Instance == id {
			return true
		}
	}
	return false
}

// Result of Ensure.
type Result struct {
	Changed bool
	Added   []string // playlists added now
	Note    string   // what happened, for the log
}

type pair struct{ sb, toc string }

func (p pair) read() (sb, toc []byte, err error) {
	if sb, err = os.ReadFile(p.sb); err != nil {
		return
	}
	toc, err = os.ReadFile(p.toc)
	return
}

func (p pair) hasOrig() bool {
	_, e1 := os.Stat(p.sb + ".orig")
	_, e2 := os.Stat(p.toc + ".orig")
	return e1 == nil && e2 == nil
}

// restore puts the .orig files back (they stay as backups).
func (p pair) restore() error {
	sb, err := os.ReadFile(p.sb + ".orig")
	if err != nil {
		return err
	}
	toc, err := os.ReadFile(p.toc + ".orig")
	if err != nil {
		return err
	}
	if err := replace(p.sb, sb); err != nil {
		return err
	}
	return replace(p.toc, toc)
}

// original returns the unmodified files (the .orig backups when present).
func (p pair) original() (sb, toc []byte, err error) {
	if p.hasOrig() {
		if sb, err = os.ReadFile(p.sb + ".orig"); err != nil {
			return
		}
		toc, err = os.ReadFile(p.toc + ".orig")
		return
	}
	return p.read()
}

// write keeps the first backup and replaces the pair.
func (p pair) write(origSB, origTOC, sb, toc []byte) error {
	for _, f := range []struct {
		path string
		data []byte
	}{{p.sb + ".orig", origSB}, {p.toc + ".orig", origTOC}} {
		if _, err := os.Stat(f.path); err == nil {
			continue
		}
		if err := os.WriteFile(f.path, f.data, 0644); err != nil {
			return fmt.Errorf("cannot back up %s: %w", filepath.Base(f.path), err)
		}
	}
	if err := replace(p.sb, sb); err != nil {
		return err
	}
	if err := replace(p.toc, toc); err != nil {
		_ = replace(p.sb, origSB)
		return err
	}
	return nil
}

// Ensure adds the 10 extra playlists to the game folder's NFSGame data (on)
// or restores the original files (off).
//
// With an Update\Patch NFSGame (retail 1.1) only the delta is patched and the
// base stays untouched; otherwise the base bundle is patched. Originals are
// kept once as .orig.
func Ensure(gameDir string, on bool) (Result, error) {
	base := pair{filepath.Join(gameDir, RelDir, "NFSGame.sb"), filepath.Join(gameDir, RelDir, "NFSGame.toc")}
	upd := pair{filepath.Join(gameDir, UpdateRelDir, "NFSGame.sb"), filepath.Join(gameDir, UpdateRelDir, "NFSGame.toc")}
	_, errU := os.Stat(upd.toc)
	hasUpdate := errU == nil

	var notes []string
	changed := false
	// a patched base file breaks the Update delta: restore it
	if hasUpdate || !on {
		if bsb, _, err := base.read(); err == nil {
			if st, err := Inspect(bsb); err == nil && len(st.Missing) == 0 && base.hasOrig() {
				if err := base.restore(); err != nil {
					return Result{}, fmt.Errorf("cannot restore the original NFSGame.sb: %w", err)
				}
				changed = true
				notes = append(notes, "original base NFSGame.sb/.toc restored")
			}
		}
	}
	target := base
	if hasUpdate {
		target = upd
	}
	if !on {
		if target == upd && upd.hasOrig() {
			cur, _, err := upd.read()
			orig, _ := os.ReadFile(upd.sb + ".orig")
			if err == nil && !bytes.Equal(cur, orig) {
				if err := upd.restore(); err != nil {
					return Result{}, err
				}
				changed = true
				notes = append(notes, "original Update NFSGame.sb/.toc restored")
			}
		}
		notes = append(notes, "original playlist set (EXTRA_PLAYLISTS off)")
		return Result{Changed: changed, Note: strings.Join(notes, "; ")}, nil
	}

	sb, toc, err := target.read()
	if err != nil {
		return Result{}, err
	}
	var nsb, ntoc []byte
	var added []string
	if IsDeltaTOC(toc) {
		bsb, _, err := base.original()
		if err != nil {
			return Result{}, err
		}
		common, err := os.ReadFile(filepath.Join(gameDir, CommonRel))
		if err != nil {
			return Result{}, fmt.Errorf("the 1.1 patch data is incomplete: %w", err)
		}
		full, err := ApplyDelta(sb, bsb, common)
		if err != nil {
			return Result{}, fmt.Errorf("Update\\Patch NFSGame.sb: %w", err)
		}
		if err := verifyBundle(full); err != nil {
			return Result{}, fmt.Errorf("the NFSGame bundle the game builds does not verify (%v); nothing written", err)
		}
		if st, err := Inspect(full); err == nil && len(st.Missing) == 0 {
			notes = append(notes, fmt.Sprintf("%d playlists in the menu set (already patched)", len(st.Main)))
			return Result{Changed: changed, Note: strings.Join(notes, "; ")}, nil
		}
		if nsb, ntoc, added, err = PatchDelta(sb, toc, full, bsb, common); err != nil {
			return Result{}, err
		}
		rebuilt, _ := ApplyDelta(nsb, bsb, common)
		if st, err := Inspect(rebuilt); err != nil || len(st.Missing) > 0 {
			return Result{}, fmt.Errorf("patched delta did not verify (%v); nothing written", err)
		}
		if err := verifyBundle(rebuilt); err != nil {
			return Result{}, fmt.Errorf("patched delta did not verify (%v); nothing written", err)
		}
	} else {
		if st, err := Inspect(sb); err == nil && len(st.Missing) == 0 {
			notes = append(notes, fmt.Sprintf("%d playlists in the menu set (already patched)", len(st.Main)))
			return Result{Changed: changed, Note: strings.Join(notes, "; ")}, nil
		}
		if nsb, ntoc, added, err = Patch(sb, toc); err != nil {
			return Result{}, err
		}
		if err := Verify(nsb, ntoc); err != nil {
			return Result{}, fmt.Errorf("patched data did not verify, nothing written: %w", err)
		}
	}
	if err := target.write(sb, toc, nsb, ntoc); err != nil {
		return Result{}, err
	}
	where := "NFSGame.sb"
	if target == upd {
		where = "the 1.1 data (Update\\Patch NFSGame delta)"
	}
	notes = append(notes, fmt.Sprintf("added %d playlists to the menu set in %s (%s); originals kept as .orig", len(added), where, strings.Join(added, ", ")))
	return Result{Changed: true, Added: added, Note: strings.Join(notes, "; ")}, nil
}

// verifyBundle: every ebx SHA-1 matches its data.
func verifyBundle(sb []byte) error {
	b, err := parseBundle(sb)
	if err != nil {
		return err
	}
	for i := range b.ebx {
		if sha1Of(b.data(i)) != *(*[20]byte)(sb[b.shaStart+20*i:]) {
			return fmt.Errorf("SHA-1 of %s does not match", b.ebx[i].name)
		}
	}
	return nil
}

func replace(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	_ = os.Chmod(path, 0644) // game files may be read-only
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if err2 := os.Rename(tmp, path); err2 != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return nil
}
