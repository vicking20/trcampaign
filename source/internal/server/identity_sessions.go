package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/database"
)

// launcherOwnershipStore tells an unclaimed persona from a launcher-owned one.
type launcherOwnershipStore interface {
	LauncherIdentityClaimed(name string) (bool, error)
}

type identityAliasStore interface {
	PersonaByIdentityAlias(alias string) (database.Persona, bool, error)
	BindIdentityAlias(alias string, personaID uint64) error
}

// launcherSessionLookupStore accepts launcher sessions only; stores without it
// fall back to PersonaBySessionKey.
type launcherSessionLookupStore interface {
	PersonaByLauncherSessionKey(key string) (database.Persona, bool, error)
}

type launcherLease struct {
	SessionKey string
	Persona    database.Persona
	IP         string
	LastSeen   time.Time
	Active     int
}

// launcherRegistry links the launcher's HTTP/LSX side to a Blaze connection.
// The session key identifies it; the IP is only a fallback for EA app/Origin
// logins that do not carry the ticket.
type launcherRegistry struct {
	mu         sync.Mutex
	byKey      map[string]*launcherLease
	shadowName map[string]uint64 // observed technical persona -> canonical persona id
}

const (
	launcherRecentTTL = 3 * time.Minute
	launcherStaleTTL  = 15 * time.Minute
)

func (r *launcherRegistry) remember(per database.Persona, addr string) {
	if strings.TrimSpace(per.SessionKey) == "" || per.ID == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		r.byKey = map[string]*launcherLease{}
	}
	now := time.Now()
	r.pruneLocked(now)
	if x := r.byKey[per.SessionKey]; x != nil {
		// keep Active when a request refreshes an open stream lease
		x.Persona = per
		if ip := hostOf(addr); ip != "" {
			x.IP = ip
		}
		x.LastSeen = now
		return
	}
	r.byKey[per.SessionKey] = &launcherLease{SessionKey: per.SessionKey, Persona: per, IP: hostOf(addr), LastSeen: now}
}

func (r *launcherRegistry) touch(key, addr string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		return
	}
	if x := r.byKey[key]; x != nil {
		x.LastSeen = time.Now()
		if ip := hostOf(addr); ip != "" {
			x.IP = ip
		}
	}
}

func (r *launcherRegistry) activate(key, addr string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		return false
	}
	x := r.byKey[key]
	if x == nil {
		return false
	}
	x.Active++
	x.LastSeen = time.Now()
	if ip := hostOf(addr); ip != "" {
		x.IP = ip
	}
	return true
}

func (r *launcherRegistry) deactivate(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if x := r.byKey[key]; x != nil {
		if x.Active > 0 {
			x.Active--
		}
		x.LastSeen = time.Now()
	}
}

// uniqueForPeer returns the single launcher persona of a public IP (active
// streams first). Two different personas on one IP: no result.
func (r *launcherRegistry) uniqueForPeer(addr string) (database.Persona, string, bool) {
	ip := hostOf(addr)
	if ip == "" {
		return database.Persona{}, "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		return database.Persona{}, "", false
	}
	now := time.Now()
	r.pruneLocked(now)

	choose := func(activeOnly bool) (database.Persona, string, bool) {
		byPersona := map[uint64]*launcherLease{}
		for _, x := range r.byKey {
			if x.IP != ip {
				continue
			}
			if activeOnly {
				if x.Active <= 0 {
					continue
				}
			} else if now.Sub(x.LastSeen) > launcherRecentTTL {
				continue
			}
			old := byPersona[x.Persona.ID]
			if old == nil || x.LastSeen.After(old.LastSeen) {
				byPersona[x.Persona.ID] = x
			}
		}
		if len(byPersona) != 1 {
			return database.Persona{}, "", false
		}
		for _, x := range byPersona {
			return x.Persona, x.SessionKey, true
		}
		return database.Persona{}, "", false
	}
	if per, key, ok := choose(true); ok {
		return per, key, true
	}
	return choose(false)
}

