package server

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// multiStore hands out one persona per name.
type multiStore struct {
	fakeStore
	mu  sync.Mutex
	ids map[string]database.Persona
}

func (m *multiStore) GetOrCreateIdentity(email, name string) (database.Persona, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ids == nil {
		m.ids = map[string]database.Persona{}
	}
	if p, ok := m.ids[name]; ok {
		return p, nil
	}
	id := uint64(len(m.ids) + 1)
	p := database.Persona{ID: id, AccountID: id, BlazeID: 1000000 + id, Name: name, Email: email, SessionKey: "k" + name}
	m.ids[name] = p
	return p, nil
}

type client struct {
	t   *testing.T
	c   net.Conn
	id  uint16
	in  chan *blaze.Packet
	got []*blaze.Packet
}

// newClient keeps reading in the background: net.Pipe has no buffer, so an
// unread notification would block the server like a full TCP window.
func newClient(t *testing.T, c net.Conn) *client {
	cl := &client{t: t, c: c, in: make(chan *blaze.Packet, 256)}
	go func() {
		defer close(cl.in)
		for {
			p, err := blaze.ReadPacket(c)
			if err != nil {
				return
			}
			cl.in <- p
		}
	}()
	return cl
}

func (c *client) next() (*blaze.Packet, bool) {
	select {
	case p, ok := <-c.in:
		return p, ok
	case <-time.After(3 * time.Second):
		return nil, false
	}
}

func (c *client) call(comp, cmd uint16, w *blaze.Writer) *blaze.Packet {
	c.t.Helper()
	c.id++
	var payload []byte
	if w != nil {
		payload = w.Bytes()
	}
	c.c.Write((&blaze.Packet{Component: comp, Command: cmd, ID: c.id, Payload: payload}).Bytes())
	for {
		p, ok := c.next()
		if !ok {
			c.t.Fatalf("no reply to %x/%d", comp, cmd)
		}
		if p.QType == blaze.TypeNotify {
			c.got = append(c.got, p)
			continue
		}
		return p
	}
}

// waitNotify returns the next notification comp/cmd (buffered or incoming).
func (c *client) waitNotify(comp, cmd uint16) *blaze.Packet {
	c.t.Helper()
	for i, p := range c.got {
		if p.Component == comp && p.Command == cmd {
			c.got = append(c.got[:i], c.got[i+1:]...)
			return p
		}
	}
	for {
		p, ok := c.next()
		if !ok {
			c.t.Fatalf("waiting for notify %x/%d: none", comp, cmd)
		}
		if p.Component == comp && p.Command == cmd && p.QType == blaze.TypeNotify {
			return p
		}
		c.got = append(c.got, p)
	}
}

// closed reports whether the server closed the connection.
func (c *client) closed() bool {
	for {
		_, ok := c.next()
		if !ok {
			return true
		}
	}
}

func connectAndLogin(t *testing.T, s *Server, name string) *client {
	a, b := net.Pipe()
	go s.handleConn(b, false)
	cl := newClient(t, a)
	var pre blaze.Writer
	pre.GroupStart("CINF")
	pre.TagString("CLNT", "nfs client")
	pre.GroupEnd()
	cl.call(0x0009, 0x0007, &pre)
	var lg blaze.Writer
	lg.TagString("MAIL", "eatest."+name+"@ea.com")
	lg.TagVar("TYPE", 1)
	if r := cl.call(0x0001, 0x0028, &lg); r.Error != 0 {
		t.Fatalf("login %s failed", name)
	}
	var pn blaze.Writer
	pn.TagString("PNAM", name)
	cl.call(0x0001, 0x006E, &pn)
	return cl
}

func mmRequestWriter(dur, pmin uint64, intIP uint64) *blaze.Writer {
	var w blaze.Writer
	w.GroupStart("CRIT")
	w.GroupStart("SIZE")
	w.TagVar("PCAP", 8)
	w.TagVar("PMIN", pmin)
	w.GroupEnd()
	w.GroupEnd()
	w.TagVar("DUR", dur)
	w.TagVar("GSET", 1287)
	w.TagString("GVER", "2100")
	w.TagVar("MODE", 1)
	w.TagVar("NTOP", 1)
	w.UnionStart("PNET", 2)
	writeIPPairValue(&w, netInfo{Set: true, IntIP: intIP, IntPort: 3659})
	w.TagVar("VOIP", 1)
	return &w
}

