package server

import (
	"path/filepath"
	"testing"

	"nfsrun-server/internal/config"
)

func TestBans(t *testing.T) {
	s := &Server{cfg: config.Config{BansFile: filepath.Join(t.TempDir(), "sub", "bans.txt")}}
	if s.isBanned("Racer") {
		t.Fatal("nobody should be banned yet")
	}
	if ch, err := s.setBan("Racer", true); err != nil || !ch {
		t.Fatalf("ban: changed=%v err=%v", ch, err)
	}
	if ch, _ := s.setBan("racer", true); ch {
		t.Fatal("banning twice must change nothing")
	}
	if !s.isBanned("RACER") {
		t.Fatal("bans are not case-sensitive")
	}
	// a fresh server reads the file back
	s2 := &Server{cfg: s.cfg}
	if !s2.isBanned("racer") {
		t.Fatal("the ban was not saved")
	}
	if ch, _ := s.setBan("Racer", false); !ch || s.isBanned("racer") {
		t.Fatal("unban failed")
	}
}