// candidateCountForPeer counts distinct launcher personas active or recent on
// this peer (tells "no launcher" from "shared NAT").
func (r *launcherRegistry) candidateCountForPeer(addr string) int {
	ip := hostOf(addr)
	if ip == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		return 0
	}
	now := time.Now()
	r.pruneLocked(now)
	ids := map[uint64]struct{}{}
	for _, x := range r.byKey {
		if x.IP != ip || (x.Active <= 0 && now.Sub(x.LastSeen) > launcherRecentTTL) {
			continue
		}
		ids[x.Persona.ID] = struct{}{}
	}
	return len(ids)
}

// sessionForPeerPersona returns the latest launcher ticket of one persona on
// this IP, so a stored EA alias can pick between players behind one NAT.
func (r *launcherRegistry) sessionForPeerPersona(addr string, personaID uint64) (string, bool) {
	ip := hostOf(addr)
	if ip == "" || personaID == 0 {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		return "", false
	}
	now := time.Now()
	r.pruneLocked(now)
	var best *launcherLease
	for _, x := range r.byKey {
		if x.IP != ip || x.Persona.ID != personaID || (x.Active <= 0 && now.Sub(x.LastSeen) > launcherRecentTTL) {
			continue
		}
		if best == nil || (x.Active > 0 && best.Active <= 0) || x.LastSeen.After(best.LastSeen) {
			best = x
		}
	}
	if best == nil {
		return "", false
	}
	return best.SessionKey, true
}

func (r *launcherRegistry) shadow(name string, canonicalPersonaID uint64) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || canonicalPersonaID == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shadowName == nil {
		r.shadowName = map[string]uint64{}
	}
	r.shadowName[name] = canonicalPersonaID
}

func (r *launcherRegistry) isShadowed(name string, personaID uint64) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target, ok := r.shadowName[name]
	return ok && target != 0 && target != personaID
}

// forget drops every launcher lease of a persona (reset-key, delete, rename).
func (r *launcherRegistry) forget(personaID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, x := range r.byKey {
		if x.Persona.ID == personaID {
			delete(r.byKey, k)
		}
	}
}

func (r *launcherRegistry) pruneLocked(now time.Time) {
	for k, x := range r.byKey {
		if x.Active == 0 && now.Sub(x.LastSeen) > launcherStaleTTL {
			delete(r.byKey, k)
		}
	}
}

func identityAliasToken(kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return strings.ToLower(kind) + ":" + hex.EncodeToString(sum[:16])
}

// externalAuthAlias accepts only opaque external auth values; short/static
// values would merge unrelated players.
func externalAuthAlias(kind, value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 16 {
		return ""
	}
	return identityAliasToken(kind, value)
}

func sessionFingerprint(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "none"
	}
	// logs get a hash of the token, never the token
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

func (s *Server) personaByLauncherSession(key string) (database.Persona, bool, error) {
	key = strings.TrimSpace(key)
	var (
		per database.Persona
		ok  bool
		err error
	)
	if store, strict := s.db.(launcherSessionLookupStore); strict {
		per, ok, err = store.PersonaByLauncherSessionKey(key)
	} else {
		per, ok, err = s.db.PersonaBySessionKey(key)
	}
	if err != nil || !ok {
		return database.Persona{}, ok, err
	}
	return per, true, nil
}

func (s *Server) authenticateLauncherSession(state *connState, key, source string) (database.Persona, error) {
	per, ok, err := s.personaByLauncherSession(key)
	if err != nil {
		return database.Persona{}, err
	}
	if !ok {
		return database.Persona{}, fmt.Errorf("unknown or expired launcher session")
	}
	state.Persona = per
	state.Email = per.Email
	state.Authenticated = true
	s.online.set(state, per.Name)
	// rebuild the launcher link after a server restart from the stored ticket
	s.launchers.remember(per, state.peer)
	s.friendsChanged(true)
	s.log.Printf("[AUTH] %s launcher session=%s -> persona=%q blazeId=%d", source, sessionFingerprint(key), per.Name, per.BlazeID)
	return per, nil
}

