package server

import (
	"log"
	"net"
	"strings"
	"testing"

	"nfsrun-server/internal/blaze"
)

// connectDedicatedServer logs in the way the exe does with -dedicated:
// account c4win32serv01@ea.com / c4server.
func connectDedicatedServer(t *testing.T, s *Server) *client {
	a, b := net.Pipe()
	go s.handleConn(b, false)
	cl := newClient(t, a)
	var pre blaze.Writer
	pre.GroupStart("CINF")
	pre.TagString("CLNT", "nfs server")
	pre.GroupEnd()
	cl.call(0x0009, 0x0007, &pre)
	var lg blaze.Writer
	lg.TagString("MAIL", "c4win32serv01@ea.com")
	lg.TagString("PASS", "c4server")
	if r := cl.call(0x0001, 0x0028, &lg); r.Error != 0 {
		t.Fatal("ds login failed")
	}
	return cl
}

func dsCreateGameWriter() *blaze.Writer {
	var w blaze.Writer
	w.EmptyList("ADMN", blaze.TDFVarInt)
	w.EmptyMap("ATTR", blaze.TDFString, blaze.TDFString)
	w.TagString("GNAM", "nfs ds")
	w.TagVar("GSET", 0x507)
	w.Tag("HNET", blaze.TDFList)
	w.WriteByte(byte(blaze.TDFGroup)) // the exe's own format
	w.Var(1)
	writeListNetworkAddress(&w, netInfo{Set: true, IntIP: 0xC0A80A66, IntPort: 25200})
	w.TagVar("NTOP", 1)
	writeVarList(&w, "PCAP", []uint64{8, 0})
	w.TagString("VSTR", "2100")
	return &w
}

func mmWithPlaylist(dur uint64, playlist string) *blaze.Writer {
	w := mmRequestWriter(dur, 2, 0xC0A80A02)
	if playlist != "" {
		// ATTR must precede CRIT? No: tags are written in ascending order by the
		// real client; the decoder here does not care about order.
		writeKVMap(w, "ATTR", []kv{{"playListId", playlist}, {"skillLevel", "12"}})
	}
	return w
}

