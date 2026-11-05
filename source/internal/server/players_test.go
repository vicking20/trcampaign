package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nfsrun-server/internal/database"
)

func TestPlayersEndpointForLauncher(t *testing.T) {
	st := &fakeStore{others: []database.PersonaRef{{ID: 1, BlazeID: 1000001, Name: "PlayerOne"}, {ID: 2, BlazeID: 1000002, Name: "Rival"}, {ID: 3, BlazeID: 1000003, Name: "c4win32serv01"}}}
	cfg := testConfig()
	cfg.ClientGuard = true
	s := New(cfg, st, log.New(io.Discard, "", 0))
	s.online.set(&connState{}, "Rival")
	mux := http.NewServeMux()
	mux.HandleFunc("/nfsrun/players", s.handlePlayersHTTP)
	srv := httptest.NewServer(s.httpGuard(mux))
	defer srv.Close()

	get := func(ua string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", srv.URL+"/nfsrun/players", nil)
		req.Header.Set("User-Agent", ua)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		return r, string(b)
	}
	if r, _ := get("curl/8"); r.StatusCode != 403 {
		t.Fatalf("guard let curl in: %d", r.StatusCode)
	}
	r, body := get("nfsrun-launcher")
	if r.StatusCode != 200 {
		t.Fatalf("status %d %s", r.StatusCode, body)
	}
	var out struct {
		Players []playerEntry `json:"players"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Players) != 2 || out.Players[0].Name != "Rival" || !out.Players[0].Online || out.Players[0].UserID != 1000002 || strings.Contains(body, "c4win32") {
		t.Fatalf("players %s", body)
	}
}
