package server

import (
	"crypto/ed25519"
	"crypto/rand"
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
	launcherAuthVersion       = "nfsrun-auth-v3"
	launcherChallengeTTL      = 60 * time.Second
	launcherChallengeMax      = 2048
	launcherChallengePerIPMax = 32
	launcherRenewFraction     = 2 // rotate after roughly half of the ticket lifetime
)

type launcherV3Store interface {
	AuthenticateLauncherPublicKey(email, name, publicKey, claimCode, authMode string, ttl time.Duration) (database.Persona, int64, error)
	RenewLauncherSession(publicKey, sessionKey string, ttl time.Duration) (database.Persona, int64, bool, error)
}

type launcherV3ChallengeStore interface {
	LauncherPublicKeyKnown(publicKey string) (bool, error)
}

type launcherAuthChallenge struct {
	ID        string
	Nonce     string
	Persona   string
	PublicKey string
	IP        string
	ExpiresAt time.Time
}

type launcherChallengeRegistry struct {
	mu sync.Mutex
	by map[string]launcherAuthChallenge
}

func authRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (r *launcherChallengeRegistry) issue(persona, publicKey, addr string) (launcherAuthChallenge, error) {
	id, err := authRandomHex(16)
	if err != nil {
		return launcherAuthChallenge{}, err
	}
	nonce, err := authRandomHex(32)
	if err != nil {
		return launcherAuthChallenge{}, err
	}
	c := launcherAuthChallenge{
		ID:        id,
		Nonce:     nonce,
		Persona:   persona,
		PublicKey: strings.ToLower(publicKey),
		IP:        hostOf(addr),
		ExpiresAt: time.Now().Add(launcherChallengeTTL),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.by == nil {
		r.by = make(map[string]launcherAuthChallenge)
	}
	now := time.Now()
	peerPending := 0
	for k, old := range r.by {
		if now.After(old.ExpiresAt) {
			delete(r.by, k)
			continue
		}
		if c.IP != "" && old.IP == c.IP {
			peerPending++
		}
	}
	if len(r.by) >= launcherChallengeMax {
		return launcherAuthChallenge{}, fmt.Errorf("too many pending auth challenges")
	}
	if c.IP != "" && peerPending >= launcherChallengePerIPMax {
		return launcherAuthChallenge{}, fmt.Errorf("too many pending auth challenges for peer")
	}
	r.by[c.ID] = c
	return c, nil
}

// consume is deliberately one-shot even when signature verification fails.
// Replays therefore cannot reuse a captured challenge/signature pair.
func (r *launcherChallengeRegistry) consume(id, addr string) (launcherAuthChallenge, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.by == nil {
		return launcherAuthChallenge{}, false
	}
	c, ok := r.by[id]
	if ok {
		delete(r.by, id)
	}
	if !ok || time.Now().After(c.ExpiresAt) {
		return launcherAuthChallenge{}, false
	}
	if c.IP != "" && c.IP != hostOf(addr) {
		return launcherAuthChallenge{}, false
	}
	return c, true
}

func normalizePublicKey(v string) (string, ed25519.PublicKey, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) != ed25519.PublicKeySize*2 {
		return "", nil, false
	}
	b, err := hex.DecodeString(v)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return "", nil, false
	}
	return v, ed25519.PublicKey(b), true
}

func normalizeSignature(v string) ([]byte, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) != ed25519.SignatureSize*2 {
		return nil, false
	}
	b, err := hex.DecodeString(v)
	return b, err == nil && len(b) == ed25519.SignatureSize
}

func normalizeClaimForSignature(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	return strings.ReplaceAll(v, "-", "")
}

func launcherAuthMessage(challengeID, nonce, persona, publicKey, claimCode, currentSession string) []byte {
	return []byte(strings.Join([]string{
		launcherAuthVersion,
		strings.ToLower(strings.TrimSpace(challengeID)),
		strings.ToLower(strings.TrimSpace(nonce)),
		strings.TrimSpace(persona),
		strings.ToLower(strings.TrimSpace(publicKey)),
		normalizeClaimForSignature(claimCode),
		strings.ToLower(strings.TrimSpace(currentSession)),
	}, "\n"))
}