func TestDedicatedServerFlow(t *testing.T) {
	logs := &syncBuf{}
	cfg := testConfig()
	s := New(cfg, &multiStore{}, log.New(logs, "", 0))
	ds := connectDedicatedServer(t, s)

	r := ds.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter())
	gid := fieldUintAuth(decodeOK(t, r.Payload), "GID")
	if r.Error != 0 || gid == 0 {
		t.Fatalf("createGame %v", r)
	}
	setup := decodeOK(t, ds.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	assertSorted(t, setup, "NotifyGameSetup(ds)")
	if len(fieldStructList(setup, "PROS")) != 0 {
		t.Fatal("DS must not be in the roster")
	}
	var adv blaze.Writer
	adv.TagVar("GID", gid)
	adv.TagVar("GSTA", gameStateResetable)
	ds.call(ComponentGameManager, GMAdvanceGameState, &adv)
	ds.waitNotify(ComponentGameManager, GMNotifyGameStateChange)

	// Quick Match: no playListId -> server default playlist is applied.
	a := connectAndLogin(t, s, "Player1")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	reset := decodeOK(t, ds.waitNotify(ComponentGameManager, GMNotifyGameReset).Payload)
	data := usGroup(t, reset, "DATA")
	if !hasAttr(data, "playListId", cfg.MMDefaultPlaylist) {
		t.Fatalf("reset data attrs %v", data)
	}
	join := decodeOK(t, ds.waitNotify(ComponentGameManager, GMNotifyPlayerJoining).Payload)
	if fieldUintAuth(usGroup(t, join, "PDAT"), "PID") != 1000002 {
		t.Fatalf("joining %v", join)
	}
	preGame(t, ds, uint64(gid)) // Players get the game once the race server is in PRE_GAME
	sa := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	g := usGroup(t, sa, "GAME")
	assertSorted(t, g, "ReplicatedGameData(ds)")
	if fieldUintAuth(g, "NTOP") != 1 || fieldUintAuth(usGroup(t, g, "THST"), "HPID") != 1000001 || fieldUintAuth(matchCtx(t, sa), "RSLT") != mmResultCreatedGame {
		t.Fatalf("player setup %v", sa)
	}
	if len(fieldStructList(sa, "PROS")) != 1 {
		t.Fatalf("roster %v", fieldStructList(sa, "PROS"))
	}

	// DS opens the doors: OkToConnect reaches the player.
	var at blaze.Writer
	writeKVMap(&at, "ATTR", []kv{{"OkToConnect", "true"}})
	at.TagVar("GID", gid)
	ds.call(ComponentGameManager, GMSetGameAttributes, &at)
	if f := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameAttribChange).Payload); !hasAttr(f, "OkToConnect", "true") {
		t.Fatalf("attr change %v", f)
	}

	// A second player of the same playlist joins the running game.
	b := connectAndLogin(t, s, "Rival")
	b.call(ComponentGameManager, GMStartMatchmaking, mmWithPlaylist(10000, cfg.MMDefaultPlaylist))
	sb := decodeOK(t, b.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	if fieldUintAuth(matchCtx(t, sb), "RSLT") != mmResultJoinedExistingGame || len(fieldStructList(sb, "PROS")) != 2 {
		t.Fatalf("second setup %v", sb)
	}
	if f := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyPlayerJoining).Payload); fieldUintAuth(usGroup(t, f, "PDAT"), "PID") != 1000003 {
		t.Fatalf("a not told about b: %v", f)
	}
	ds.waitNotify(ComponentGameManager, GMNotifyPlayerJoining)

	// DS reports Player1 connected.
	var mesh blaze.Writer
	mesh.TagVar("GID", gid)
	writeStructListHeader(&mesh, "TARG", 1)
	mesh.TagVar("FLGS", 0)
	mesh.TagVar("PID", 1000002)
	mesh.TagVar("STAT", 2)
	mesh.GroupEnd()
	ds.call(ComponentGameManager, GMUpdateMeshConnection, &mesh)
	if f := decodeOK(t, b.waitNotify(ComponentGameManager, GMNotifyPlayerJoinCompleted).Payload); fieldUintAuth(f, "PID") != 1000002 {
		t.Fatalf("join completed %v", f)
	}

	// Race over: back to the pool.
	var ret blaze.Writer
	ret.TagVar("GID", gid)
	ds.call(ComponentGameManager, GMReturnDedicatedServerToPool, &ret)
	a.waitNotify(ComponentGameManager, GMNotifyPlayerRemoved)
	b.waitNotify(ComponentGameManager, GMNotifyPlayerRemoved)

	// Server goes away while a player is matched in: game removed.
	c := connectAndLogin(t, s, "Third")
	c.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 1))
	preGame(t, ds, uint64(gid))
	c.waitNotify(ComponentGameManager, GMNotifyGameSetup)
	ds.c.Close()
	if f := decodeOK(t, c.waitNotify(ComponentGameManager, GMNotifyGameRemoved).Payload); fieldUintAuth(f, "GID") != gid {
		t.Fatalf("removed %v", f)
	}
	a.c.Close()
	b.c.Close()
	c.c.Close()
}

func TestDedicatedModeWaitsForServer(t *testing.T) {
	logs := &syncBuf{}
	cfg := testConfig()
	cfg.MMMode = "dedicated"
	s := New(cfg, &multiStore{}, log.New(logs, "", 0))
	a := connectAndLogin(t, s, "Player1")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 1))
	// no game yet; a server shows up and becomes RESETABLE -> the waiting player gets it
	ds := connectDedicatedServer(t, s)
	gid := fieldUintAuth(decodeOK(t, ds.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter()).Payload), "GID")
	var adv blaze.Writer
	adv.TagVar("GID", gid)
	adv.TagVar("GSTA", gameStateResetable)
	ds.call(ComponentGameManager, GMAdvanceGameState, &adv)
	preGame(t, ds, uint64(gid)) // Players get the game once the race server is in PRE_GAME
	sa := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	if fieldUintAuth(usGroup(t, sa, "GAME"), "GID") != gid {
		t.Fatalf("setup %v", sa)
	}
	a.c.Close()
	ds.c.Close()
}

