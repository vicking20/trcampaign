package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// Player list for the launchers (the game's Origin friends).
//
// The game takes its friends from Origin (LSX QueryFriends), mirrors them into
// Blaze (AssociationLists.setUsersToList) and reads them back. Each launcher
// asks GET /nfsrun/players and reports the players as MUTUAL Origin friends.
//
// A player has one id everywhere: the Blaze id is also the Origin
// UserId/PersonaId and the external id (EXID/XREF), so presence lookups by id
// find the friend.

type onlineSet struct {
	mu sync.Mutex
	by map[*connState]string
}

func (o *onlineSet) set(st *connState, name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.by == nil {
		o.by = map[*connState]string{}
	}
	o.by[st] = name
}

func (o *onlineSet) drop(st *connState) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.by, st)
}

func (o *onlineSet) names() map[string]bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := map[string]bool{}
	for _, n := range o.by {
		out[strings.ToLower(n)] = true
	}
	return out
}

type playerEntry struct {
	Name    string `json:"name"`
	UserID  uint64 `json:"userId"`
	BlazeID uint64 `json:"blazeId"`
	Online  bool   `json:"online"`
	// Origin presence (OFFLINE/INGAME/JOINABLE) and session id
	Presence  string `json:"presence,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

func (s *Server) handlePlayersHTTP(w http.ResponseWriter, r *http.Request) {
	// launchers authenticate with their session ticket; ?persona= is the
	_, me, authenticated := s.resolveLauncherHTTPRequest(r, r.URL.Query().Get("persona"))
	if launcherSessionFromRequest(r) != "" && !authenticated {
		http.Error(w, `{"error":"session"}`, http.StatusUnauthorized)
		return
	}
	if !authenticated && me != "" {
		if err := s.refuseNameOnly(me); err != nil {
			http.Error(w, `{"error":"session"}`, http.StatusUnauthorized)
			return
		}
	}
	body, err := s.playersSnapshot(me, true)
	if err != nil {
		http.Error(w, `{"error":"db"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