func TestMatchmakingTimeoutAndCancel(t *testing.T) {
	var logs syncBuf
	cfg := testConfig()
	cfg.MMTimeoutSec = 0 // the client's own DUR
	s := New(cfg, &multiStore{}, log.New(&logs, "", 0))
	a := connectAndLogin(t, s, "Player1")
	r := a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(50, 2, 1))
	msid := fieldUintAuth(decodeOK(t, r.Payload), "MSID")
	n := a.waitNotify(ComponentGameManager, GMNotifyMatchmakingFailed)
	if f := decodeOK(t, n.Payload); fieldUintAuth(f, "RSLT") != mmResultTimedOut || fieldUintAuth(f, "MSID") != msid || fieldUintAuth(f, "USID") != 1000001 {
		t.Fatalf("timeout notify %v", f)
	}
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(60000, 2, 1))
	a.call(ComponentGameManager, GMCancelMatchmaking, nil)
	if f := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyMatchmakingFailed).Payload); fieldUintAuth(f, "RSLT") != mmResultCanceled {
		t.Fatalf("cancel notify %v", f)
	}
}

func TestTwoPlayersGetAGame(t *testing.T) {
	var logs syncBuf
	pc := testConfig()
	pc.MMMode = "peer"
	s := New(pc, &multiStore{}, log.New(&logs, "", 0))
	a := connectAndLogin(t, s, "Player1")
	b := connectAndLogin(t, s, "Rival")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A01))
	rb := b.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	if rb.Error != 0 {
		t.Fatal("start b")
	}
	sa := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	sb := decodeOK(t, b.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	assertSorted(t, sa, "NotifyGameSetup")
	game := usGroup(t, sa, "GAME")
	assertSorted(t, game, "ReplicatedGameData")
	gid := fieldUintAuth(game, "GID")
	if gid == 0 || fieldUintAuth(game, "GSTA") != gameStateInitializing || fieldUintAuth(usGroup(t, game, "THST"), "HPID") != 1000001 {
		t.Fatalf("game %v", game)
	}
	pros := fieldStructList(sa, "PROS")
	if len(pros) != 2 || blaze.FindString(pros[0], "NAME") != "Player1" || blaze.FindString(pros[1], "NAME") != "Rival" {
		t.Fatalf("roster %v", pros)
	}
	assertSorted(t, pros[1], "ReplicatedGamePlayer")
	n1, ok := parseNetworkAddress(pros[1], "PNET")
	if !ok || n1.IntIP != 0xC0A80A02 || n1.IntPort != 3659 {
		t.Fatalf("Rival PNET %+v", n1)
	}
	reasonA := matchCtx(t, sa)
	reasonB := matchCtx(t, sb)
	if fieldUintAuth(reasonA, "RSLT") != mmResultCreatedGame || fieldUintAuth(reasonB, "RSLT") != mmResultJoinedNewGame || fieldUintAuth(reasonB, "USID") != 1000002 {
		t.Fatalf("reasons %v / %v", reasonA, reasonB)
	}

	// host advances state; both get NotifyGameStateChange
	var adv blaze.Writer
	adv.TagVar("GID", gid)
	adv.TagVar("GSTA", 130)
	a.call(ComponentGameManager, GMAdvanceGameState, &adv)
	for _, c := range []*client{a, b} {
		if f := decodeOK(t, c.waitNotify(ComponentGameManager, GMNotifyGameStateChange).Payload); fieldUintAuth(f, "GSTA") != 130 {
			t.Fatalf("state %v", f)
		}
	}

	// b reports the mesh connection to a as CONNECTED
	var mesh blaze.Writer
	mesh.TagVar("GID", gid)
	writeStructListHeader(&mesh, "TARG", 1)
	mesh.TagVar("FLGS", 0)
	mesh.TagVar("PID", 1000001)
	mesh.TagVar("STAT", 2)
	mesh.GroupEnd()
	b.call(ComponentGameManager, GMUpdateMeshConnection, &mesh)
	if f := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyPlayerJoinCompleted).Payload); fieldUintAuth(f, "PID") != 1000002 {
		t.Fatalf("join completed %v", f)
	}

	// b disconnects: a is told, game stays (a is host)
	b.c.Close()
	if f := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyPlayerRemoved).Payload); fieldUintAuth(f, "PID") != 1000002 {
		t.Fatalf("removed %v", f)
	}
	// host leaves via removePlayer: game removed
	var rm blaze.Writer
	rm.TagVar("GID", gid)
	rm.TagVar("PID", 1000001)
	a.call(ComponentGameManager, GMRemovePlayer, &rm)
	if f := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameRemoved).Payload); fieldUintAuth(f, "GID") != gid {
		t.Fatalf("game removed %v", f)
	}
	a.c.Close()
}

