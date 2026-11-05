package console

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nfsrun-server/internal/server"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestDashboardAndStopFile(t *testing.T) {
	ring := NewEventRing(5)
	l := log.New(ring, "", log.LstdFlags|log.Lmicroseconds)
	l.Printf("[BLAZE] RX id=1 comp=0x0009 (Util.ping)")
	l.Printf("[AUTH] loginPersona persona=\"Player1\" blazeId=1000001")
	l.Printf("[MM] dedicated game 2 (server \"NFS-DS-01\") now hosts [Player1]\n  TDF line")
	if r := ring.Recent(); len(r) != 2 || !strings.Contains(r[0], "Player1") || strings.Contains(r[1], "TDF") {
		t.Fatalf("recent %q", r)
	}
	stopFile := filepath.Join(t.TempDir(), "stop.request")
	var out syncBuf
	stopped := make(chan string, 1)
	done := make(chan struct{})
	stats := func() server.Stats {
		return server.Stats{Uptime: 75 * time.Second, PlayersOnline: []string{"Player1", "Ze"}, InRace: []string{"Player1"},
			RaceServers: []server.RaceServerStat{{N: 1, Port: 25200, PID: 42, State: "running", Uptime: time.Minute}},
			ServerGames: []server.GameStat{{ID: 2, State: "IN_GAME", Host: "NFS-DS-01", Dedicated: true, Players: []string{"Player1"}}}}
	}
	go Run(stats, ring, Options{Public: "host", LogPath: "logs/server.log", StopFile: stopFile, Refresh: time.Hour, Out: &out},
		func(r string) { stopped <- r }, done)
	time.Sleep(200 * time.Millisecond)
	os.WriteFile(stopFile, nil, 0644)
	select {
	case r := <-stopped:
		if !strings.Contains(r, "stop_server.bat") {
			t.Fatal(r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop file ignored")
	}
	close(done)
	s := out.String()
	for _, want := range []string{"Players online : 2", "Racing         : 1", "#1  UDP 25200", "game 2", "IN_GAME", "now hosts [Player1]", "[Q] stop the server"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	if _, err := os.Stat(stopFile); err == nil {
		t.Fatal("stop file not removed")
	}
}
