package server

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nfsrun-server/internal/database"
)

type claimedHTTPStore struct {
	fakeStore
	claimed string
}

func (s *claimedHTTPStore) LauncherIdentityClaimed(name string) (bool, error) {
	return strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(s.claimed)), nil
}

func TestPresenceSessionOverridesSpoofedPersona(t *testing.T) {
	key := strings.Repeat("12", 24)
	per := database.Persona{
		ID: 1, AccountID: 1, BlazeID: 1000001,
		Name: "RacerOne", Email: "eatest.RacerOne@ea.com", SessionKey: key,
	}
	s := New(testConfig(), &fakeStore{per: per}, log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodPost, "/nfsrun/presence",
		bytes.NewBufferString(`{"persona":"SomeoneElse","presence":"JOINABLE","sessionId":"99"}`))
	req.Header.Set("X-NFSRun-Session", key)
	req.RemoteAddr = "203.0.113.2:40000"
	w := httptest.NewRecorder()
	s.handlePresenceHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if _, ok := s.invites.getPresence("SomeoneElse"); ok {
		t.Fatal("spoofed persona received presence")
	}
	got, ok := s.invites.getPresence("RacerOne")
	if !ok || got.Presence != "JOINABLE" || got.SessionID != "99" {
		t.Fatalf("canonical presence=%+v ok=%v", got, ok)
	}
}

func TestPlayersRejectsBadSuppliedSession(t *testing.T) {
	per := database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001, Name: "RacerOne", SessionKey: strings.Repeat("34", 24)}
	s := New(testConfig(), &fakeStore{per: per}, log.New(io.Discard, "", 0))
	req := httptest.NewRequest(http.MethodGet, "/nfsrun/players?persona=RacerOne", nil)
	req.Header.Set("X-NFSRun-Session", strings.Repeat("99", 24))
	w := httptest.NewRecorder()
	s.handlePlayersHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad session status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestValidHTTPSessionRebuildsLauncherRegistryAfterRestart(t *testing.T) {
	key := strings.Repeat("45", 24)
	per := database.Persona{ID: 7, AccountID: 7, BlazeID: 1000007, Name: "RacerOne", SessionKey: key}
	s := New(testConfig(), &fakeStore{per: per}, log.New(io.Discard, "", 0))
	req := httptest.NewRequest(http.MethodGet, "/nfsrun/players?persona=WrongName", nil)
	req.Header.Set("X-NFSRun-Session", key)
	req.RemoteAddr = "198.51.100.55:51000"
	got, name, ok := s.resolveLauncherHTTPRequest(req, "WrongName")
	if !ok || got.ID != per.ID || name != per.Name {
		t.Fatalf("session resolve got=%+v name=%q ok=%v", got, name, ok)
	}
	resolved, _, ok := s.launchers.uniqueForPeer("198.51.100.55:52000")
	if !ok || resolved.ID != per.ID {
		t.Fatalf("registry was not rebuilt: %+v ok=%v", resolved, ok)
	}
}

func TestClaimedPersonaRejectsLegacyHTTPNameOnly(t *testing.T) {
	per := database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001, Name: "RacerOne", SessionKey: strings.Repeat("34", 24)}
	s := New(testConfig(), &claimedHTTPStore{fakeStore: fakeStore{per: per}, claimed: "RacerOne"}, log.New(io.Discard, "", 0))
	allowNameLogin = false
	defer func() { allowNameLogin = true }()

	players := httptest.NewRequest(http.MethodGet, "/nfsrun/players?persona=RacerOne", nil)
	pw := httptest.NewRecorder()
	s.handlePlayersHTTP(pw, players)
	if pw.Code != http.StatusUnauthorized {
		t.Fatalf("legacy players request status=%d body=%s", pw.Code, pw.Body.String())
	}

	presence := httptest.NewRequest(http.MethodPost, "/nfsrun/presence",
		bytes.NewBufferString(`{"persona":"RacerOne","presence":"JOINABLE","sessionId":"99"}`))
	rw := httptest.NewRecorder()
	s.handlePresenceHTTP(rw, presence)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("legacy presence request status=%d body=%s", rw.Code, rw.Body.String())
	}
}

func TestReconciledTechnicalPersonaIsHiddenFromPlayerList(t *testing.T) {
	s := New(testConfig(), &fakeStore{
		others: []database.PersonaRef{
			{ID: 1, BlazeID: 1000001, Name: "RacerOne"},
			{ID: 2, BlazeID: 1000002, Name: "EA-CLIENT-PC.0"},
			{ID: 3, BlazeID: 1000003, Name: "Rival"},
		},
	}, log.New(io.Discard, "", 0))
	s.launchers.shadow("EA-CLIENT-PC.0", 1)

	body, err := s.playersSnapshot("", false)
	if err != nil {
		t.Fatal(err)
	}
	players := body["players"].([]playerEntry)
	for _, p := range players {
		if p.Name == "EA-CLIENT-PC.0" {
			t.Fatalf("technical duplicate leaked into player list: %+v", players)
		}
	}
}