func matchCtx(t *testing.T, fs []blaze.Field) []blaze.Field {
	t.Helper()
	for _, f := range fs {
		if f.Tag == "REAS" {
			m := f.Value.(map[string]any)
			if m["key"].(byte) != 3 {
				t.Fatalf("REAS key %v", m["key"])
			}
			return m["field"].(blaze.Field).Value.([]blaze.Field)
		}
	}
	t.Fatal("REAS missing")
	return nil
}

// syncBuf is a log sink safe to read while server goroutines write.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.b.Bytes()...)
}
func (s *syncBuf) String() string { return string(s.Bytes()) }

func TestGuardBlocksNonGameAndPreLogin(t *testing.T) {
	logs := &syncBuf{}
	s := New(testConfig(), &multiStore{}, log.New(logs, "", 0))
	// preAuth without the game's identity -> error + connection closed
	a, b := net.Pipe()
	go s.handleConn(b, false)
	cl := newClient(t, a)
	var pre blaze.Writer
	pre.GroupStart("CINF")
	pre.TagString("CLNT", "curl")
	pre.GroupEnd()
	if r := cl.call(0x0009, 0x0007, &pre); r.Error == 0 {
		t.Fatal("foreign client must be refused")
	}
	if !cl.closed() {
		t.Fatal("connection must be closed")
	}
	// game identity but a protected RPC before login -> refused
	a2, b2 := net.Pipe()
	go s.handleConn(b2, false)
	cl2 := newClient(t, a2)
	var pre2 blaze.Writer
	pre2.GroupStart("CINF")
	pre2.TagString("CLNT", "nfs client")
	pre2.GroupEnd()
	cl2.call(0x0009, 0x0007, &pre2)
	if r := cl2.call(ComponentLocker, LockerListContent, nil); r.Error == 0 {
		t.Fatal("Locker before login must be refused")
	}
	if !bytes.Contains(logs.Bytes(), []byte("before login")) {
		t.Fatalf("log: %s", logs.String())
	}
}

func TestSniffFront(t *testing.T) {
	cases := []struct {
		kind string
		data []byte
		ok   bool
	}{
		{"REDIRECTOR", []byte{0x16, 0x03, 0x00, 0x00, 0x2F}, true},
		{"REDIRECTOR", []byte{0x16, 0x03, 0x01, 0x00, 0x2F}, false}, // modern TLS
		{"REDIRECTOR", []byte("GET / HTTP/1.1\r\n"), false},
		{"BLAZE", (&blaze.Packet{Component: 9, Command: 7}).Bytes(), true},
		{"BLAZE", []byte("GET / HTTP/1.1\r\n"), false},
	}
	for _, c := range cases {
		a, b := net.Pipe()
		go func() { a.Write(c.data); a.Close() }()
		_, ok, _ := sniffFront(b, c.kind)
		if ok != c.ok {
			t.Fatalf("%s %q -> %v", c.kind, c.data, ok)
		}
		b.Close()
	}
}

func TestGuardAllowlistAndLimit(t *testing.T) {
	g := newGuard(true, "10.0.0.0/8, 192.168.1.5", 2)
	if ok, _, _ := g.admit("8.8.8.8:1"); ok {
		t.Fatal("not allowlisted")
	}
	if ok, _, _ := g.admit("127.0.0.1:1"); !ok {
		t.Fatal("loopback always allowed")
	}
	_, _, r1 := g.admit("10.1.2.3:1")
	_, _, r2 := g.admit("10.1.2.3:2")
	if ok, _, _ := g.admit("10.1.2.3:3"); ok {
		t.Fatal("limit 2")
	}
	r1()
	if ok, _, _ := g.admit("10.1.2.3:4"); !ok {
		t.Fatal("slot freed")
	}
	r2()
	if ok, _, _ := g.admit("192.168.1.5:9"); !ok {
		t.Fatal("single ip allowlisted")
	}
}

func TestHTTPGuardUserAgent(t *testing.T) {
	logs := &syncBuf{}
	s := New(testConfig(), &multiStore{}, log.New(logs, "", 0))
	h := s.httpGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	cases := []struct {
		ua, path, remote string
		code             int
	}{
		{"ProtoHttp 1.3/DS 8.14.2.0 (Windows)", "/locker/get/1/client", "8.8.8.8:1", 200},
		{"Mozilla/5.0", "/locker/get/1/client", "8.8.8.8:1", 403},
		{"curl/8", "/health", "8.8.8.8:1", 403},
		{"curl/8", "/health", "127.0.0.1:5", 200},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "http://x"+c.path, nil)
		r.Header.Set("User-Agent", c.ua)
		r.RemoteAddr = c.remote
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		if rr.Code != c.code {
			t.Fatalf("%+v -> %d", c, rr.Code)
		}
	}
}

