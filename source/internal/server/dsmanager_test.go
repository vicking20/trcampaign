package server

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nfsrun-server/internal/blaze"
)

func TestRaceServerPersonasAreDistinct(t *testing.T) {
	s := New(testConfig(), &multiStore{}, log.New(&syncBuf{}, "", 0))
	a := connectDedicatedServer(t, s)
	b := connectDedicatedServer(t, s)
	var names []string
	for _, c := range []*client{a, b} {
		r := c.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter())
		gid := uint32(fieldUintAuth(decodeOK(t, r.Payload), "GID"))
		s.gm.mu.Lock()
		names = append(names, s.gm.games[gid].ds.Persona.Name)
		s.gm.mu.Unlock()
	}
	if names[0] != "NFS-DS-01" || names[1] != "NFS-DS-02" {
		t.Fatalf("names %v", names)
	}
	a.c.Close()
	b.c.Close()
}

// The supervisor starts the exe with the server arguments and restarts it.
func TestDSInstanceRestarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as fake server")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	exe := filepath.Join(dir, "fake.sh")
	os.WriteFile(exe, []byte("#!/bin/sh\necho \"$@\" >> "+out+"\n"), 0755)
	cfg := testConfig()
	cfg.DSRestartSec = 1
	cfg.DSExtraArgs = "-foo bar"
	logs := &syncBuf{}
	s := New(cfg, &multiStore{}, log.New(logs, "", 0))
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	s.runDSInstance(ctx, &dsInstance{n: 2, port: 25201, keep: true}, exe, dir, filepath.Join(dir, "instance2"))
	b, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "-Server.ServerName NFSTR_DS_2_port25201 -GamePort 25201 -listen 0.0.0.0:25301-25301 -serverInstancePath "+filepath.Join(dir, "instance2")+" -instanceName NFSTR_DS_2_port25201 -Game.DefaultLayerInclusion StartupMode=Game;GameMode=CareerMode -NfsOnline.Backend Backend_Blaze -Server.IsPresenceEnabled true -NfsOnline.AllowSinglePlayerOnline true -foo bar") {
		t.Fatalf("runs %q\nlog %s", lines, logs.Bytes())
	}
}

var _ = blaze.TDFString

// No race server runs until a player starts Quick Match; then one
// is started for it (once: a quick exit is not retried in a loop).
func TestRaceServerStartsOnDemand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as fake server")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	exe := filepath.Join(dir, "fake.sh")
	os.WriteFile(exe, []byte("#!/bin/sh\necho \"$@\" >> "+out+"\n"), 0755)
	cfg := testConfig()
	cfg.DSRestartSec = 30
	logs := &syncBuf{}
	s := New(cfg, &multiStore{}, log.New(logs, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.ds.mu.Lock()
	s.ds.ctx, s.ds.exe, s.ds.gameDir, s.ds.instRoot, s.ds.ready = ctx, exe, dir, dir, true
	s.ds.insts = []*dsInstance{{n: 1, port: 25200, state: "stopped"}}
	s.ds.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a race server ran before anyone asked")
	}
	a := connectAndLogin(t, s, "Player1")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(logs.String(), "failed to start just before") {
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(out)
	if n := len(strings.Split(strings.TrimSpace(string(b)), "\n")); strings.TrimSpace(string(b)) == "" || n != 1 {
		t.Fatalf("%d runs\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), "starting race server 1 for session") || !strings.Contains(logs.String(), "up to 45 s") {
		t.Fatal(logs.String())
	}
	a.c.Close()
}

func TestQuickMatchPlaylistsPersist(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig()
	cfg.LockerDir = filepath.Join(dir, "locker")
	cfg.MMDefaultPlaylist = "A2BD2E19-116D-43A6-A338-D47F6D1EA729"
	s := New(cfg, &multiStore{}, log.New(&syncBuf{}, "", 0))
	s.rememberPlaylistLocked("40808f8d-99f3-44d6-ae7f-0f06b2d84fd3")
	s.rememberPlaylistLocked("40808F8D-99F3-44D6-AE7F-0F06B2D84FD3")
	b, _ := os.ReadFile(filepath.Join(dir, "playlists.txt"))
	if strings.Count(string(b), "40808F8D") != 1 {
		t.Fatalf("file %q", b)
	}
	s2 := New(cfg, &multiStore{}, log.New(&syncBuf{}, "", 0))
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		seen[s2.quickMatchPlaylistLocked()] = true
	}
	if len(seen) != 2 || !seen["40808F8D-99F3-44D6-AE7F-0F06B2D84FD3"] {
		t.Fatalf("pool %v", seen)
	}
}

func TestRuleCoversRange(t *testing.T) {
	rule := "Nome da regra:                        NFSTR race servers (UDP)\r\nProtocolo:                            UDP\r\nPortaLocal:                           6000,6100-6163,9999,25200-25207,25300-25307\r\n"
	if !ruleCoversRange(rule, 6100, 6163) {
		t.Fatal("relay range not found")
	}
	if ruleCoversRange(rule, 6100, 6200) {
		t.Fatal("wider range accepted")
	}
	if ruleCoversRange("LocalPort: 25200-25207,6000,9999", 6100, 6163) {
		t.Fatal("old rule accepted")
	}
}
