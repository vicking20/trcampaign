package playlistpatch

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Tests against the real game data run when NFSTR_GAME_DIR names the game
// folder; otherwise they are skipped.
var (
	realDir = gameData(RelDir)
	realUpd = gameData(UpdateRelDir)
)

func gameData(rel string) string {
	if d := os.Getenv("NFSTR_GAME_DIR"); d != "" {
		return filepath.Join(d, rel)
	}
	return filepath.Join(os.TempDir(), "nfstr-game-data-not-set")
}

func TestPatchRealNFSGame(t *testing.T) {
	sb, err := os.ReadFile(filepath.Join(realDir, "NFSGame.sb"))
	if err != nil {
		t.Skip("game data not here")
	}
	toc, _ := os.ReadFile(filepath.Join(realDir, "NFSGame.toc"))
	st, err := Inspect(sb)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Main) != 6 || len(st.Missing) != 10 {
		t.Fatalf("retail set: %d in menu, %d missing", len(st.Main), len(st.Missing))
	}
	nsb, ntoc, added, err := Patch(sb, toc)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 10 || len(nsb) != len(sb)+352 {
		t.Fatalf("added %v, size %d -> %d", added, len(sb), len(nsb))
	}
	if err := Verify(nsb, ntoc); err != nil {
		t.Fatal(err)
	}
	st, _ = Inspect(nsb)
	if len(st.Main) != 16 {
		t.Fatalf("menu set now %d", len(st.Main))
	}
	// idempotent
	a, b, again, err := Patch(nsb, ntoc)
	if err != nil || again != nil || len(a) != len(nsb) || len(b) != len(ntoc) {
		t.Fatal("second patch changed something", again, err)
	}
	if os.Getenv("PLAYLIST_OUT") != "" {
		os.WriteFile(filepath.Join(os.Getenv("PLAYLIST_OUT"), "NFSGame.sb"), nsb, 0644)
		os.WriteFile(filepath.Join(os.Getenv("PLAYLIST_OUT"), "NFSGame.toc"), ntoc, 0644)
	}
}

func TestEnsureOnOff(t *testing.T) {
	sb, err := os.ReadFile(filepath.Join(realDir, "NFSGame.sb"))
	if err != nil {
		t.Skip("game data not here")
	}
	toc, _ := os.ReadFile(filepath.Join(realDir, "NFSGame.toc"))
	game := t.TempDir()
	dir := filepath.Join(game, RelDir)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "NFSGame.sb"), sb, 0444)
	os.WriteFile(filepath.Join(dir, "NFSGame.toc"), toc, 0444)
	r, err := Ensure(game, true)
	if err != nil || !r.Changed || len(r.Added) != 10 {
		t.Fatal(r, err)
	}
	if r, err = Ensure(game, true); err != nil || r.Changed {
		t.Fatal("second run", r, err)
	}
	if r, err = Ensure(game, false); err != nil || !r.Changed {
		t.Fatal("restore", r, err)
	}
	back, _ := os.ReadFile(filepath.Join(dir, "NFSGame.sb"))
	if !bytes.Equal(back, sb) {
		t.Fatal("not restored")
	}
	if !IsExtra("fe8a591e-47bf-4f81-b278-1e7e238624b5") || IsExtra("F3E31DF0-82C0-4F21-A56E-8C6F614D4C29") {
		t.Fatal("IsExtra")
	}
}
