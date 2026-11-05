package server

import (
	"fmt"
	"io"
	"log"
	"strings"
	"testing"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Overlong personas are normalized server-side instead of rejecting login.
func TestPersonaNameLimit(t *testing.T) {
	if got := fitPersonaName("LONG-WORKSTATION.0"); got != "LONG-WORKSTATION" {
		t.Fatalf("machine persona normalized to %q", got)
	}

	a := fitPersonaName("ThisIsAVeryLongPlayerName-A")
	b := fitPersonaName("ThisIsAVeryLongPlayerName-B")
	if len([]rune(a)) > maxPersonaName || len([]rune(b)) > maxPersonaName || a == b {
		t.Fatalf("hashed normalization: a=%q b=%q", a, b)
	}
	if err := checkPersonaName(a); err != nil {
		t.Fatalf("normalized persona rejected: %v", err)
	}
}

func TestEAMailLoginReconcilesToUniqueLauncher(t *testing.T) {
	key := strings.Repeat("cd", 24)
	per := database.Persona{
		ID: 1, AccountID: 1, BlazeID: 1000001,
		Name: "RacerOne", Email: "eatest.RacerOne@ea.com", SessionKey: key,
	}
	var logs syncBuf
	st := &fakeStore{per: per}
	s := New(testConfig(), st, log.New(&logs, "", 0))
	s.launchers.remember(per, "198.51.100.22:61000")

	var w blaze.Writer
	w.TagVar("DVID", 0)
	w.TagString("MAIL", "eatest.EA-CLIENT-PC.0@ea.com")
	w.TagString("PASS", "ea-generated")
	w.TagString("TOKN", "")
	w.TagVar("TYPE", 0)
	fs, _ := blaze.DecodeAll(w.Bytes())
	cs := &connState{peer: "198.51.100.22:62000"}

	resp := s.handleAuth(&blaze.Packet{Component: 1, Command: 0x0028}, fs, cs)
	if resp.Error != 0 || cs.Persona.Name != "RacerOne" ||
		cs.Persona.BlazeID != 1000001 || cs.Email != "eatest.RacerOne@ea.com" {
		t.Fatalf("err=%d persona=%+v email=%q log=%s", resp.Error, cs.Persona, cs.Email, logs.String())
	}
	if !strings.Contains(logs.String(), `persona="EA-CLIENT-PC.0" -> canonical="RacerOne"`) {
		t.Fatalf("reconciliation missing: %s", logs.String())
	}
}

func TestLauncherRegistryDoesNotGuessBehindSharedNAT(t *testing.T) {
	var r launcherRegistry
	keyA := strings.Repeat("11", 24)
	keyB := strings.Repeat("22", 24)
	r.remember(database.Persona{ID: 1, Name: "PlayerA", SessionKey: keyA}, "203.0.113.40:1000")
	r.remember(database.Persona{ID: 2, Name: "PlayerB", SessionKey: keyB}, "203.0.113.40:2000")
	r.activate(keyA, "203.0.113.40:1000")
	r.activate(keyB, "203.0.113.40:2000")
	if per, _, ok := r.uniqueForPeer("203.0.113.40:3000"); ok {
		t.Fatalf("shared NAT was guessed as %+v", per)
	}
}

func TestAuthenticatedPersonaIgnoresLaterLoginPersonaName(t *testing.T) {
	key := strings.Repeat("ef", 24)
	per := database.Persona{
		ID: 1, AccountID: 1, BlazeID: 1000001,
		Name: "RacerOne", Email: "eatest.RacerOne@ea.com", SessionKey: key,
	}
	var logs syncBuf
	s := New(testConfig(), &fakeStore{per: per}, log.New(&logs, "", 0))
	cs := &connState{peer: "203.0.113.55:50000"}

	code, err := s.gameCodes.issue(key, per.ID)
	if err != nil {
		t.Fatal(err)
	}
	var auth blaze.Writer
	auth.TagString("AUTH", code)
	auth.TagVar("TYPE", 0)
	authFields, _ := blaze.DecodeAll(auth.Bytes())
	if r := s.handleAuth(&blaze.Packet{Component: 1, Command: 0x0098}, authFields, cs); r.Error != 0 {
		t.Fatalf("origin login failed: %d", r.Error)
	}

	var pn blaze.Writer
	pn.TagString("PNAM", "EA-CLIENT-PC.0")
	pnFields, _ := blaze.DecodeAll(pn.Bytes())
	r := s.handleAuth(&blaze.Packet{Component: 1, Command: 0x006e}, pnFields, cs)
	if r.Error != 0 || cs.Persona.Name != "RacerOne" || cs.Persona.BlazeID != 1000001 {
		t.Fatalf("loginPersona forked identity: err=%d persona=%+v", r.Error, cs.Persona)
	}
	if !strings.Contains(logs.String(), `asked for "EA-CLIENT-PC.0": keeping the account's persona "RacerOne"`) {
		t.Fatalf("mismatch was not documented: %s", logs.String())
	}
}

type aliasTestStore struct {
	fakeStore
	aliases  map[string]uint64
	personas map[uint64]database.Persona
	sessions map[string]database.Persona
}

func (a *aliasTestStore) PersonaByIdentityAlias(alias string) (database.Persona, bool, error) {
	id := a.aliases[strings.ToLower(alias)]
	if id == 0 {
		return database.Persona{}, false, nil
	}
	if a.personas != nil {
		if per, ok := a.personas[id]; ok {
			return per, true, nil
		}
	}
	if id == a.per.ID {
		return a.per, true, nil
	}
	return database.Persona{}, false, nil
}

func (a *aliasTestStore) PersonaBySessionKey(key string) (database.Persona, bool, error) {
	if a.sessions != nil {
		per, ok := a.sessions[key]
		return per, ok, nil
	}
	return a.fakeStore.PersonaBySessionKey(key)
}

func (a *aliasTestStore) ValidateSession(key string) bool {
	if a.sessions != nil {
		_, ok := a.sessions[key]
		return ok
	}
	return a.fakeStore.ValidateSession(key)
}

func (a *aliasTestStore) BindIdentityAlias(alias string, personaID uint64) error {
	if a.aliases == nil {
		a.aliases = map[string]uint64{}
	}
	alias = strings.ToLower(alias)
	if old, ok := a.aliases[alias]; ok && old != personaID {
		return fmt.Errorf("alias conflict")
	}
	a.aliases[alias] = personaID
	return nil
}

func TestStrongEAAliasRequiresActiveLauncherSession(t *testing.T) {
	key := strings.Repeat("56", 24)
	per := database.Persona{
		ID: 1, AccountID: 1, BlazeID: 1000001,
		Name: "RacerOne", Email: "eatest.RacerOne@ea.com", SessionKey: key,
	}
	st := &aliasTestStore{fakeStore: fakeStore{per: per}}

	// First login: one active launcher lets the server learn the opaque EA token.
	cfg := testConfig()
	allowNameLogin = false
	defer func() { allowNameLogin = true }()
	s1 := New(cfg, st, log.New(io.Discard, "", 0))
	s1.launchers.remember(per, "198.51.100.70:50000")
	var first blaze.Writer
	first.TagString("MAIL", "eatest.EA-CLIENT-PC.0@ea.com")
	first.TagString("TOKN", "opaque-ea-token-0123456789")
	first.TagVar("DVID", 0)
	firstFields, _ := blaze.DecodeAll(first.Bytes())
	cs1 := &connState{peer: "198.51.100.70:50001"}
	if r := s1.handleAuth(&blaze.Packet{Component: 1, Command: 0x0028}, firstFields, cs1); r.Error != 0 {
		t.Fatalf("first login failed: %d", r.Error)
	}
	alias := identityAliasToken("token", "opaque-ea-token-0123456789")
	if st.aliases[alias] != per.ID {
		t.Fatalf("strong alias not persisted: %#v", st.aliases)
	}

	// A persisted EA alias is only a disambiguation hint. By itself it must not
	// become a second credential that can authenticate without our launcher.
	s2 := New(cfg, st, log.New(io.Discard, "", 0))
	var second blaze.Writer
	second.TagString("MAIL", "eatest.EA-CLIENT-PC.0@ea.com")
	second.TagString("TOKN", "opaque-ea-token-0123456789")
	second.TagVar("DVID", 0)
	secondFields, _ := blaze.DecodeAll(second.Bytes())
	cs2 := &connState{peer: "203.0.113.99:60000"}
	if r := s2.handleAuth(&blaze.Packet{Component: 1, Command: 0x0028}, secondFields, cs2); r.Error == 0 || cs2.Authenticated {
		t.Fatalf("EA alias authenticated without launcher: err=%d persona=%+v", r.Error, cs2.Persona)
	}

	// Once the canonical V3 launcher reconnects at the new address, the same
	// persisted alias can select that player again.
	s2.launchers.remember(per, "203.0.113.99:61000")
	cs3 := &connState{peer: "203.0.113.99:62000"}
	if r := s2.handleAuth(&blaze.Packet{Component: 1, Command: 0x0028}, secondFields, cs3); r.Error != 0 {
		t.Fatalf("alias + active launcher failed: %d", r.Error)
	}
	if cs3.Persona.Name != "RacerOne" || cs3.Persona.BlazeID != 1000001 {
		t.Fatalf("alias + active launcher resolved %+v", cs3.Persona)
	}
}

func TestStrongEAAliasDoesNotOverrideSharedNATAmbiguity(t *testing.T) {
	keyA := strings.Repeat("31", 24)
	keyB := strings.Repeat("42", 24)
	perA := database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001, Name: "PlayerA", Email: "eatest.PlayerA@ea.com", SessionKey: keyA}
	perB := database.Persona{ID: 2, AccountID: 2, BlazeID: 1000002, Name: "PlayerB", Email: "eatest.PlayerB@ea.com", SessionKey: keyB}
	alias := identityAliasToken("token", "opaque-ea-token-player-a-0123456789")
	st := &aliasTestStore{
		fakeStore: fakeStore{per: perA},
		aliases:   map[string]uint64{alias: perA.ID},
		personas:  map[uint64]database.Persona{perA.ID: perA, perB.ID: perB},
		sessions:  map[string]database.Persona{keyA: perA, keyB: perB},
	}
	cfg := testConfig()
	allowNameLogin = false
	defer func() { allowNameLogin = true }()
	s := New(cfg, st, log.New(io.Discard, "", 0))
	s.launchers.remember(perA, "203.0.113.77:41000")
	s.launchers.remember(perB, "203.0.113.77:42000")
	s.launchers.activate(keyA, "203.0.113.77:41000")
	s.launchers.activate(keyB, "203.0.113.77:42000")

	var w blaze.Writer
	w.TagString("MAIL", "eatest.EA-CLIENT-PC.0@ea.com")
	w.TagString("TOKN", "opaque-ea-token-player-a-0123456789")
	w.TagVar("DVID", 0)
	fields, _ := blaze.DecodeAll(w.Bytes())
	cs := &connState{peer: "203.0.113.77:43000"}
	resp := s.handleAuth(&blaze.Packet{Component: 1, Command: 0x0028}, fields, cs)
	if resp.Error == 0 || cs.Authenticated {
		t.Fatalf("shared NAT alias guessed a player: err=%d auth=%v persona=%+v", resp.Error, cs.Authenticated, cs.Persona)
	}
}