// reconcileObservedIdentity maps an EA app/Origin login to a launcher
// persona: a stored alias first, else the single launcher on the same public
// IP (then the alias is stored). It never guesses between several personas.
func (s *Server) reconcileObservedIdentity(state *connState, aliases ...string) (database.Persona, string, bool, error) {
	var cleaned []string
	for _, a := range aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" {
			cleaned = append(cleaned, a)
		}
	}

	// several launchers behind one NAT: an EA alias may not choose between them
	if n := s.launchers.candidateCountForPeer(state.peer); n > 1 {
		return database.Persona{}, "", false, fmt.Errorf("ambiguous launcher identity: %d personas are active/recent on peer %s; a launcher ticket is required", n, hostOf(state.peer))
	}

	if store, ok := s.db.(identityAliasStore); ok {
		for _, alias := range cleaned {
			per, found, err := store.PersonaByIdentityAlias(alias)
			if err != nil {
				return database.Persona{}, "", false, err
			}
			if found {
				key, active := s.launchers.sessionForPeerPersona(state.peer, per.ID)
				if !active {
					continue
				}
				live, valid, err := s.personaByLauncherSession(key)
				if err != nil {
					return database.Persona{}, "", false, err
				}
				if !valid || live.ID != per.ID {
					continue
				}
				return live, "persisted alias + active launcher " + alias, true, nil
			}
		}
	}

	hint, key, ok := s.launchers.uniqueForPeer(state.peer)
	if !ok {
		return database.Persona{}, "", false, nil
	}
	per, valid, err := s.personaByLauncherSession(key)
	if err != nil {
		return database.Persona{}, "", false, err
	}
	if !valid {
		return database.Persona{}, "", false, nil
	}
	// the registry may not switch persona if the DB session changed
	if hint.ID != 0 && hint.ID != per.ID {
		return database.Persona{}, "", false, fmt.Errorf("launcher session/persona mismatch")
	}

	if store, ok := s.db.(identityAliasStore); ok {
		for _, alias := range cleaned {
			if err := store.BindIdentityAlias(alias, per.ID); err != nil {
				// an alias seen for several personas is not stored; the player stays logged in
				s.log.Printf("[AUTH] external alias %s not bound to %q: %v", alias, per.Name, err)
			}
		}
	}
	return per, "unique launcher session on " + hostOf(state.peer), true, nil
}

func launcherSessionFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if key := strings.TrimSpace(r.Header.Get("X-NFSRun-Session")); key != "" {
		return key
	}
	return ""
}

// errNameLogin: a player name alone is never a credential.
var errNameLogin = errors.New("login by name is not allowed: use the launcher (one-time game code)")

// allowNameLogin: the protocol tests log in by name (MAIL/PNAM, ?persona=).
var allowNameLogin bool

// refuseNameOnly refuses requests that only carry a name.
func (s *Server) refuseNameOnly(name string) error {
	if strings.TrimSpace(name) == "" || allowNameLogin {
		return nil
	}
	return errNameLogin
}

// resolveLauncherHTTPRequest authenticates a launcher HTTP request by its
// session key; without one, fallbackPersona is used (tests only).
func (s *Server) resolveLauncherHTTPRequest(r *http.Request, fallbackPersona string) (database.Persona, string, bool) {
	key := launcherSessionFromRequest(r)
	if key == "" {
		return database.Persona{}, strings.TrimSpace(fallbackPersona), false
	}
	per, ok, err := s.personaByLauncherSession(key)
	if err != nil || !ok {
		return database.Persona{}, "", false
	}
	// the registry is in memory; tickets survive restarts, so rebuild the lease
	s.launchers.remember(per, r.RemoteAddr)
	return per, per.Name, true
}
