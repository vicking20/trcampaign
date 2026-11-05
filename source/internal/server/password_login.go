package server

// Password login: POST /nfsrun/auth/password {"name","password"} (HTTPS) returns
// the same one-time game code the launcher gets, so a player can sign in with
// a name and a password the server owner handed out instead of a launcher key.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/database"
)

const (
	pwFailMax    = 8                // failed logins per address within pwFailWindow
	pwFailWindow = 10 * time.Minute // ...before the address is refused
)

type passwordStore interface {
	SetPassword(name, password string) (bool, error)
	CheckPassword(name, password string) (database.Persona, bool, error)
	IssuePasswordSession(per database.Persona, ttl time.Duration) (database.Persona, error)
}

// pwLimiter counts failures per address and per name (a name under attack
// from many addresses is slowed too).
type pwLimiter struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

func (l *pwLimiter) recent(key string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.fail[key] {
		if now.Sub(t) < pwFailWindow {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fail, key)
	} else {
		l.fail[key] = keep
	}
	return keep
}

func (l *pwLimiter) blocked(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for _, k := range keys {
		if len(l.recent(k, now)) >= pwFailMax {
			return true
		}
	}
	return false
}

func (l *pwLimiter) failed(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail == nil {
		l.fail = map[string][]time.Time{}
	}
	now := time.Now()
	for _, k := range keys {
		l.fail[k] = append(l.recent(k, now), now)
	}
}

func (l *pwLimiter) ok(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.fail, k)
	}
}

func (s *Server) handlePasswordLoginHTTP(w http.ResponseWriter, r *http.Request) {
	reply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.Method != http.MethodPost {
		reply(http.StatusMethodNotAllowed, map[string]string{"error": "method"})
		return
	}
	store, ok := s.db.(passwordStore)
	if !ok {
		reply(http.StatusNotImplemented, map[string]string{"error": "unsupported"})
		return
	}
	var req struct{ Name, Password string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		reply(http.StatusBadRequest, map[string]string{"error": "request"})
		return
	}
	name := strings.TrimSpace(req.Name)
	ipKey, nameKey := "ip:"+hostOf(r.RemoteAddr), "name:"+strings.ToLower(name)
	if s.pwLimit.blocked(ipKey, nameKey) {
		s.log.Printf("[AUTH] password login from %s refused: too many failures", hostOf(r.RemoteAddr))
		reply(http.StatusTooManyRequests, map[string]string{"error": "rate", "message": "too many failed logins, try again later"})
		return
	}
	per, valid, err := store.CheckPassword(name, req.Password)
	if err != nil {
		s.log.Printf("[AUTH] password login error: %v", err)
		reply(http.StatusServiceUnavailable, map[string]string{"error": "server"})
		return
	}
	if !valid {
		s.pwLimit.failed(ipKey, nameKey)
		s.log.Printf("[AUTH] password login failed for %q from %s", name, hostOf(r.RemoteAddr))
		reply(http.StatusUnauthorized, map[string]string{"error": "login", "message": "wrong name or password"})
		return
	}
	s.pwLimit.ok(ipKey, nameKey)
	if s.isBanned(per.Name) {
		s.log.Printf("[AUTH] password login refused: %q is banned", per.Name)
		reply(http.StatusForbidden, map[string]string{"error": "banned", "message": "this player is banned"})
		return
	}
	per, err = store.IssuePasswordSession(per, time.Duration(s.cfg.AuthSessionHours)*time.Hour)
	if err != nil {
		s.log.Printf("[AUTH] password session for %q: %v", per.Name, err)
		reply(http.StatusServiceUnavailable, map[string]string{"error": "server"})
		return
	}
	s.launchers.remember(per, r.RemoteAddr)
	code, err := s.gameCodes.issue(per.SessionKey, per.ID)
	if err != nil {
		reply(http.StatusServiceUnavailable, map[string]string{"error": "server"})
		return
	}
	s.log.Printf("[AUTH] game code issued to %q by password (%s)", per.Name, sessionFingerprint(code))
	reply(http.StatusOK, map[string]any{"code": code, "expiresIn": int(gameCodeTTL / time.Second), "name": per.Name})
}
