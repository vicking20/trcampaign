package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nfsrun-server/internal/database"
)

type v3AuthTestStore struct {
	multiStore
	v3mu        sync.Mutex
	byPub       map[string]database.Persona
	ownerByName map[string]string
	sessions    map[string]database.Persona
	// Blaze session keys of game connections
	gameSessions map[string]database.Persona
	seq          int
}

func (s *v3AuthTestStore) nextSession(per database.Persona) (database.Persona, int64) {
	s.seq++
	key := fmt.Sprintf("%048x", s.seq)
	per.SessionKey = key
	if s.sessions == nil {
		s.sessions = map[string]database.Persona{}
	}
	s.sessions[key] = per
	return per, time.Now().Add(8 * time.Hour).Unix()
}

func (s *v3AuthTestStore) LauncherPublicKeyKnown(publicKey string) (bool, error) {
	s.v3mu.Lock()
	defer s.v3mu.Unlock()
	_, ok := s.byPub[publicKey]
	return ok, nil
}

func (s *v3AuthTestStore) AuthenticateLauncherPublicKey(email, name, publicKey, claimCode, authMode string, ttl time.Duration) (database.Persona, int64, error) {
	s.v3mu.Lock()
	defer s.v3mu.Unlock()
	if s.byPub == nil {
		s.byPub = map[string]database.Persona{}
		s.ownerByName = map[string]string{}
	}
	if per, ok := s.byPub[publicKey]; ok {
		per, exp := s.nextSession(per)
		s.byPub[publicKey] = per
		return per, exp, nil
	}
	if owner := s.ownerByName[strings.ToLower(name)]; owner != "" && owner != publicKey {
		return database.Persona{}, 0, fmt.Errorf("persona %q is already claimed by another launcher installation", name)
	}
	if authMode == "invite" && strings.ToUpper(strings.ReplaceAll(claimCode, "-", "")) != "ABCDE12345" {
		return database.Persona{}, 0, fmt.Errorf("invalid or expired invite code for %q", name)
	}
	per, err := s.multiStore.GetOrCreateIdentity(email, name)
	if err != nil {
		return database.Persona{}, 0, err
	}
	per, exp := s.nextSession(per)
	s.byPub[publicKey] = per
	s.ownerByName[strings.ToLower(name)] = publicKey
	return per, exp, nil
}

func (s *v3AuthTestStore) RenewLauncherSession(publicKey, sessionKey string, ttl time.Duration) (database.Persona, int64, bool, error) {
	s.v3mu.Lock()
	defer s.v3mu.Unlock()
	per, ok := s.sessions[sessionKey]
	if !ok {
		return database.Persona{}, 0, false, nil
	}
	owned, ok := s.byPub[publicKey]
	if !ok || owned.ID != per.ID {
		return database.Persona{}, 0, false, nil
	}
	per, exp := s.nextSession(per)
	s.byPub[publicKey] = per
	return per, exp, true, nil
}

func (s *v3AuthTestStore) PersonaBySessionKey(key string) (database.Persona, bool, error) {
	s.v3mu.Lock()
	defer s.v3mu.Unlock()
	per, ok := s.sessions[key]
	return per, ok, nil
}

func (s *v3AuthTestStore) PersonaByLauncherSessionKey(key string) (database.Persona, bool, error) {
	return s.PersonaBySessionKey(key)
}

func (s *v3AuthTestStore) IssueGameSession(per database.Persona, ttl time.Duration) (string, error) {
	s.v3mu.Lock()
	defer s.v3mu.Unlock()
	s.seq++
	key := fmt.Sprintf("b%047x", s.seq)
	per.SessionKey = key
	if s.gameSessions == nil {
		s.gameSessions = map[string]database.Persona{}
	}
	s.gameSessions[key] = per
	return key, nil
}

func (s *v3AuthTestStore) ValidateSession(key string) bool {
	s.v3mu.Lock()
	defer s.v3mu.Unlock()
	_, ok := s.sessions[key]
	return ok
}

