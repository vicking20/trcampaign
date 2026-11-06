package server

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"

	"nfsrun-server/internal/database"
)

// onlineSet: the Blaze connections that are logged in, by player name.
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

// friendsChanged: a player may be new: with FRIENDS_ALL everyone is linked as friends first.
func (s *Server) friendsChanged(relink bool) {
	if relink && s.cfg.FriendsAll {
		go func() {
			if err := s.db.LinkAllFriends(); err != nil {
				s.log.Printf("[FRIENDS] linking all players as friends failed: %v", err)
			}
		}()
	}
}

// friendRefs: the friends of a persona from the friends table; with
// FRIENDS_ALL and an empty table (old database) every other player.
func (s *Server) friendRefs(personaID uint64) ([]database.PersonaRef, error) {
	refs, err := s.db.ListFriends(personaID)
	if err == nil && len(refs) == 0 && s.cfg.FriendsAll {
		refs, err = s.db.ListPersonas(personaID, 500)
	}
	var out []database.PersonaRef
	for _, r := range refs {
		if r.ID != personaID && !isDSPersonaName(r.Name) && !s.launchers.isShadowed(r.Name, r.ID) {
			out = append(out, r)
		}
	}
	return out, err
}

func authRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
