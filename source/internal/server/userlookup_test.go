package server

import (
	"io"
	"log"
	"net"
	"sync"
	"testing"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

func lookupTestServer(t *testing.T) (*Server, *connState, *connState) {
	t.Helper()
	p1 := database.Persona{ID: 1, AccountID: 11, BlazeID: 1000011, Name: "PlayerOne", SessionKey: "k1"}
	p2 := database.Persona{ID: 2, AccountID: 12, BlazeID: 1000012, Name: "PlayerTwo", SessionKey: "k2"}
	st := &fakeStore{per: p1, others: []database.PersonaRef{{ID: 1, AccountID: 11, BlazeID: 1000011, Name: "PlayerOne"}, {ID: 2, AccountID: 12, BlazeID: 1000012, Name: "PlayerTwo"}, {ID: 3, AccountID: 13, BlazeID: 1000013, Name: "NFS-DS-01"}}}
	var logs syncBuf
	s := New(testConfig(), st, log.New(&logs, "", 0))
	mk := func(per database.Persona) *connState {
		a, b := net.Pipe()
		go io.Copy(io.Discard, b)
		t.Cleanup(func() { a.Close(); b.Close() })
		cs := &connState{kind: "BLAZE", conn: a, wmu: &sync.Mutex{}, Persona: per, Authenticated: true, identified: true}
		s.online.set(cs, per.Name)
		return cs
	}
	c1, c2 := mk(p1), mk(p2)
	c2.Net = netInfo{Set: true, ExtIP: 0x01020304, ExtPort: 3659, IntIP: 0xC0A80002, IntPort: 3659}
	return s, c1, c2
}

func decodeOrFail(t *testing.T, b []byte) []blaze.Field {
	t.Helper()
	f, err := blaze.DecodeAll(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestLookupUsersFindsOtherPlayers(t *testing.T) {
	s, c1, _ := lookupTestServer(t)
	sc := blaze.GameSchema()
	var w blaze.Writer
	w.TagVar("LTYP", lookupByBlazeID)
	writeStructListHeader(&w, "ULST", 2)
	w.TagVar("ID", 1000012)
	w.GroupEnd()
	w.TagVar("ID", 999) // unknown
	w.GroupEnd()
	f := decodeOrFail(t, w.Bytes())
	resp := s.dispatch(&blaze.Packet{Component: ComponentUserSessions, Command: USLookupUsers, ID: 1}, f, false, c1)
	if issues := sc.Validate("Blaze::UserDataResponse", resp.Payload); len(issues) > 0 {
		t.Fatalf("schema: %v", issues)
	}
	out := decodeOrFail(t, resp.Payload)
	users := fieldStructList(out, "ULST")
	if len(users) != 1 {
		t.Fatalf("want 1 user, got %d: %s", len(users), blaze.FormatFieldsSafe(out, " "))
	}
	if blaze.FindString(users[0], "NAME") != "PlayerTwo" || fieldUint(users[0], "FLGS") != userStatusOnline {
		t.Fatalf("bad user %s", blaze.FormatFieldsSafe(users[0], " "))
	}

	// by persona name, offline/unknown cases
	var w2 blaze.Writer
	w2.TagVar("LTYP", lookupByPersonaName)
	writeStructListHeader(&w2, "ULST", 2)
	w2.TagString("NAME", "playertwo")
	w2.GroupEnd()
	w2.TagString("NAME", "NFS-DS-01") // race servers are not players
	w2.GroupEnd()
	resp = s.dispatch(&blaze.Packet{Component: ComponentUserSessions, Command: USLookupUsers, ID: 2}, decodeOrFail(t, w2.Bytes()), false, c1)
	if n := len(fieldStructList(decodeOrFail(t, resp.Payload), "ULST")); n != 1 {
		t.Fatalf("by name: want 1, got %d", n)
	}
}

func TestLookupUserNotFoundIsAnError(t *testing.T) {
	s, c1, _ := lookupTestServer(t)
	var w blaze.Writer
	w.TagVar("ID", 42)
	resp := s.dispatch(&blaze.Packet{Component: ComponentUserSessions, Command: USLookupUser, ID: 1}, decodeOrFail(t, w.Bytes()), false, c1)
	if resp.QType != blaze.TypeError || resp.Error != errUserNotFound {
		t.Fatalf("want USER_ERR_USER_NOT_FOUND, got q=%x err=%x", resp.QType, resp.Error)
	}
	var w2 blaze.Writer
	w2.TagVar("ID", 1000012)
	resp = s.dispatch(&blaze.Packet{Component: ComponentUserSessions, Command: USLookupUser, ID: 2}, decodeOrFail(t, w2.Bytes()), false, c1)
	if issues := blaze.GameSchema().Validate("Blaze::UserData", resp.Payload); len(issues) > 0 || resp.QType == blaze.TypeError {
		t.Fatalf("lookupUser: %v %x", issues, resp.QType)
	}
}

func TestLoginReplyIsLoginResponseWithPersonaList(t *testing.T) {
	sc := blaze.GameSchema()
	per := database.Persona{ID: 1, AccountID: 11, BlazeID: 1000011, Name: "PlayerOne", SessionKey: "k1"}
	b := buildLoginResponse(per)
	if issues := sc.Validate("Blaze::Authentication::LoginResponse", b); len(issues) > 0 {
		t.Fatalf("schema: %v", issues)
	}
	f := decodeOrFail(t, b)
	pl := fieldStructList(f, "PLST")
	if len(pl) != 1 || blaze.FindString(pl[0], "DSNM") != "PlayerOne" || fieldUint(f, "UID") != 11 || blaze.FindString(f, "SKEY") != "k1" {
		t.Fatalf("bad login response %s", blaze.FormatFieldsSafe(f, " "))
	}
}

func TestAddUsersToListResolvesMembers(t *testing.T) {
	s, c1, _ := lookupTestServer(t)
	var w blaze.Writer
	writeStructListHeader(&w, "BIDL", 1)
	w.TagVar("BLID", 1000012)
	w.GroupEnd()
	w.GroupStart("LID")
	w.TagString("LNM", "")
	w.TagVar("TYPE", 1)
	w.GroupEnd()
	resp := s.dispatch(&blaze.Packet{Component: ComponentAssociationLists, Command: ALAddUsersToList, ID: 1}, decodeOrFail(t, w.Bytes()), false, c1)
	if issues := blaze.GameSchema().Validate("Blaze::Association::UpdateListMembersResponse", resp.Payload); len(issues) > 0 {
		t.Fatalf("schema: %v", issues)
	}
	if n := len(fieldStructList(decodeOrFail(t, resp.Payload), "LMID")); n != 1 {
		t.Fatalf("want 1 member, got %d", n)
	}
}
