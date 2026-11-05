package server

import (
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nfsrun-server/internal/database"
)

func TestSplitCommand(t *testing.T) {
	got := splitCommand(`  rename "Ze Silva"   Ze2 `)
	if len(got) != 3 || got[0] != "rename" || got[1] != "Ze Silva" || got[2] != "Ze2" {
		t.Fatalf("%q", got)
	}
	if got := splitCommand(`kick ""`); len(got) != 2 || got[1] != "" {
		t.Fatalf("%q", got)
	}
}

func TestAdminKickPlayersHelp(t *testing.T) {
	var logs syncBuf
	fs := &fakeStore{per: database.Persona{ID: 1, BlazeID: 1000001, Name: "Player1"}}
	s := New(testConfig(), fs, log.New(&logs, "", 0))
	a, b := net.Pipe()
	defer b.Close()
	st := &connState{conn: a, kind: "BLAZE", wmu: &sync.Mutex{}, peer: "203.0.113.7:5000", Persona: fs.per}
	s.online.set(st, "Player1")

	if r := s.Command("players", false); len(r.Lines) != 2 || !strings.Contains(r.Lines[1], "Player1") || !strings.Contains(r.Lines[1], "menus") || !strings.Contains(r.Lines[1], "203.0.113.7") {
		t.Fatalf("players %q", r.Lines)
	}
	closed := make(chan error, 1)
	go func() { _, err := b.Read(make([]byte, 1)); closed <- err }()
	if r := s.Command("kick player1", false); !strings.Contains(r.Lines[0], "disconnected") {
		t.Fatalf("kick %q", r.Lines)
	}
	select {
	case err := <-closed:
		if err != io.EOF {
			t.Fatalf("pipe read %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connection not closed")
	}
	if r := s.Command("kick Nobody", false); !strings.Contains(r.Lines[0], "not online") {
		t.Fatalf("kick nobody %q", r.Lines)
	}
	if r := s.Command("help", false); len(r.Lines) < 8 {
		t.Fatalf("help %q", r.Lines)
	}
	if r := s.Command("frobnicate", false); !strings.Contains(r.Lines[0], "unknown command") {
		t.Fatalf("unknown %q", r.Lines)
	}
	// commands that need the real database say so
	if r := s.Command("delete-player Player1", false); r.Confirm != "" || !strings.Contains(r.Lines[0], "not available") {
		t.Fatalf("delete without admin store %+v", r)
	}
	if !strings.Contains(logs.String(), "[ADMIN] > kick player1") {
		t.Fatal("command not logged")
	}
	if r := s.Command("games", false); r.Lines[0] != "no games" {
		t.Fatalf("games %q", r.Lines)
	}
}

func TestClaimHours(t *testing.T) {
	if h, err := claimHours(nil); err != nil || h != 12 {
		t.Fatal(h, err)
	}
	if h, err := claimHours([]string{"48h"}); err != nil || h != 48 {
		t.Fatal(h, err)
	}
	for _, bad := range []string{"0", "169", "x"} {
		if _, err := claimHours([]string{bad}); err == nil {
			t.Fatal(bad)
		}
	}
}

// Full admin flow on a real (temporary) SQLite database.
func TestLiveAdminCommands(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	var logs syncBuf
	cfg := testConfig()
	cfg.LockerDir = filepath.Join(t.TempDir(), "locker")
	s := New(cfg, db, log.New(&logs, "", 0))
	name := "Cmd" + strconv.FormatInt(time.Now().UnixNano()%1000000, 10)

	r := s.Command("invite "+name, false)
	if len(r.Lines) < 3 || !strings.Contains(r.Lines[1], "code") {
		t.Fatalf("create %q", r.Lines)
	}
	code := strings.TrimSpace(strings.TrimPrefix(r.Lines[1], "code   :"))
	if r := s.Command("invites", false); !strings.Contains(strings.Join(r.Lines, "\n"), name) {
		t.Fatalf("invites %q", r.Lines)
	}
	key := strings.Repeat("ab", 32)
	per, _, err := db.AuthenticateLauncherPublicKey("eatest."+name+"@ea.com", name, key, code, "open", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Command("invite "+name, false); !strings.Contains(r.Lines[0], "reset-key") {
		t.Fatalf("create on owned name %q", r.Lines)
	}
	// reset-key asks first, then revokes and prints a code
	r = s.Command("reset-key "+name+" 2", false)
	if r.Confirm == "" || len(r.Lines) != 0 {
		t.Fatalf("reset no confirm %+v", r)
	}
	if ok, _ := db.LauncherPublicKeyKnown(key); !ok {
		t.Fatal("key revoked before confirmation")
	}
	r = s.Command("reset-key "+name+" 2", true)
	if ok, _ := db.LauncherPublicKeyKnown(key); ok || !strings.Contains(strings.Join(r.Lines, "\n"), "code   :") {
		t.Fatalf("reset %q", r.Lines)
	}
	newName := name + "R"
	if r := s.Command("rename "+name+" "+newName, false); !strings.Contains(r.Lines[0], "is now") {
		t.Fatalf("rename %q", r.Lines)
	}
	if r := s.Command("players all", false); !strings.Contains(strings.Join(r.Lines, "\n"), newName) {
		t.Fatalf("players all %q", r.Lines)
	}
	// delete-player: files on disk go too
	c, _ := db.CreateLockerContent(per.ID, "SNAPSHOTS", "x", "", "{}", "client", 0)
	photoDir := filepath.Join(cfg.LockerDir, strconv.FormatUint(c.ID, 10))
	os.MkdirAll(photoDir, 0755)
	os.WriteFile(filepath.Join(photoDir, "main.bin"), []byte("x"), 0644)
	webFile := s.web.path(strconv.FormatUint(per.BlazeID, 10))
	os.MkdirAll(filepath.Dir(webFile), 0755)
	os.WriteFile(webFile, []byte("{}"), 0644)
	r = s.Command("delete-player "+newName, false)
	if !strings.Contains(r.Confirm, "DELETE") {
		t.Fatalf("delete confirm %+v", r)
	}
	r = s.Command("delete-player "+newName, true)
	if !strings.Contains(r.Lines[0], "deleted") {
		t.Fatalf("delete %q", r.Lines)
	}
	if _, err := os.Stat(photoDir); err == nil {
		t.Fatal("photo folder kept")
	}
	if _, err := os.Stat(webFile); err == nil {
		t.Fatal("web file kept")
	}
	if r := s.Command("delete-player "+newName, false); !strings.Contains(r.Lines[0], "not found") {
		t.Fatalf("delete twice %q", r.Lines)
	}
}