func hasAttr(fs []blaze.Field, k, v string) bool {
	for _, a := range mapKV(fs, "ATTR") {
		if a.K == k && a.V == v {
			return true
		}
	}
	return false
}

// The race server only calls finalizeGameCreation; the server must
// put its empty game into the pool (RESETABLE) so Quick Match can reset it.
func TestDSFinalizePoolsGame(t *testing.T) {
	logs := &syncBuf{}
	s := New(testConfig(), &multiStore{}, log.New(logs, "", 0))
	ds := connectDedicatedServer(t, s)
	r := ds.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter())
	gid := fieldUintAuth(decodeOK(t, r.Payload), "GID")
	ds.waitNotify(ComponentGameManager, GMNotifyGameSetup)
	var fin blaze.Writer
	fin.TagVar("GID", gid)
	ds.call(ComponentGameManager, GMFinalizeGameCreation, &fin)
	ch := decodeOK(t, ds.waitNotify(ComponentGameManager, GMNotifyGameStateChange).Payload)
	if fieldUintAuth(ch, "GSTA") != gameStateResetable || fieldUintAuth(ch, "GID") != gid {
		t.Fatalf("state change %v", ch)
	}
	a := connectAndLogin(t, s, "Player1")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	ds.waitNotify(ComponentGameManager, GMNotifyGameReset)
	preGame(t, ds, uint64(gid)) // Players get the game once the race server is in PRE_GAME
	sa := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	if fieldUintAuth(matchCtx(t, sa), "RSLT") != mmResultCreatedGame {
		t.Fatalf("player setup %v", sa)
	}
	// The race server's user (THST HPID) was announced to the player
	// BEFORE NotifyGameSetup (the client looks it up while building the mesh).
	hpid := fieldUintAuth(usGroup(t, usGroup(t, sa, "GAME"), "THST"), "HPID")
	found := false
	for _, p := range a.got {
		if p.Component == ComponentUserSessions && p.Command == NotifyUserAdded &&
			fieldUintAuth(fieldGroupOf(decodeOK(t, p.Payload), "USER"), "ID") == hpid {
			found = true
		}
	}
	if !found {
		t.Fatalf("player never told about the race server user %d before NotifyGameSetup", hpid)
	}
	// and the race server about the player
	pa := decodeOK(t, ds.waitNotify(ComponentUserSessions, NotifyUserAdded).Payload)
	if fieldUintAuth(fieldGroupOf(pa, "USER"), "ID") != 1000002 {
		t.Fatalf("ds told about %v", pa)
	}
}

// A player still listed in a race-server game who starts Quick Match
// again is removed from it first, never added twice.
func TestStaleGameLeftOnNewMatchmaking(t *testing.T) {
	logs := &syncBuf{}
	s := New(testConfig(), &multiStore{}, log.New(logs, "", 0))
	ds := connectDedicatedServer(t, s)
	r := ds.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter())
	gid := fieldUintAuth(decodeOK(t, r.Payload), "GID")
	var fin blaze.Writer
	fin.TagVar("GID", gid)
	ds.call(ComponentGameManager, GMFinalizeGameCreation, &fin)
	a := connectAndLogin(t, s, "Player1")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	preGame(t, ds, uint64(gid)) // Players get the game once the race server is in PRE_GAME
	a.waitNotify(ComponentGameManager, GMNotifyGameSetup)
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	rm := decodeOK(t, ds.waitNotify(ComponentGameManager, GMNotifyPlayerRemoved).Payload)
	if fieldUintAuth(rm, "PID") != 1000002 {
		t.Fatalf("removed %v", rm)
	}
	s.gm.mu.Lock()
	n := 0
	if g := s.gm.games[uint32(gid)]; g != nil {
		n = len(g.players)
	}
	s.gm.mu.Unlock()
	if n > 1 {
		t.Fatalf("player listed %d times", n)
	}
	if !strings.Contains(logs.String(), "still listed here") {
		t.Fatal(logs.String())
	}
}

