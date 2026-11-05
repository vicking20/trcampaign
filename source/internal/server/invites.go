package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Origin presence and invites between launchers.
//
// Real Origin carried two things for groups: the presence the game sets
// (LSX SetPresence Presence=JOINABLE SessionId=<id> when the group menu
// opens) and invites (LSX SendInvite -> the friend's Origin raises the
// MultiplayerInvite event {from, SessionInformation} on the INVITE_EVENT
// facility; schema 0x02628EA4). The launchers relay both through this server:
//
//	POST /nfsrun/presence {"persona","presence","sessionId","richPresence"}
//	POST /nfsrun/invite   {"persona","to":[userId...],"session"}
//	GET  /nfsrun/players?persona=X   adds presence/sessionId per player and
//	                                 the invites waiting for X ("invites")

type originPresence struct {
	Presence     string
	SessionID    string
	RichPresence string
	At           time.Time
}

type pendingInvite struct {
	From    uint64    `json:"from"`
	Name    string    `json:"name"`
	Session string    `json:"session"`
	At      time.Time `json:"-"`
}

type inviteBox struct {
	mu       sync.Mutex
	presence map[string]originPresence  // lower(persona) -> last SetPresence
	pending  map[uint64][]pendingInvite // target user id -> invites
}

func (b *inviteBox) init() {
	if b.presence == nil {
		b.presence = map[string]originPresence{}
		b.pending = map[uint64][]pendingInvite{}
	}
}

func (b *inviteBox) setPresence(name string, p originPresence) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	p.At = time.Now()
	b.presence[strings.ToLower(name)] = p
}

func (b *inviteBox) getPresence(name string) (originPresence, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	p, ok := b.presence[strings.ToLower(name)]
	return p, ok
}

func (b *inviteBox) add(to uint64, inv pendingInvite) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	inv.At = time.Now()
	list := b.pending[to][:0:0]
	for _, old := range b.pending[to] {
		if old.From != inv.From && time.Since(old.At) < 10*time.Minute {
			list = append(list, old)
		}
	}
	b.pending[to] = append(list, inv)
}

func (b *inviteBox) take(to uint64) []pendingInvite {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	out := b.pending[to]
	delete(b.pending, to)
	return out
}

func readSmallJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func (s *Server) handlePresenceHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST"}`, http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Persona      string `json:"persona"`
		Presence     string `json:"presence"`
		SessionID    string `json:"sessionId"`
		RichPresence string `json:"richPresence"`
	}
	if err := readSmallJSON(r, &in); err != nil {
		http.Error(w, `{"error":"body"}`, http.StatusBadRequest)
		return
	}
	persona := strings.TrimSpace(in.Persona)
	sessionKey := launcherSessionFromRequest(r)
	if sessionKey != "" {
		per, ok, err := s.personaByLauncherSession(sessionKey)
		if err != nil || !ok {
			http.Error(w, `{"error":"session"}`, http.StatusUnauthorized)
			return
		}
		persona = per.Name
		s.launchers.touch(sessionKey, r.RemoteAddr)
	} else if persona != "" {
		if err := s.refuseNameOnly(persona); err != nil {
			http.Error(w, `{"error":"session"}`, http.StatusUnauthorized)
			return
		}
	}
	if persona == "" {
		http.Error(w, `{"error":"body"}`, http.StatusBadRequest)
		return
	}
	s.invites.setPresence(persona, originPresence{Presence: strings.ToUpper(in.Presence), SessionID: in.SessionID, RichPresence: in.RichPresence})
	s.log.Printf("[ORIGIN] %q presence %s session=%q (%s)", persona, in.Presence, in.SessionID, in.RichPresence)
	s.hub.kick()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

func (s *Server) handleInviteHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST"}`, http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Persona string   `json:"persona"`
		From    uint64   `json:"from"`
		To      []uint64 `json:"to"`
		Session string   `json:"session"`
	}
	if err := readSmallJSON(r, &in); err != nil || len(in.To) == 0 || len(in.To) > 16 {
		http.Error(w, `{"error":"body"}`, http.StatusBadRequest)
		return
	}
	persona := strings.TrimSpace(in.Persona)
	from := in.From
	sessionKey := launcherSessionFromRequest(r)
	if sessionKey != "" {
		per, ok, err := s.personaByLauncherSession(sessionKey)
		if err != nil || !ok {
			http.Error(w, `{"error":"session"}`, http.StatusUnauthorized)
			return
		}
		persona, from = per.Name, per.BlazeID
		s.launchers.touch(sessionKey, r.RemoteAddr)
	} else if persona != "" {
		if err := s.refuseNameOnly(persona); err != nil {
			http.Error(w, `{"error":"session"}`, http.StatusUnauthorized)
			return
		}
	}
	if persona == "" {
		http.Error(w, `{"error":"body"}`, http.StatusBadRequest)
		return
	}
	session := in.Session
	if session == "" || session == "0" {
		if p, ok := s.invites.getPresence(persona); ok && p.SessionID != "" && p.SessionID != "0" {
			session = p.SessionID
		}
	}
	for _, to := range in.To {
		s.invites.add(to, pendingInvite{From: from, Name: persona, Session: session})
	}
	s.log.Printf("[ORIGIN] %q invites %v to session %q", persona, in.To, session)
	s.hub.kick()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

// playerPresence: what Origin shows for a player. A member of an open group
// is JOINABLE with the group's id as SessionId, whatever the launcher said.
func (s *Server) playerPresence(name string, blazeID uint64, online bool) (presence, session string) {
	if !online {
		return "OFFLINE", "0"
	}
	presence, session = "INGAME", "0"
	if p, ok := s.invites.getPresence(name); ok && p.Presence != "" {
		presence, session = p.Presence, p.SessionID
	}
	if id, joinable, ok := s.pgStatus(blazeID); ok {
		session = uintStr(uint64(id))
		if joinable {
			presence = "JOINABLE"
		} else if presence == "JOINABLE" {
			presence = "INGAME"
		}
	} else if presence == "JOINABLE" {
		// said JOINABLE but has no group (left it): only trust it for a short while
		if p, _ := s.invites.getPresence(name); time.Since(p.At) > 2*time.Minute {
			presence = "INGAME"
		}
	}
	if session == "" {
		session = "0"
	}
	return presence, session
}

func uintStr(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
