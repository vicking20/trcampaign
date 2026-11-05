package server

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// The race server logs in as c4win32serv01 and then sends
// loginPersona PNAM=<computer name>; that must keep its NFS-DS persona.
func TestDSLoginPersonaKeepsAccountPersona(t *testing.T) {
	var logs syncBuf
	s := New(testConfig(), &multiStore{}, log.New(&logs, "", 0))
	ds := connectDedicatedServer(t, s)
	var pn blaze.Writer
	pn.TagString("PNAM", "RACE-SERVER-PC")
	r := ds.call(0x0001, 0x006E, &pn)
	if r.Error != 0 {
		t.Fatalf("loginPersona error %d\n%s", r.Error, logs.String())
	}
	if !strings.Contains(logs.String(), `asked for "RACE-SERVER-PC": keeping the account's persona "NFS-DS-01"`) {
		t.Fatal(logs.String())
	}
}

func TestStreamPushesFriendChanges(t *testing.T) {
	var logs syncBuf
	fs := &fakeStore{
		per: database.Persona{ID: 1, BlazeID: 1000001, Name: "Player1"},
		others: []database.PersonaRef{
			{ID: 1, BlazeID: 1000001, Name: "Player1"},
			{ID: 2, BlazeID: 1000002, Name: "Rival"},
			{ID: 3, BlazeID: 1000003, Name: "NFS-DS-01"},
		},
	}
	s := New(testConfig(), fs, log.New(&logs, "", 0))
	mux := http.NewServeMux()
	mux.HandleFunc("/nfsrun/stream", s.handleStreamHTTP)
	srv := httptest.NewServer(s.logHTTP(mux))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/nfsrun/stream?persona=Player1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	type msg struct {
		Type    string          `json:"type"`
		Players []playerEntry   `json:"players"`
		Invites []pendingInvite `json:"invites"`
	}
	next := func() msg {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m msg
		json.Unmarshal(line, &m)
		return m
	}
	m := next()
	if m.Type != "players" || len(m.Players) != 2 || m.Players[1].Name != "Rival" || m.Players[1].Online {
		t.Fatalf("first snapshot %+v", m)
	}
	// Rival comes online -> pushed at once
	s.online.set(&connState{}, "Rival")
	s.hub.kick()
	if m = next(); m.Type != "players" || !m.Players[0].Online || m.Players[0].Name != "Rival" || m.Players[0].Presence != "INGAME" {
		t.Fatalf("online update %+v", m)
	}
	// an invite for Player1
	s.invites.add(1000001, pendingInvite{From: 1000002, Name: "Rival", Session: "1000002"})
	s.hub.kick()
	if m = next(); len(m.Invites) != 1 || m.Invites[0].From != 1000002 {
		t.Fatalf("invite %+v", m)
	}
}

// The race server needs its QoS ping site ("sjc") in preAuth.
func TestDSPreAuthHasQosPingSite(t *testing.T) {
	var logs syncBuf
	s := New(testConfig(), &multiStore{}, log.New(&logs, "", 0))
	ds := connectDedicatedServer(t, s)
	_ = ds
	if !strings.Contains(logs.String(), `QoS ping site "sjc"`) {
		t.Fatal(logs.String())
	}
	fs, err := blaze.DecodeAll(buildPreAuthResponseQoS("x", &qosSites{Host: "h", Port: 17502, Sites: []string{"sjc"}}))
	if err != nil {
		t.Fatal(err)
	}
	out := blaze.FormatFields(fs, "")
	if !strings.Contains(out, "QOSS") || !strings.Contains(out, "sjc") || !strings.Contains(out, "LTPS") {
		t.Fatal(out)
	}
	// a player does not get it
	pf, _ := blaze.DecodeAll(buildPreAuthResponse("x"))
	if strings.Contains(blaze.FormatFields(pf, ""), "QOSS") {
		t.Fatal("client got QOSS")
	}
}
