package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/database"
)

// Live channel to the launchers: what Origin's XMPP connection did, pushing
// friends, presence and invites as they change.
//
//	GET /nfsrun/stream?persona=X     (User-Agent nfsrun-launcher)
//
// The answer never ends; one JSON object per line:
//
//	{"type":"players","players":[...],"invites":[...]}   at once and on every change
//	{"type":"ping"}                                        every 20 s (keeps NATs open)
//
// "players" is X's friend list with online/presence/sessionId, the same as
// GET /nfsrun/players?persona=X (the polling fallback). Streams wake on logins
// and logouts, presence changes, group changes, invites and new players.

type streamSub struct {
	wake    chan struct{}
	drop    chan struct{} // closed by dropPersona (kick, reset-key, delete)
	persona string
	dropped bool
}

type eventHub struct {
	mu   sync.Mutex
	subs map[*streamSub]bool
}

func (h *eventHub) add() *streamSub {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[*streamSub]bool{}
	}
	sub := &streamSub{wake: make(chan struct{}, 1), drop: make(chan struct{})}
	h.subs[sub] = true
	return sub
}

func (h *eventHub) remove(sub *streamSub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, sub)
}

func (h *eventHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// dropPersona ends the live channels of one player. Returns how many.
func (h *eventHub) dropPersona(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for sub := range h.subs {
		if !sub.dropped && strings.EqualFold(sub.persona, name) {
			sub.dropped = true
			close(sub.drop)
			n++
		}
	}
	return n
}

// kick wakes every stream (each recomputes its own snapshot and sends it only
// when it changed).
func (h *eventHub) kick() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

// friendsChanged: something the launchers show changed. relink = a player may
// be new: with FRIENDS_ALL everyone is linked as friends first.
func (s *Server) friendsChanged(relink bool) {
	if relink && s.cfg.FriendsAll {
		go func() {
			if err := s.db.LinkAllFriends(); err != nil {
				s.log.Printf("[FRIENDS] linking all players as friends failed: %v", err)
			}
			s.hub.kick()
		}()
		return
	}
	s.hub.kick()
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

// playersSnapshot: the body of /nfsrun/players and of a stream update.
// me == "": every player (no friend filter, no invites).
func (s *Server) playersSnapshot(me string, takeInvites bool) (map[string]any, error) {
	all, err := s.db.ListPersonas(0, 1000)
	if err != nil {
		return nil, err
	}
	var self *database.PersonaRef
	for i := range all {
		if me != "" && strings.EqualFold(all[i].Name, me) {
			self = &all[i]
		}
	}
	refs := all
	if self != nil {
		if fr, ferr := s.friendRefs(self.ID); ferr == nil {
			refs = append([]database.PersonaRef{*self}, fr...)
		}
	}
	online := s.online.names()
	out := []playerEntry{}
	for _, ref := range refs {
		if isDSPersonaName(ref.Name) {
			continue // race servers are not players
		}
		if s.launchers.isShadowed(ref.Name, ref.ID) {
			continue // stale EA/hostname persona reconciled to another player
		}
		per := personaFromRef(ref)
		on := online[strings.ToLower(ref.Name)]
		pres, sess := s.playerPresence(ref.Name, per.BlazeID, on)
		out = append(out, playerEntry{Name: ref.Name, UserID: per.BlazeID, BlazeID: per.BlazeID, Online: on, Presence: pres, SessionID: sess})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Online && !out[j].Online })
	body := map[string]any{"players": out}
	if self != nil && takeInvites {
		bid := personaFromRef(*self).BlazeID
		if inv := s.invites.take(bid); len(inv) > 0 {
			body["invites"] = inv
			s.log.Printf("[ORIGIN] %d invite(s) delivered to %q", len(inv), self.Name)
		}
	}
	return body, nil
}

func (s *Server) handleStreamHTTP(w http.ResponseWriter, r *http.Request) {
	key := launcherSessionFromRequest(r)
	_, me, authenticated := s.resolveLauncherHTTPRequest(r, r.URL.Query().Get("persona"))
	fl, ok := w.(http.Flusher)
	if key != "" && !authenticated {
		http.Error(w, `{"error":"session"}`, http.StatusUnauthorized)
		return
	}
	if !authenticated && me != "" {
		if err := s.refuseNameOnly(me); err != nil {
			http.Error(w, `{"error":"session"}`, http.StatusUnauthorized)
			return
		}
	}
	if me == "" || !ok {
		http.Error(w, `{"error":"persona"}`, http.StatusBadRequest)
		return
	}
	if authenticated {
		s.launchers.activate(key, r.RemoteAddr)
		defer s.launchers.deactivate(key)
	}
	sub := s.hub.add()
	s.hub.mu.Lock()
	sub.persona = me
	s.hub.mu.Unlock()
	defer s.hub.remove(sub)
	if authenticated {
		s.log.Printf("[STREAM] %q connected from %s session=%s (%d live launcher(s))", me, r.RemoteAddr, sessionFingerprint(key), s.hub.count())
	} else {
		s.log.Printf("[STREAM] %q connected from %s by name (%d live launcher(s))", me, r.RemoteAddr, s.hub.count())
	}
	defer s.log.Printf("[STREAM] %q disconnected", me)

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var last []byte
	send := func(v any) bool {
		b, _ := json.Marshal(v)
		b = append(b, '\n')
		if _, err := w.Write(b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	update := func() bool {
		body, err := s.playersSnapshot(me, true)
		if err != nil {
			return true // db hiccup: try again on the next change
		}
		_, hasInv := body["invites"]
		players, _ := json.Marshal(body["players"])
		if !hasInv && bytes.Equal(players, last) {
			return true
		}
		last = players
		body["type"] = "players"
		return send(body)
	}
	if !update() {
		return
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		case <-sub.drop:
			return
		case <-sub.wake:
			// let a burst of changes (login = several) settle
			time.Sleep(150 * time.Millisecond)
			select {
			case <-sub.wake:
			default:
			}
			if !update() {
				return
			}
		case <-ping.C:
			if authenticated && !s.db.ValidateSession(key) {
				s.log.Printf("[STREAM] %q session=%s expired; closing live channel", me, sessionFingerprint(key))
				return
			}
			if !send(map[string]string{"type": "ping"}) {
				return
			}
		}
	}
}