// By default players are sent to the port the race server reports
// (its -gamePort); DS_MESH_PORT still overrides it.
func TestDSMeshPortAdvertised(t *testing.T) {
	s := New(testConfig(), &multiStore{}, log.New(&syncBuf{}, "", 0))
	ds := connectDedicatedServer(t, s)
	ds.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter())
	setup := decodeOK(t, ds.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	n, ok := parseNetworkAddressList(usGroup(t, setup, "GAME"), "HNET")
	if !ok || n.IntPort != 25200 {
		t.Fatalf("HNET %+v", n)
	}

	cfg := testConfig()
	cfg.DSMeshPort = 7777
	s2 := New(cfg, &multiStore{}, log.New(&syncBuf{}, "", 0))
	ds2 := connectDedicatedServer(t, s2)
	ds2.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter())
	setup2 := decodeOK(t, ds2.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	n2, ok := parseNetworkAddressList(usGroup(t, setup2, "GAME"), "HNET")
	if !ok || n2.IntPort != 7777 || n2.ExtPort != 7777 {
		t.Fatalf("HNET override %+v", n2)
	}
}

// In a race-server game the race server is HSLT 0 and players get
// slots from 1 (ConnApi index = slot id, so the host entry would overwrite
// a player in slot 0).
func TestDSPlayersSkipHostSlot(t *testing.T) {
	cfg := testConfig()
	s := New(cfg, &multiStore{}, log.New(&syncBuf{}, "", 0))
	ds := connectDedicatedServer(t, s)
	r := ds.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter())
	gid := fieldUintAuth(decodeOK(t, r.Payload), "GID")
	ds.waitNotify(ComponentGameManager, GMNotifyGameSetup)
	var adv blaze.Writer
	adv.TagVar("GID", gid)
	adv.TagVar("GSTA", gameStateResetable)
	ds.call(ComponentGameManager, GMAdvanceGameState, &adv)
	ds.waitNotify(ComponentGameManager, GMNotifyGameStateChange)

	a := connectAndLogin(t, s, "Player1")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	ds.waitNotify(ComponentGameManager, GMNotifyGameReset)
	join := decodeOK(t, ds.waitNotify(ComponentGameManager, GMNotifyPlayerJoining).Payload)
	if sid := fieldUintAuth(usGroup(t, join, "PDAT"), "SID"); sid != 1 {
		t.Fatalf("first player SID %d, want 1", sid)
	}
	preGame(t, ds, uint64(gid)) // Players get the game once the race server is in PRE_GAME
	sa := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	g := usGroup(t, sa, "GAME")
	if fieldUintAuth(usGroup(t, g, "THST"), "HSLT") != dsHostSlot || fieldUintAuth(usGroup(t, g, "PHST"), "HSLT") != dsHostSlot {
		t.Fatalf("host slot %v", g)
	}
	for _, p := range fieldStructList(sa, "PROS") {
		if fieldUintAuth(p, "SID") == dsHostSlot {
			t.Fatalf("player in the race server's slot: %v", p)
		}
	}

	b := connectAndLogin(t, s, "Rival")
	b.call(ComponentGameManager, GMStartMatchmaking, mmWithPlaylist(10000, cfg.MMDefaultPlaylist))
	b.waitNotify(ComponentGameManager, GMNotifyGameSetup)
	jb := decodeOK(t, ds.waitNotify(ComponentGameManager, GMNotifyPlayerJoining).Payload)
	if sid := fieldUintAuth(usGroup(t, jb, "PDAT"), "SID"); sid != 2 {
		t.Fatalf("second player SID %d, want 2", sid)
	}
}