func (s *Server) handleLauncherAuthChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Persona   string `json:"persona"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		http.Error(w, `{"error":"json"}`, http.StatusBadRequest)
		return
	}
	requested := strings.TrimSpace(in.Persona)
	if requested == "" || len(requested) > 256 {
		http.Error(w, `{"error":"persona"}`, http.StatusBadRequest)
		return
	}
	persona := fitPersonaName(requested)
	if err := checkPersonaName(persona); err != nil {
		http.Error(w, `{"error":"persona"}`, http.StatusBadRequest)
		return
	}
	pubHex, _, ok := normalizePublicKey(in.PublicKey)
	if !ok {
		http.Error(w, `{"error":"public_key"}`, http.StatusBadRequest)
		return
	}
	// invite: every new key needs a code. open: only a name with a pending
	// code (invite / reset-key) needs it.
	claimRequired := s.cfg.AuthMode == "invite"
	reserved, isReservable := s.db.(interface {
		LauncherNameReserved(name string) (bool, error)
	})
	if claimRequired || isReservable {
		if store, ok := s.db.(launcherV3ChallengeStore); ok {
			known, err := store.LauncherPublicKeyKnown(pubHex)
			if err != nil {
				http.Error(w, `{"error":"server","message":"could not check launcher ownership"}`, http.StatusServiceUnavailable)
				return
			}
			switch {
			case known:
				claimRequired = false
			case !claimRequired && isReservable:
				claimRequired, _ = reserved.LauncherNameReserved(persona)
			}
		}
	}
	c, err := s.authChallenges.issue(persona, pubHex, r.RemoteAddr)
	if err != nil {
		http.Error(w, `{"error":"challenge"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Version       string `json:"version"`
		ChallengeID   string `json:"challengeId"`
		Nonce         string `json:"nonce"`
		Persona       string `json:"persona"`
		AuthMode      string `json:"authMode"`
		ClaimRequired bool   `json:"claimRequired"`
		ExpiresIn     int    `json:"expiresIn"`
		SessionHours  int    `json:"sessionHours"`
	}{
		Version: launcherAuthVersion, ChallengeID: c.ID, Nonce: c.Nonce, Persona: persona,
		AuthMode: s.cfg.AuthMode, ClaimRequired: claimRequired,
		ExpiresIn: int(launcherChallengeTTL / time.Second), SessionHours: s.cfg.AuthSessionHours,
	})
}

func (s *Server) handleLauncherAuthSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		ChallengeID    string `json:"challengeId"`
		Persona        string `json:"persona"`
		PublicKey      string `json:"publicKey"`
		Signature      string `json:"signature"`
		ClaimCode      string `json:"claimCode,omitempty"`
		CurrentSession string `json:"currentSession,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
		http.Error(w, `{"error":"json"}`, http.StatusBadRequest)
		return
	}
	c, ok := s.authChallenges.consume(in.ChallengeID, r.RemoteAddr)
	if !ok {
		http.Error(w, `{"error":"challenge","message":"unknown, expired or replayed challenge"}`, http.StatusUnauthorized)
		return
	}
	persona := fitPersonaName(strings.TrimSpace(in.Persona))
	pubHex, pub, ok := normalizePublicKey(in.PublicKey)
	if !ok || !strings.EqualFold(pubHex, c.PublicKey) || !strings.EqualFold(persona, c.Persona) {
		http.Error(w, `{"error":"challenge","message":"challenge data mismatch"}`, http.StatusUnauthorized)
		return
	}
	sig, ok := normalizeSignature(in.Signature)
	if !ok || !ed25519.Verify(pub, launcherAuthMessage(c.ID, c.Nonce, c.Persona, c.PublicKey, in.ClaimCode, in.CurrentSession), sig) {
		http.Error(w, `{"error":"signature","message":"launcher key proof failed"}`, http.StatusUnauthorized)
		return
	}
	store, ok := s.db.(launcherV3Store)
	if !ok {
		http.Error(w, `{"error":"server","message":"database does not support launcher auth v3"}`, http.StatusNotImplemented)
		return
	}
	ttl := time.Duration(s.cfg.AuthSessionHours) * time.Hour
	var per database.Persona
	var expires int64
	var err error
	if current := strings.TrimSpace(in.CurrentSession); current != "" {
		var renewed bool
		per, expires, renewed, err = store.RenewLauncherSession(pubHex, current, ttl)
		if err == nil && !renewed {
			// Stale/expired ticket: the public-key credential still proves identity,
			// so fall through to issuing a fresh ticket.
			per, expires, err = store.AuthenticateLauncherPublicKey("eatest."+c.Persona+"@ea.com", c.Persona, pubHex, in.ClaimCode, s.cfg.AuthMode, ttl)
		}
	} else {
		per, expires, err = store.AuthenticateLauncherPublicKey("eatest."+c.Persona+"@ea.com", c.Persona, pubHex, in.ClaimCode, s.cfg.AuthMode, ttl)
	}
	if err != nil {
		s.log.Printf("[AUTHV3] persona=%q publicKey=%s refused: %v", c.Persona, pubHex[:8], err)
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusConflict
		if strings.Contains(strings.ToLower(err.Error()), "invite code") {
			status = http.StatusUnauthorized
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "launcher_auth", "message": err.Error()})
		return
	}
	s.launchers.remember(per, r.RemoteAddr)
	if s.cfg.FriendsAll {
		if err := s.db.LinkAllFriends(); err != nil {
			s.log.Printf("[FRIENDS] linking all players as friends failed: %v", err)
		}
		s.hub.kick()
	}
	if expires == 0 {
		expires = time.Now().Add(ttl).Unix()
	}
	renewAfter := time.Now().Add(ttl / launcherRenewFraction).Unix()
	s.log.Printf("[AUTHV3] launcher authenticated persona=%q blazeId=%d publicKey=%s session=%s expires=%s",
		per.Name, per.BlazeID, pubHex[:8], sessionFingerprint(per.SessionKey), time.Unix(expires, 0).Format(time.RFC3339))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Name       string `json:"name"`
		UserID     uint64 `json:"userId"`
		BlazeID    uint64 `json:"blazeId"`
		SessionKey string `json:"sessionKey"`
		ExpiresAt  int64  `json:"expiresAt"`
		RenewAfter int64  `json:"renewAfter"`
	}{
		Name: per.Name, UserID: per.BlazeID, BlazeID: per.BlazeID, SessionKey: per.SessionKey,
		ExpiresAt: expires, RenewAfter: renewAfter,
	})
}
