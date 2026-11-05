package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nfsrun-server/internal/lsx"
)

type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func TestLiveChannelTurnsChangesIntoEvents(t *testing.T) {
	step := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snap := func(online bool, pres string, inv string) string {
			return fmt.Sprintf(`{"type":"players","players":[{"name":"Me","userId":1,"online":true},{"name":"Ze","userId":2,"online":%t,"presence":"%s","sessionId":"2"}]%s}`, online, pres, inv)
		}
		if r.URL.Path == "/nfsrun/players" {
			fmt.Fprintln(w, snap(false, "OFFLINE", ""))
			return
		}
		fl := w.(http.Flusher)
		fmt.Fprintln(w, snap(false, "OFFLINE", ""))
		fl.Flush()
		for msg := range step {
			fmt.Fprintln(w, msg)
			fl.Flush()
		}
	}))
	defer srv.Close()
	var buf lockedBuf
	l := log.New(&buf, "", 0)
	fs := newFriendSource(srv.URL, "Me", &launcherSessionState{}, l)
	ls := lsx.NewServer(lsx.Identity{Persona: "Me", UserID: 1, Friends: fs.list}, l)
	go fs.run(ls, 1)
	step <- `{"type":"players","players":[{"name":"Me","userId":1,"online":true},{"name":"Ze","userId":2,"online":true,"presence":"JOINABLE","sessionId":"2"}],"invites":[{"from":2,"name":"Ze","session":"2"}]}`
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out := buf.String()
		if strings.Contains(out, "Ze (2) is now online session=2: GetPresenceResponse event") && strings.Contains(out, `invite from "Ze" (2) accepted, session "2"`) {
			if fl := fs.list(); len(fl) != 2 || !fl[1].Online {
				t.Fatalf("cache %+v", fl)
			}
			close(step)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(buf.String())
}