// After a reset the players are held until the race server is in
// PRE_GAME, then get the game with the race server's attributes in it; a
// race server that never gets there releases them after DS_SETUP_WAIT; a
// race server that disconnects meanwhile ends their matchmaking.
func TestHeldUntilPreGame(t *testing.T) {
	logs := &syncBuf{}
	cfg := testConfig()
	cfg.DSSetupWaitSec = 1
	s := New(cfg, &multiStore{}, log.New(logs, "", 0))
	ds := connectDedicatedServer(t, s)
	gid := fieldUintAuth(decodeOK(t, ds.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter()).Payload), "GID")
	finalizeGame(t, ds, gid)
	a := connectAndLogin(t, s, "Player1")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	ds.waitNotify(ComponentGameManager, GMNotifyGameReset)
	ds.waitNotify(ComponentGameManager, GMNotifyPlayerJoining)
	var at blaze.Writer
	writeKVMap(&at, "ATTR", []kv{{"SessionIndex", "8"}, {"RouteIndex", "0"}})
	at.TagVar("GID", gid)
	ds.call(ComponentGameManager, GMSetGameAttributes, &at)
	for _, p := range a.got {
		if p.Component == ComponentGameManager && (p.Command == GMNotifyGameSetup || p.Command == GMNotifyGameAttribChange) {
			t.Fatalf("held player got %d before PRE_GAME", p.Command)
		}
	}
	preGame(t, ds, gid)
	sa := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	g := usGroup(t, sa, "GAME")
	// The game as reset (INITIALIZING, no RouteIndex/SessionIndex),
	// then PRE_GAME and the race server's attributes
	if hasAttr(g, "SessionIndex", "8") || fieldUintAuth(g, "GSTA") != gameStateInitializing || fieldUintAuth(matchCtx(t, sa), "RSLT") != mmResultCreatedGame {
		t.Fatalf("setup %v", g)
	}
	if f := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameStateChange).Payload); fieldUintAuth(f, "GSTA") != gameStatePreGame {
		t.Fatalf("state %v", f)
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		f := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameAttribChange).Payload)
		for _, kv := range mapKV(f, "ATTR") {
			got[kv.K+"="+kv.V] = true
		}
	}
	if !got["SessionIndex=8"] || !got["RouteIndex=0"] {
		t.Fatalf("attr changes %v", got)
	}

	// replayGame -> REPLAY_SETUP for everyone
	var rp blaze.Writer
	rp.TagVar("GID", gid)
	var post blaze.Writer
	post.TagVar("GID", gid)
	post.TagVar("GSTA", gameStatePostGame)
	ds.call(ComponentGameManager, GMAdvanceGameState, &post)
	ds.call(ComponentGameManager, GMReplayGame, &rp)
	s.gm.mu.Lock()
	replayed := s.gm.games[uint32(gid)].gstate
	s.gm.mu.Unlock()
	if replayed != gameStatePreGame {
		t.Fatalf("after replayGame state %d, want PRE_GAME", replayed)
	}

	// a second race server that never reaches PRE_GAME: released after 1 s
	ds2 := connectDedicatedServer(t, s)
	gid2 := fieldUintAuth(decodeOK(t, ds2.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter()).Payload), "GID")
	finalizeGame(t, ds2, gid2)
	b := connectAndLogin(t, s, "Rival")
	b.call(ComponentGameManager, GMStartMatchmaking, mmWithPlaylist(10000, "40808F8D-99F3-44D6-AE7F-0F06B2D84FD3"))
	if f := decodeOK(t, b.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload); fieldUintAuth(usGroup(t, f, "GAME"), "GID") != gid2 {
		t.Fatalf("b setup %v", f)
	}
	if !strings.Contains(logs.String(), "did not reach PRE_GAME") {
		t.Fatal(logs.String())
	}

	// a third one that disconnects while its player is held
	ds3 := connectDedicatedServer(t, s)
	gid3 := fieldUintAuth(decodeOK(t, ds3.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter()).Payload), "GID")
	finalizeGame(t, ds3, gid3)
	c := connectAndLogin(t, s, "Third")
	c.call(ComponentGameManager, GMStartMatchmaking, mmWithPlaylist(10000, "11111111-1111-1111-1111-111111111111"))
	ds3.waitNotify(ComponentGameManager, GMNotifyPlayerJoining)
	ds3.c.Close()
	if f := decodeOK(t, c.waitNotify(ComponentGameManager, GMNotifyMatchmakingFailed).Payload); fieldUintAuth(f, "RSLT") != mmResultTimedOut {
		t.Fatalf("c %v", f)
	}
	a.c.Close()
	b.c.Close()
	c.c.Close()
	ds.c.Close()
	ds2.c.Close()
}
