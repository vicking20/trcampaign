package server

import (
	"log"
	"testing"
	"time"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

func decodeOK(t *testing.T, b []byte) []blaze.Field {
	t.Helper()
	fs, err := blaze.DecodeAll(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return fs
}

func TestLeaderboardGroupResponse(t *testing.T) {
	fs := decodeOK(t, buildLeaderboardGroupResponse("RunRace"))
	assertSorted(t, fs, "LeaderboardGroupResponse")
	if blaze.FindString(fs, "NAME") != "RunRace" || blaze.FindString(fs, "SNAM") != "time" || fieldUintAuth(fs, "ASCD") != 1 {
		t.Fatalf("%v", fs)
	}
	if l := fieldStructList(fs, "LIST"); len(l) != 1 || blaze.FindString(l[0], "NAME") != "time" {
		t.Fatalf("LIST %v", l)
	}
}

func statsReq(t *testing.T, ids []uint64, event uint64) []blaze.Field {
	var w blaze.Writer
	w.TagVar("FILT", 1)
	writeVarList(&w, "IDLS", ids)
	w.Tag("KSUM", blaze.TDFMap)
	w.WriteByte(byte(blaze.TDFString))
	w.WriteByte(byte(blaze.TDFVarInt))
	w.Var(1)
	w.Var(7)
	w.WriteString("therun")
	w.WriteByte(0)
	w.Var(event)
	w.TagVar("LBID", 0)
	w.TagString("NAME", "")
	return decodeOK(t, w.Bytes())
}

func TestFilteredLeaderboardFromRecords(t *testing.T) {
	var logs syncBuf
	fs := &fakeStore{per: database.Persona{ID: 1, BlazeID: 1000001, Name: "Player1"}, entries: []database.BoardEntry{
		{Board: "run_single", EventID: 4129689794, PersonaID: 2, BlazeID: 1000002, Score: 800000, PersonaName: "Rival"},
		{Board: "run_single", EventID: 4129689794, PersonaID: 1, BlazeID: 1000001, Score: 872864, PersonaName: "Player1"},
	}}
	s := New(testConfig(), fs, log.New(&logs, "", 0))
	st := &connState{Persona: fs.per}
	// only me
	resp := s.handleStats(&blaze.Packet{Component: ComponentStats, Command: StatsGetFilteredLeaderboard}, statsReq(t, []uint64{1000001}, 4129689794), st)
	rows := fieldStructList(decodeOK(t, resp.Payload), "LDLS")
	if len(rows) != 1 || blaze.FindString(rows[0], "ENAM") != "Player1" || fieldUintAuth(rows[0], "RANK") != 2 || blaze.FindString(rows[0], "RSTA") != "872864" {
		t.Fatalf("rows=%v", rows)
	}
	// unfiltered page
	var w blaze.Writer
	w.TagVar("COUN", 10)
	w.Tag("KSUM", blaze.TDFMap)
	w.WriteByte(byte(blaze.TDFString))
	w.WriteByte(byte(blaze.TDFVarInt))
	w.Var(1)
	w.Var(7)
	w.WriteString("therun")
	w.WriteByte(0)
	w.Var(4129689794)
	w.TagVar("STRT", 0)
	resp = s.handleStats(&blaze.Packet{Component: ComponentStats, Command: StatsGetLeaderboard}, decodeOK(t, w.Bytes()), st)
	rows = fieldStructList(decodeOK(t, resp.Payload), "LDLS")
	if len(rows) != 2 || blaze.FindString(rows[0], "ENAM") != "Rival" || fieldUintAuth(rows[0], "RANK") != 1 {
		t.Fatalf("rows=%v", rows)
	}
	tags := []string{}
	for _, f := range rows[0] {
		tags = append(tags, f.Tag)
	}
	if joinTags(tags) != "ENAM,ENID,RANK,RSTA,RWFG,STAT,UATT" {
		t.Fatalf("row tags %v", tags)
	}
}

func TestAssociationFriendsAll(t *testing.T) {
	var logs syncBuf
	fs := &fakeStore{per: database.Persona{ID: 1, BlazeID: 1000001}, others: []database.PersonaRef{{ID: 2, BlazeID: 1000002, Name: "Rival"}}}
	s := New(testConfig(), fs, log.New(&logs, "", 0))
	var w blaze.Writer
	writeStructListHeader(&w, "ALST", 1)
	w.TagTriple("BOID", 0, 0, 0)
	w.TagVar("FLGS", 0)
	w.GroupStart("LID")
	w.TagString("LNM", "")
	w.TagVar("TYPE", 1)
	w.GroupEnd()
	w.TagVar("LMS", 100)
	w.TagVar("PRID", 0)
	w.GroupEnd()
	w.TagVar("MXRC", 4294967295)
	w.TagVar("OFRC", 0)
	st := &connState{Persona: fs.per}
	resp := s.handleAssociationLists(&blaze.Packet{Component: ComponentAssociationLists, Command: ALGetLists}, decodeOK(t, w.Bytes()), st)
	lm := fieldStructList(decodeOK(t, resp.Payload), "LMAP")
	if len(lm) != 1 {
		t.Fatalf("LMAP %v", lm)
	}
	mem := fieldStructList(lm[0], "MEML")
	if len(mem) != 1 || fieldUintAuth(lm[0], "TOCT") != 1 {
		t.Fatalf("MEML %v", mem)
	}
	lmid := usGroup(t, mem[0], "LMID")
	if fieldUintAuth(lmid, "BLID") != 1000002 || blaze.FindString(lmid, "PNAM") != "Rival" {
		t.Fatalf("LMID %v", lmid)
	}
	// The (offline) member must be announced, or the client's
	// waitForAllUsers never lets the friends service continue.
	if len(st.Pending) != 2 || st.Pending[0].Command != NotifyUserAdded || st.Pending[1].Command != NotifyUserUpdated {
		t.Fatalf("pending %+v", st.Pending)
	}
	ua := decodeOK(t, st.Pending[0].Payload)
	if usVar(t, usGroup(t, ua, "USER"), "ID") != fieldUintAuth(lmid, "BLID") {
		t.Fatal("UserAdded ID differs from the member BLID")
	}
	if fieldUintAuth(decodeOK(t, st.Pending[1].Payload), "FLGS") != 0 {
		t.Fatal("offline member announced as online")
	}
}

func TestEntitlements(t *testing.T) {
	fs := decodeOK(t, buildEntitlements([]string{"NFS12PC"}, database.Persona{ID: 7}, time.Unix(0, 0)))
	l := fieldStructList(fs, "NLST")
	if len(l) != 1 || blaze.FindString(l[0], "GNAM") != "NFS12PC" || fieldUintAuth(l[0], "STAT") != 1 || fieldUintAuth(l[0], "TYPE") != 1 || fieldUintAuth(l[0], "PID") != 7 {
		t.Fatalf("%v", l)
	}
	assertSorted(t, l[0], "Entitlement")
}
