package server

import (
	"fmt"
	"log"
	"net"
	"testing"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// fakeStore mimics database.DB for one account/persona.
type fakeStore struct {
	per     database.Persona
	reports map[string]string // sha -> game type
	bests   map[string]uint64 // board/event/persona -> best time
	entries []database.BoardEntry
	others  []database.PersonaRef
	locker  []database.LockerContent
	owned   []database.OwnedCount
	friends map[uint64][]uint64 // optional friends table (persona id -> ids)
}

func (f *fakeStore) PersonaBySessionKey(key string) (database.Persona, bool, error) {
	if key != "" && key == f.per.SessionKey {
		return f.per, true, nil
	}
	return database.Persona{}, false, nil
}
func (f *fakeStore) CreateLockerContent(personaID uint64, category, name, desc, attrsJSON, subNames string, perm uint64) (database.LockerContent, error) {
	c := database.LockerContent{ID: uint64(len(f.locker) + 1), PersonaID: personaID, BlazeID: f.per.BlazeID, OwnerName: f.per.Name,
		Category: category, Name: name, Description: desc, AttributesJSON: attrsJSON, SubNames: subNames, Permission: perm,
		Status: 1, UploadToken: "0123456789abcdef0123456789abcdef", Created: 1, Updated: 1}
	f.locker = append(f.locker, c)
	return c, nil
}
func (f *fakeStore) GetLockerContent(id uint64) (database.LockerContent, bool, error) {
	if id == 0 || int(id) > len(f.locker) {
		return database.LockerContent{}, false, nil
	}
	return f.locker[id-1], true, nil
}
func (f *fakeStore) SetLockerStatus(id, status, size uint64) error {
	f.locker[id-1].Status = status
	if size > 0 {
		f.locker[id-1].Size = size
	}
	return nil
}
func (f *fakeStore) UpdateLockerContent(id, ownerID uint64, name, desc, attrsJSON string) error {
	f.locker[id-1].Name, f.locker[id-1].Description, f.locker[id-1].AttributesJSON = name, desc, attrsJSON
	return nil
}
func (f *fakeStore) SpeedwallOwnedCounts() ([]database.OwnedCount, error)    { return f.owned, nil }
func (f *fakeStore) DeleteLockerContents(ownerID uint64, ids []uint64) error { return nil }
func (f *fakeStore) ListLockerContents(category string, ownerID uint64, includePrivate bool, offset, limit int) ([]database.LockerContent, uint64, error) {
	var out []database.LockerContent
	for _, c := range f.locker {
		if c.Category == category && c.Status == 0 {
			out = append(out, c)
		}
	}
	return out, uint64(len(out)), nil
}

func (f *fakeStore) Ping() error                     { return nil }
func (f *fakeStore) Touch()                          {}
func (f *fakeStore) ValidateSession(key string) bool { return key == f.per.SessionKey }
func (f *fakeStore) Migrate() error                  { return nil }
func (f *fakeStore) SaveGameReport(personaID uint64, gameType, sha, payloadHex, resultsJSON string) (bool, error) {
	if f.reports == nil {
		f.reports = map[string]string{}
	}
	if _, dup := f.reports[sha]; dup {
		return false, nil
	}
	f.reports[sha] = gameType
	return true, nil
}
func (f *fakeStore) UpsertPersonalBest(personaID uint64, board string, eventID, timeMs uint64, meta string) (bool, error) {
	if f.bests == nil {
		f.bests = map[string]uint64{}
	}
	k := fmt.Sprintf("%s/%d/%d", board, eventID, personaID)
	old, ok := f.bests[k]
	if !ok || timeMs < old {
		f.bests[k] = timeMs
		return true, nil
	}
	return false, nil
}
func (f *fakeStore) LoadBoardEntries(boards []string, events []uint64) ([]database.BoardEntry, error) {
	return f.entries, nil
}
func (f *fakeStore) LoadBoard(board string) ([]database.BoardEntry, error) {
	var out []database.BoardEntry
	for _, e := range f.entries {
		if e.Board == board {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeStore) ListPersonas(excludeID uint64, limit int) ([]database.PersonaRef, error) {
	return f.others, nil
}
func (f *fakeStore) ListFriends(personaID uint64) ([]database.PersonaRef, error) {
	var out []database.PersonaRef
	if f.friends != nil {
		for _, id := range f.friends[personaID] {
			for _, o := range f.others {
				if o.ID == id {
					out = append(out, o)
				}
			}
		}
		return out, nil
	}
	for _, o := range f.others {
		if o.ID != personaID {
			out = append(out, o)
		}
	}
	return out, nil
}
func (f *fakeStore) LinkAllFriends() error { return nil }
func (f *fakeStore) GetOrCreateIdentity(email, name string) (database.Persona, error) {
	p := f.per
	p.Email = email
	if name != "" {
		p.Name = name
	}
	return p, nil
}

func req(id, comp, cmd uint16, w *blaze.Writer) []byte {
	var payload []byte
	if w != nil {
		payload = w.Bytes()
	}
	p := &blaze.Packet{Component: comp, Command: cmd, QType: blaze.TypeRequest, ID: id, Payload: payload}
	return p.Bytes()
}

// runFlow replays the exact retail sequence captured in logs/server.log
// and returns every packet the server wrote back.
func runFlow(t *testing.T) []*blaze.Packet {
	t.Helper()
	cfg := testConfig()
	cfg.LogTDF = true
	var logs syncBuf
	s := New(cfg, &fakeStore{per: database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001, Name: "EXAMPLE-CLIENT1", SessionKey: "sk"}}, log.New(&logs, "", 0))

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() { s.handleConn(server, false); close(done) }()

	var login, pnam blaze.Writer
	login.TagVar("DVID", 0)
	login.TagString("MAIL", "eatest.EXAMPLE-CLIENT1@ea.com")
	login.TagString("PASS", "x")
	login.TagString("TOKN", "y")
	login.TagVar("TYPE", 0)
	pnam.TagString("PNAM", "EXAMPLE-CLIENT1")

	var stream []byte
	var pre blaze.Writer // live preAuth carries the client identity checked by the guard
	pre.GroupStart("CINF")
	pre.TagString("BSDK", "3.15.2.0")
	pre.TagString("CLNT", "nfs client")
	pre.GroupEnd()
	stream = append(stream, req(0, 0x0009, 0x0007, &pre)...) // preAuth
	stream = append(stream, req(1, 0x0009, 0x0002, nil)...)  // ping
	stream = append(stream, req(2, 0x0001, 0x0028, &login)...)
	stream = append(stream, req(3, 0x0001, 0x0050, &pnam)...)
	stream = append(stream, req(4, 0x0001, 0x006E, &pnam)...)
	stream = append(stream, req(5, 0x0009, 0x0008, nil)...) // postAuth

	go func() { client.Write(stream); time.Sleep(50 * time.Millisecond); client.Close() }()

	var out []*blaze.Packet
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		p, err := blaze.ReadPacket(client)
		if err != nil { // io.EOF / closed pipe once the client side is done
			break
		}
		out = append(out, p)
		if len(out) == 8 {
			break
		}
	}
	client.Close()
	<-done
	if testing.Verbose() {
		t.Log(logs.String())
	}
	return out
}

func TestRetailFlowSendsUserSessionNotifications(t *testing.T) {
	out := runFlow(t)
	type want struct {
		comp, cmd, qtype uint16
	}
	exp := []want{
		{0x0009, 0x0007, blaze.TypeResponse},
		{0x0009, 0x0002, blaze.TypeResponse},
		{0x0001, 0x0028, blaze.TypeResponse},
		{0x0001, 0x0050, blaze.TypeResponse},
		{0x0001, 0x006E, blaze.TypeResponse}, // loginPersona reply first..
		{0x7802, 0x0002, blaze.TypeNotify},   // ...then UserAdded
		{0x7802, 0x0005, blaze.TypeNotify},   // ...then UserUpdated
		{0x0009, 0x0008, blaze.TypeResponse},
	}
	if len(out) != len(exp) {
		t.Fatalf("got %d packets want %d", len(out), len(exp))
	}
	for i, e := range exp {
		if out[i].Component != e.comp || out[i].Command != e.cmd || out[i].QType != e.qtype {
			t.Fatalf("packet %d = comp 0x%04X cmd 0x%04X type 0x%04X, want %+v", i, out[i].Component, out[i].Command, out[i].QType, e)
		}
	}
	// UserAdded USER.ID must equal the SessionInfo BUID returned by loginPersona.
	lp, _ := blaze.DecodeAll(out[4].Payload)
	ua, _ := blaze.DecodeAll(out[5].Payload)
	buid := usVar(t, lp, "BUID")
	if got := usVar(t, usGroup(t, ua, "USER"), "ID"); got != buid || buid != 1000001 {
		t.Fatalf("UserAdded ID=%d BUID=%d", got, buid)
	}
}
