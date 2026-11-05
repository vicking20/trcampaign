package server

import (
	"bytes"
	"log"
	"testing"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// Same shape as the live Autolog.getMultipleSpeedwalls request:
// BLID int, REQ list<struct{BLID,EVNT,TYPE,VETY}>.
func liveMultipleSpeedwallRequest(events ...uint64) []blaze.Field {
	var w blaze.Writer
	w.TagVar("BLID", 1000001)
	writeStructListHeader(&w, "REQ", len(events))
	for _, e := range events {
		w.TagVar("BLID", 1000001)
		w.TagVar("EVNT", e)
		w.TagString("TYPE", "Race")
		w.TagVar("VETY", 2)
		w.GroupEnd()
	}
	fs, err := blaze.DecodeAll(w.Bytes())
	if err != nil {
		panic(err)
	}
	return fs
}

func autologTestServer(entries []database.BoardEntry) (*Server, *syncBuf, *connState) {
	var logs syncBuf
	fs := &fakeStore{per: database.Persona{ID: 1, AccountID: 1, BlazeID: 1000001}, entries: entries}
	s := New(testConfig(), fs, log.New(&logs, "", 0))
	return s, &logs, &connState{Persona: fs.per}
}

func asStructList(t *testing.T, fs []blaze.Field, tag string) [][]blaze.Field {
	t.Helper()
	l := fieldStructList(fs, tag)
	if l == nil {
		for _, f := range fs {
			if f.Tag == tag {
				return [][]blaze.Field{}
			}
		}
		t.Fatalf("%s missing", tag)
	}
	return l
}

func TestGetMultipleSpeedwallsFromDatabase(t *testing.T) {
	entries := []database.BoardEntry{ // as LoadBoardEntries returns them: fastest first
		{Board: "race_single", EventID: 2573038515, PersonaID: 2, BlazeID: 1000002, Score: 120000, MetadataJSON: `{"vehicleId":7,"win":true,"fields":{"ATMP":3}}`},
		{Board: "race_single", EventID: 2573038515, PersonaID: 1, BlazeID: 1000001, Score: 132232, MetadataJSON: `{"vehicleId":1450737518,"win":true,"fields":{"ATMP":0}}`},
		{Board: "race_single", EventID: 3118382346, PersonaID: 1, BlazeID: 1000001, Score: 128198, MetadataJSON: `{"vehicleId":3884276172,"win":true}`},
		{Board: "run_single", EventID: 4174458999, PersonaID: 1, BlazeID: 1000001, Score: 1}, // other board: must not leak
	}
	s, logs, st := autologTestServer(entries)
	req := liveMultipleSpeedwallRequest(4174458999, 2573038515, 3118382346)
	resp := s.handleAutolog(&blaze.Packet{Component: ComponentAutolog, Command: AutologGetMultipleSpeedwalls}, req, st)
	fs, err := blaze.DecodeAll(resp.Payload)
	if err != nil {
		t.Fatal(err)
	}
	walls := asStructList(t, fs, "ROWS")
	if len(walls) != 3 {
		t.Fatalf("walls=%d want 3 (one per request, same order)", len(walls))
	}
	if n := len(asStructList(t, walls[0], "ROWS")); n != 0 {
		t.Fatalf("event 4174458999 must be empty, got %d", n)
	}
	w1 := asStructList(t, walls[1], "ROWS")
	if len(w1) != 2 || fieldUint(w1[0], "BLID") != 1000002 || fieldUint(w1[1], "TIME") != 132232 ||
		fieldUint(w1[1], "VEHI") != 1450737518 || fieldUint(w1[1], "WIN") != 1 || fieldUint(w1[0], "ATMP") != 3 || fieldUint(w1[1], "ATMP") != 1 {
		t.Fatalf("wall 2573038515 = %v", w1)
	}
	tags := []string{}
	for _, f := range w1[0] {
		tags = append(tags, f.Tag)
	}
	if want := "ATMP,BLID,BNTY,TIME,VEHI,WIN"; joinTags(tags) != want {
		t.Fatalf("row tags %s want %s", joinTags(tags), want)
	}
	w2 := asStructList(t, walls[2], "ROWS")
	if len(w2) != 1 || fieldUint(w2[0], "TIME") != 128198 {
		t.Fatalf("wall 3118382346 = %v", w2)
	}
	if !bytes.Contains(logs.Bytes(), []byte("3 events requested, 2 with times")) {
		t.Fatalf("log: %s", logs.String())
	}
}

func TestGetBestScores(t *testing.T) {
	s, _, st := autologTestServer([]database.BoardEntry{
		{Board: "race_single", EventID: 10, PersonaID: 2, BlazeID: 1000002, Score: 500},
		{Board: "race_single", EventID: 10, PersonaID: 1, BlazeID: 1000001, Score: 900},
	})
	var w blaze.Writer
	w.TagVar("BLID", 1000001)
	writeStructListHeader(&w, "REQS", 2)
	for _, e := range []uint64{10, 11} {
		w.TagVar("EVNT", e)
		w.TagString("TYPE", "Race")
		w.TagVar("UINF", 4)
		w.TagVar("VETY", 2)
		w.GroupEnd()
	}
	req, _ := blaze.DecodeAll(w.Bytes())
	fs, err := blaze.DecodeAll(s.handleAutolog(&blaze.Packet{Component: ComponentAutolog, Command: AutologGetBestScores}, req, st).Payload)
	if err != nil {
		t.Fatal(err)
	}
	sc := asStructList(t, fs, "SCRS")
	if len(sc) != 1 || fieldUint(sc[0], "EVNT") != 10 || fieldUint(sc[0], "TIME") != 900 || fieldUint(sc[0], "UINF") != 4 {
		t.Fatalf("scores=%v", sc)
	}
}

func TestFriendsPlayAndStoryReplies(t *testing.T) {
	s, _, st := autologTestServer(nil)
	var w blaze.Writer
	w.TagVar("BLID", 1000001)
	w.TagString("GEN", "Race")
	req, _ := blaze.DecodeAll(w.Bytes())
	fs, err := blaze.DecodeAll(s.handleAutolog(&blaze.Packet{Component: ComponentAutolog, Command: AutologGetFriendsPlayRecommendation}, req, st).Payload)
	if err != nil || len(fs) != 2 || fs[0].Tag != "STOR" || fs[1].Tag != "WISD" {
		t.Fatalf("fs=%v err=%v", fs, err)
	}
	fs, _ = blaze.DecodeAll(s.handleAutolog(&blaze.Packet{Component: ComponentAutolog, Command: AutologSetStoryViewed}, nil, st).Payload)
	if len(fs) != 1 || fs[0].Tag != "BTAG" {
		t.Fatalf("story reply %v", fs)
	}
	if p := s.handleAutolog(&blaze.Packet{Component: ComponentAutolog, Command: AutologGetRival}, nil, st); len(p.Payload) != 0 || p.Error != 0 {
		t.Fatal("unfilled commands keep the empty default reply")
	}
}

func TestBoardForType(t *testing.T) {
	for in, want := range map[string]string{"Race": "race_single", "Run": "run_single", "race_single": "race_single", "": ""} {
		if got := boardForType(in); got != want {
			t.Fatalf("%q -> %q want %q", in, got, want)
		}
	}
}

func joinTags(t []string) string {
	out := ""
	for i, s := range t {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

func TestSpeedwallOwnedCountPC(t *testing.T) {
	var logs syncBuf
	fs := &fakeStore{per: database.Persona{ID: 1, BlazeID: 1000001}, owned: []database.OwnedCount{{PersonaID: 1, BlazeID: 1000001, Count: 7}}}
	s := New(testConfig(), fs, log.New(&logs, "", 0))
	var w blaze.Writer
	writeVarList(&w, "BLID", []uint64{1000001, 1000002}) // live PC request shape
	req, _ := blaze.DecodeAll(w.Bytes())
	resp := s.handleAutolog(&blaze.Packet{Component: ComponentAutolog, Command: AutologGetSpeedwallOwnedCount}, req, &connState{Persona: fs.per})
	fsr, err := blaze.DecodeAll(resp.Payload)
	if err != nil {
		t.Fatal(err)
	}
	rows := fieldStructList(fsr, "SCRS")
	if len(rows) != 2 || fieldUint(rows[0], "BLID") != 1000001 || fieldUint(rows[0], "OSC") != 7 || fieldUint(rows[1], "OSC") != 0 {
		t.Fatalf("rows=%v", rows)
	}
	assertSorted(t, rows[0], "SpeedWallOwnedRow")
}