func authChallengeForTest(t *testing.T, s *Server, persona, pub string) launcherAuthChallenge {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"persona": persona, "publicKey": pub})
	req := httptest.NewRequest(http.MethodPost, "/nfsrun/auth/challenge", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.20:50000"
	w := httptest.NewRecorder()
	s.handleLauncherAuthChallenge(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("challenge status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		ChallengeID   string `json:"challengeId"`
		Nonce         string `json:"nonce"`
		Persona       string `json:"persona"`
		ClaimRequired bool   `json:"claimRequired"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return launcherAuthChallenge{ID: out.ChallengeID, Nonce: out.Nonce, Persona: out.Persona, PublicKey: pub}
}

func challengeClaimRequiredForTest(t *testing.T, s *Server, persona, pub string) bool {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"persona": persona, "publicKey": pub})
	req := httptest.NewRequest(http.MethodPost, "/nfsrun/auth/challenge", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.20:50000"
	w := httptest.NewRecorder()
	s.handleLauncherAuthChallenge(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("challenge status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		ClaimRequired bool `json:"claimRequired"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.ClaimRequired
}

func authSessionForTest(t *testing.T, s *Server, c launcherAuthChallenge, priv ed25519.PrivateKey, claim, current string) *httptest.ResponseRecorder {
	t.Helper()
	sig := ed25519.Sign(priv, launcherAuthMessage(c.ID, c.Nonce, c.Persona, c.PublicKey, claim, current))
	body, _ := json.Marshal(map[string]string{
		"challengeId": c.ID, "persona": c.Persona, "publicKey": c.PublicKey,
		"signature": hex.EncodeToString(sig), "claimCode": claim, "currentSession": current,
	})
	req := httptest.NewRequest(http.MethodPost, "/nfsrun/auth/session", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.20:50001"
	w := httptest.NewRecorder()
	s.handleLauncherAuthSession(w, req)
	return w
}

type strictLauncherLookupStore struct {
	fakeStore
	legacy database.Persona
}

func (s *strictLauncherLookupStore) PersonaBySessionKey(key string) (database.Persona, bool, error) {
	if key == "legacy-session" {
		return s.legacy, true, nil
	}
	return database.Persona{}, false, nil
}

func (s *strictLauncherLookupStore) PersonaByLauncherSessionKey(key string) (database.Persona, bool, error) {
	return database.Persona{}, false, nil
}

func TestLauncherChallengePerIPLimit(t *testing.T) {
	var r launcherChallengeRegistry
	for i := 0; i < launcherChallengePerIPMax; i++ {
		if _, err := r.issue("Player", strings.Repeat("ab", 32), "203.0.113.90:1234"); err != nil {
			t.Fatalf("challenge %d: %v", i, err)
		}
	}
	if _, err := r.issue("Player", strings.Repeat("ab", 32), "203.0.113.90:9999"); err == nil {
		t.Fatal("per-IP challenge limit was not enforced")
	}
	if _, err := r.issue("Player", strings.Repeat("ab", 32), "203.0.113.91:1234"); err != nil {
		t.Fatalf("different peer was incorrectly blocked: %v", err)
	}
}

func TestLauncherSessionLookupDoesNotPromoteLegacySession(t *testing.T) {
	st := &strictLauncherLookupStore{legacy: database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001, Name: "Legacy"}}
	s := New(testConfig(), st, log.New(io.Discard, "", 0))
	if _, ok, err := s.personaByLauncherSession("legacy-session"); err != nil || ok {
		t.Fatalf("legacy session became launcher authority: ok=%v err=%v", ok, err)
	}
}

func TestLauncherAuthV3SignedClaimCanonicalAndRenew(t *testing.T) {
	cfg := testConfig()
	cfg.AuthMode = "invite"
	cfg.FriendsAll = false
	st := &v3AuthTestStore{}
	s := New(cfg, st, log.New(io.Discard, "", 0))

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)

	if !challengeClaimRequiredForTest(t, s, "RacerOne", pubHex) {
		t.Fatal("new key in invite mode did not require first-pair claim code")
	}
	c := authChallengeForTest(t, s, "RacerOne", pubHex)
	w := authSessionForTest(t, s, c, priv, "ABCDE-12345", "")
	if w.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", w.Code, w.Body.String())
	}
	var first serverIdentityForTest
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Name != "RacerOne" || first.BlazeID == 0 || len(first.SessionKey) != 48 {
		t.Fatalf("first auth=%+v", first)
	}

	// A known key in invite mode no longer needs the one-time code.
	if challengeClaimRequiredForTest(t, s, "DifferentName", pubHex) {
		t.Fatal("known launcher key was asked for a second invite claim code")
	}

	// A changed INI name cannot fork the same launcher key into another persona.
	c = authChallengeForTest(t, s, "DifferentName", pubHex)
	w = authSessionForTest(t, s, c, priv, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", w.Code, w.Body.String())
	}
	var resumed serverIdentityForTest
	_ = json.Unmarshal(w.Body.Bytes(), &resumed)
	if resumed.Name != "RacerOne" {
		t.Fatalf("same key forked persona: %+v", resumed)
	}

	// Renewal proves the same private key and rotates the bearer ticket.
	c = authChallengeForTest(t, s, "RacerOne", pubHex)
	w = authSessionForTest(t, s, c, priv, "", resumed.SessionKey)
	if w.Code != http.StatusOK {
		t.Fatalf("renew status=%d body=%s", w.Code, w.Body.String())
	}
	var renewed serverIdentityForTest
	_ = json.Unmarshal(w.Body.Bytes(), &renewed)
	if renewed.SessionKey == resumed.SessionKey {
		t.Fatalf("renew did not rotate bearer ticket: %q", renewed.SessionKey)
	}
	if _, ok := st.sessions[resumed.SessionKey]; !ok {
		t.Fatal("previous ticket was removed before its natural expiry")
	}
}

type serverIdentityForTest struct {
	Name       string `json:"name"`
	BlazeID    uint64 `json:"blazeId"`
	SessionKey string `json:"sessionKey"`
	ExpiresAt  int64  `json:"expiresAt"`
}

func TestLauncherAuthV3RejectsBadSignatureAndReplay(t *testing.T) {
	cfg := testConfig()
	cfg.AuthMode = "open"
	cfg.FriendsAll = false
	s := New(cfg, &v3AuthTestStore{}, log.New(io.Discard, "", 0))
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pubHex := hex.EncodeToString(pub)
	c := authChallengeForTest(t, s, "PlayerOne", pubHex)

	bad := append(ed25519.PrivateKey(nil), priv...)
	bad[len(bad)-1] ^= 0xff
	w := authSessionForTest(t, s, c, bad, "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status=%d body=%s", w.Code, w.Body.String())
	}
	// Even the correct signature cannot reuse the consumed challenge.
	w = authSessionForTest(t, s, c, priv, "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replay status=%d body=%s", w.Code, w.Body.String())
	}
}
