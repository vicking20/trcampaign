package server

// One-time game login codes.
//
// The game's own connections (Blaze, web Autolog) cannot be encrypted, so the
// launcher's session ticket never goes to the game. Each time the game asks
// its local Origin for an auth code, the launcher fetches a fresh code from
// POST /nfsrun/auth/gamecode (over HTTPS); Blaze login accepts it once, within
// gameCodeTTL. The Blaze connection then gets its own session key, separate
// from the launcher ticket.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/database"
)

const (
	gameCodePrefix        = "nfsrun-g1."
	gameCodeTTL           = 5 * time.Minute
	gameCodeMax           = 4096
	gameCodePerPersonaMax = 8
)

type gameCodeEntry struct {
	launcherKey string
	personaID   uint64
	expires     time.Time
}

// gameCodeRegistry keeps outstanding codes by their SHA-256, never in clear.
type gameCodeRegistry struct {
	mu sync.Mutex
	by map[string]gameCodeEntry
}

func gameCodeHash(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func (g *gameCodeRegistry) issue(launcherKey string, personaID uint64) (string, error) {
	random, err := authRandomHex(16)
	if err != nil {
		return "", err
	}
	code := gameCodePrefix + random
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.by == nil {
		g.by = map[string]gameCodeEntry{}
	}
	var oldest string
	n := 0
	for k, e := range g.by {
		if now.After(e.expires) {
			delete(g.by, k)
			continue
		}
		if e.personaID == personaID {
			n++
			if oldest == "" || e.expires.Before(g.by[oldest].expires) {
				oldest = k
			}
		}
	}
	if n >= gameCodePerPersonaMax {
		delete(g.by, oldest)
	}
	if len(g.by) >= gameCodeMax {
		return "", fmt.Errorf("too many outstanding game codes")
	}
	g.by[gameCodeHash(code)] = gameCodeEntry{launcherKey: launcherKey, personaID: personaID, expires: now.Add(gameCodeTTL)}
	return code, nil
}

// consume returns the code's entry once; a second use fails.
func (g *gameCodeRegistry) consume(code string) (gameCodeEntry, bool) {
	h := gameCodeHash(strings.TrimSpace(code))
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.by[h]
	if ok {
		delete(g.by, h)
	}
	if !ok || time.Now().After(e.expires) {
		return gameCodeEntry{}, false
	}
	return e, true
}

// isGameCode reports whether v has the game code format.
func isGameCode(v string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(v), gameCodePrefix)
	if !ok || len(rest) != 32 {
		return false
	}
	_, err := hex.DecodeString(rest)
	return err == nil
}

// handleGameCodeHTTP issues a game code to an authenticated launcher.
func (s *Server) handleGameCodeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
		return
	}
	key := launcherSessionFromRequest(r)
	per, ok, err := s.personaByLauncherSession(key)
	if key == "" || err != nil || !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"session","message":"unknown or expired launcher session"}`))
		return
	}
	s.launchers.remember(per, r.RemoteAddr)
	code, err := s.gameCodes.issue(key, per.ID)
	if err != nil {
		s.log.Printf("[AUTH] game code for %q refused: %v", per.Name, err)
		http.Error(w, `{"error":"server"}`, http.StatusServiceUnavailable)
		return
	}
	s.log.Printf("[AUTH] game code issued to %q (%s)", per.Name, sessionFingerprint(code))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "expiresIn": int(gameCodeTTL / time.Second)})
}

// loginWithGameCode logs a Blaze connection in with a game code.
func (s *Server) loginWithGameCode(state *connState, code, source string) (database.Persona, error) {
	e, ok := s.gameCodes.consume(code)
	if !ok {
		return database.Persona{}, fmt.Errorf("unknown, used or expired game code")
	}
	per, err := s.authenticateLauncherSession(state, e.launcherKey, source+" game code")
	if err != nil {
		return database.Persona{}, err
	}
	if s.isBanned(per.Name) {
		return database.Persona{}, fmt.Errorf("player %q is banned", per.Name)
	}
	return s.withGameSession(state, per)
}

type gameSessionStore interface {
	IssueGameSession(per database.Persona, ttl time.Duration) (string, error)
}

// withGameSession gives the Blaze connection its own session key, so the
// launcher ticket is never sent to the game. Stores without game sessions
// (some tests) keep the launcher key.
func (s *Server) withGameSession(state *connState, per database.Persona) (database.Persona, error) {
	store, ok := s.db.(gameSessionStore)
	if !ok {
		return per, nil
	}
	key, err := store.IssueGameSession(per, time.Duration(s.cfg.AuthSessionHours)*time.Hour)
	if err != nil {
		return database.Persona{}, fmt.Errorf("game session: %w", err)
	}
	per.SessionKey = key
	state.Persona.SessionKey = key
	return per, nil
}
