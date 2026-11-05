package server

import (
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"nfsrun-server/internal/blaze"
)

// Player <-> relay <-> race server, end to end over real UDP.
func TestRelayEndToEnd(t *testing.T) {
	fakeDS, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer fakeDS.Close()
	cfg := testConfig()
	cfg.DSMeshPort = fakeDS.LocalAddr().(*net.UDPAddr).Port
	cfg.RelayPortBase = 46100
	logs := &syncBuf{}
	s := New(cfg, &multiStore{}, log.New(logs, "", 0))

	ds := connectDedicatedServer(t, s)
	r := ds.call(ComponentGameManager, GMCreateGame, dsCreateGameWriter())
	gid := fieldUintAuth(decodeOK(t, r.Payload), "GID")
	finalizeGame(t, ds, gid)

	a := connectAndLogin(t, s, "Player1")
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	preGame(t, ds, gid)
	sa := decodeOK(t, a.waitNotify(ComponentGameManager, GMNotifyGameSetup).Payload)
	hnet, ok := parseNetworkAddressList(usGroup(t, sa, "GAME"), "HNET")
	if !ok || hnet.ExtPort < 46100 || hnet.ExtPort >= 46164 {
		t.Fatalf("player HNET %+v", hnet)
	}
	join := decodeOK(t, ds.waitNotify(ComponentGameManager, GMNotifyPlayerJoining).Payload)
	pnet, ok := parseNetworkAddress(usGroup(t, join, "PDAT"), "PNET")
	if !ok || ipString(pnet.ExtIP) != "127.0.0.1" || pnet.ExtPort == 0 {
		t.Fatalf("race server PNET %+v", pnet)
	}

	// player -> relay -> race server
	player, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer player.Close()
	relayAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(hnet.ExtPort)}
	player.WriteToUDP([]byte("hello-ds"), relayAddr)
	buf := make([]byte, 64)
	fakeDS.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, from, err := fakeDS.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != "hello-ds" || from.Port != int(pnet.ExtPort) {
		t.Fatalf("race server got %q from %v (%v), want from port %d", buf[:n], from, err, pnet.ExtPort)
	}
	// race server -> relay -> player
	fakeDS.WriteToUDP([]byte("hello-player"), from)
	player.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, from2, err := player.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != "hello-player" || from2.Port != int(hnet.ExtPort) {
		t.Fatalf("player got %q from %v (%v)", buf[:n], from2, err)
	}
	// leaving closes the relay
	a.call(ComponentGameManager, GMStartMatchmaking, mmRequestWriter(10000, 2, 0xC0A80A02))
	time.Sleep(100 * time.Millisecond)
	if !strings.Contains(logs.String(), "race server answered") || !strings.Contains(logs.String(), "closed") {
		t.Fatal(logs.String())
	}
}

// preGame: the race server finishes setting up its reset game.
func preGame(t *testing.T, ds *client, gid uint64) {
	var w blaze.Writer
	w.TagVar("GID", gid)
	w.TagVar("GSTA", gameStatePreGame)
	ds.call(ComponentGameManager, GMAdvanceGameState, &w)
}

func finalizeGame(t *testing.T, ds *client, gid uint64) {
	var w blaze.Writer
	w.TagVar("GID", gid)
	ds.call(ComponentGameManager, GMFinalizeGameCreation, &w)
}
