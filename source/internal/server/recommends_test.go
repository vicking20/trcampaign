package server

import (
	"testing"

	"nfsrun-server/internal/database"
)

func TestBuildRecommends(t *testing.T) {
	// split mode: wire id = blaze id
	e := func(ev, pid, score uint64) database.BoardEntry {
		return database.BoardEntry{Board: "race_single", EventID: ev, PersonaID: pid, BlazeID: 1000000 + pid, Score: score, UpdatedUnix: int64(ev)}
	}
	entries := []database.BoardEntry{
		e(1, 2, 50000),                 // never played by me -> try this
		e(2, 1, 60000), e(2, 2, 55000), // friend leads -> took top spot
		e(3, 1, 40000), e(3, 2, 45000), e(3, 3, 46000), // I lead -> nothing
		e(4, 2, 30000), e(4, 3, 31000), e(4, 1, 35000), // friend 2 top, I'm 3rd -> top spot story
		e(5, 3, 20000),                 // try this
		e(6, 2, 70000), e(6, 3, 71000), // never raced, 2 friends -> popular
	}
	st, pop := buildRecommends(entries, 1000001)
	subj := map[uint64]string{}
	for _, s := range st {
		subj[s.event] = s.subject
		if s.poster == 1000001 {
			t.Fatal("own story")
		}
	}
	if subj[1] != "friend_try_this" || subj[2] != "friend_took_top_spot" || subj[4] != "friend_took_top_spot" || subj[5] != "friend_try_this" || subj[3] != "" {
		t.Fatalf("stories %v", subj)
	}
	if len(pop) != 1 || pop[0].event != 6 || pop[0].friends != 2 || pop[0].best != 1000002 { // 3: I lead; 4: story
		t.Fatalf("popular %+v", pop)
	}
	fields := decodeOK(t, buildFriendsPlayStories("Race", st, pop))
	assertSorted(t, fields, "FriendsPlayResponse")
	stor := fieldStructList(fields, "STOR")
	if len(stor) != 4 {
		t.Fatalf("STOR %d", len(stor))
	}
	assertSorted(t, stor[0], "Story")
	assertSorted(t, fieldStructList(fields, "WISD")[0], "WisdomOfCrowdsStory")
}