// Front proxy: scanners never reach the backend; the game's bytes arrive intact
// and the backend can see the real client address.
func TestFrontProxyGuard(t *testing.T) {
	logs := &syncBuf{}
	s := New(testConfig(), &multiStore{}, log.New(logs, "", 0))
	back, _ := net.Listen("tcp", "127.0.0.1:0")
	defer back.Close()
	got := make(chan []byte, 4)
	peers := make(chan string, 4)
	go func() {
		for {
			c, err := back.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				b := make([]byte, 5)
				n, _ := io.ReadFull(c, b)
				peers <- s.clientAddr(c)
				got <- b[:n]
			}(c)
		}
	}()
	front, _ := net.Listen("tcp", "127.0.0.1:0")
	defer front.Close()
	s.wg.Add(1)
	go s.frontProxyLoop(front, "REDIRECTOR", back.Addr().String())

	bad, _ := net.Dial("tcp", front.Addr().String())
	bad.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	select {
	case b := <-got:
		t.Fatalf("scanner reached backend: %q", b)
	case <-time.After(300 * time.Millisecond):
	}
	bad.Close()

	good, _ := net.Dial("tcp", front.Addr().String())
	good.Write([]byte{0x16, 0x03, 0x00, 0x00, 0x2F})
	select {
	case b := <-got:
		if !bytes.Equal(b, []byte{0x16, 0x03, 0x00, 0x00, 0x2F}) {
			t.Fatalf("forwarded %x", b)
		}
		if p := <-peers; p != good.LocalAddr().String() {
			t.Fatalf("backend saw %s, client is %s", p, good.LocalAddr())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("game bytes not forwarded")
	}
	good.Close()
	if !bytes.Contains(logs.Bytes(), []byte("not an SSLv3 ClientHello")) {
		t.Fatalf("log: %s", logs.String())
	}
}

// Auto mode, no race server up, MM_MIN_PLAYERS=1 -> a peer game
// was made for an NTOP=1 request and the player sat on a black screen. Now the
// request waits for a race server and fails cleanly instead.
func TestAutoWithoutRaceServerFailsCleanly(t *testing.T) {
	var logs syncBuf
	cfg := testConfig()
	cfg.MMTimeoutSec = 0 // the client's own DUR
	s := New(cfg, &multiStore{}, log.New(&logs, "", 0))
	a := connectAndLogin(t, s, "Player1")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(300, 2, 1))
	n := a.waitNotify(ComponentGameManager, GMNotifyMatchmakingFailed)
	if fieldUintAuth(decodeOK(t, n.Payload), "RSLT") != mmResultTimedOut || strings.Contains(logs.String(), "[GAME] ") {
		t.Fatalf("log %s", logs.String())
	}
	a.c.Close()
}

// Blaze presence for friends.
func TestFriendPresenceNotifications(t *testing.T) {
	var logs syncBuf
	cfg := testConfig()
	cfg.FriendsAll = true
	s := New(cfg, &multiStore{}, log.New(&logs, "", 0))
	a := connectAndLogin(t, s, "Ze1")
	a.waitNotify(ComponentUserSessions, NotifyUserAdded) // own announcement
	b := connectAndLogin(t, s, "Ze2")
	b.waitNotify(ComponentUserSessions, NotifyUserAdded)
	// Ze1 hears that Ze2 came online
	n := a.waitNotify(ComponentUserSessions, NotifyUserAdded)
	if !strings.Contains(fmt.Sprint(decodeOK(t, n.Payload)), "Ze2") {
		t.Fatalf("UserAdded for Ze2 expected: %v", decodeOK(t, n.Payload))
	}
	// Ze2 leaves -> Ze1 gets UserUpdated with FLGS 0
	b.c.Close()
	for {
		u := a.waitNotify(ComponentUserSessions, NotifyUserUpdated)
		if fieldUintAuth(decodeOK(t, u.Payload), "FLGS") == 0 {
			break
		}
	}
	a.c.Close()
}

func TestRecentPlayersList(t *testing.T) {
	var rp recentPlayers
	ps := []database.Persona{{ID: 1, BlazeID: 11, Name: "A"}, {ID: 2, BlazeID: 12, Name: "B"}, {ID: 3, BlazeID: 13, Name: "NFS-DS-01"}}
	rp.met(ps, func(p database.Persona) uint64 { return p.BlazeID })
	if l := rp.list(1); len(l) != 1 || l[0].BlazeID != 12 {
		t.Fatalf("recent %v", l)
	}
}