func TestBareLoginPersonaReconcilesToLauncher(t *testing.T) {
	key := strings.Repeat("78", 24)
	per := database.Persona{
		ID: 1, AccountID: 1, BlazeID: 1000001,
		Name: "RacerOne", Email: "eatest.RacerOne@ea.com", SessionKey: key,
	}
	var logs syncBuf
	s := New(testConfig(), &fakeStore{per: per}, log.New(&logs, "", 0))
	s.launchers.remember(per, "198.51.100.88:41000")

	var pn blaze.Writer
	pn.TagString("PNAM", "EA-CLIENT-PC.0")
	fields, _ := blaze.DecodeAll(pn.Bytes())
	cs := &connState{peer: "198.51.100.88:42000"}
	resp := s.handleAuth(&blaze.Packet{Component: 1, Command: 0x006e}, fields, cs)
	if resp.Error != 0 || cs.Persona.Name != "RacerOne" || cs.Persona.BlazeID != 1000001 {
		t.Fatalf("bare loginPersona: err=%d persona=%+v log=%s", resp.Error, cs.Persona, logs.String())
	}
	if !strings.Contains(logs.String(), `persona "EA-CLIENT-PC.0" reconciled to canonical "RacerOne"`) {
		t.Fatalf("reconciliation log missing: %s", logs.String())
	}
}
