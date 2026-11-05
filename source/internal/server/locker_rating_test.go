package server

import (
	"log"
	"testing"

	"nfsrun-server/internal/blaze"
	"nfsrun-server/internal/database"
)

// ratingStore adds the optional rating/view-count methods to fakeStore.
type ratingStore struct {
	*fakeStore
	ratings map[[2]uint64]int64 // content, persona -> rating
}

func (r *ratingStore) RateLockerContent(cid, pid uint64, rating int64) error {
	r.ratings[[2]uint64{cid, pid}] = rating
	c := &r.locker[cid-1]
	c.RatingCount, c.RatingSum = 0, 0
	for k, v := range r.ratings {
		if k[0] == cid {
			c.RatingCount++
			c.RatingSum += v
		}
	}
	return nil
}
func (r *ratingStore) IncrementLockerUseCount(cid uint64) error {
	r.locker[cid-1].UseCount++
	return nil
}
func (r *ratingStore) LockerMyRatings(pid uint64, ids []uint64) (map[uint64]int64, error) {
	out := map[uint64]int64{}
	for _, id := range ids {
		if v, ok := r.ratings[[2]uint64{id, pid}]; ok {
			out[id] = v
		}
	}
	return out, nil
}

func TestLockerLikesAndViews(t *testing.T) {
	var logs syncBuf
	cfg := testConfig()
	cfg.LockerDir = t.TempDir()
	fs := &fakeStore{per: database.Persona{ID: 1, BlazeID: 1000001, Name: "Player1"}}
	rs := &ratingStore{fakeStore: fs, ratings: map[[2]uint64]int64{}}
	s := New(cfg, rs, log.New(&logs, "", 0))
	owner := &connState{Persona: fs.per}
	s.handleLocker(&blaze.Packet{Component: ComponentLocker, Command: LockerCreateContent}, liveCreateContent(t), owner)
	fs.locker[0].Status = 0 // uploaded

	viewer := &connState{Persona: database.Persona{ID: 2, BlazeID: 1000002, Name: "Player2"}}
	call := func(st *connState, cmd uint16, rate int) {
		var w blaze.Writer
		w.TagString("CCAT", "SNAPSHOTS")
		w.TagVar("CID", 1)
		if rate >= 0 {
			w.TagVar("RATE", uint64(rate))
		}
		r := s.handleLocker(&blaze.Packet{Component: ComponentLocker, Command: cmd}, decodeOK(t, w.Bytes()), st)
		if r == nil || r.Error != 0 || len(r.Payload) != 0 {
			t.Fatalf("cmd %#x reply %+v", cmd, r)
		}
	}
	call(viewer, LockerIncrementUseCount, -1)
	call(viewer, LockerIncrementUseCount, -1)
	call(viewer, LockerUpdateRating, 1)
	call(viewer, LockerUpdateRating, 1) // liking twice counts once
	call(owner, LockerUpdateRating, 1)

	list := func(st *connState) []blaze.Field {
		var w blaze.Writer
		w.TagString("CCAT", "SNAPSHOTS")
		w.TagVar("EID", 1000001)
		w.TagVar("MXRC", 4294967295)
		r := s.handleLocker(&blaze.Packet{Component: ComponentLocker, Command: LockerListContent}, decodeOK(t, w.Bytes()), st)
		lk := fieldStructList(decodeOK(t, r.Payload), "LKRS")
		if len(lk) != 1 {
			t.Fatalf("list %v", lk)
		}
		assertSorted(t, lk[0], "ContentInfo")
		return lk[0]
	}
	info := list(viewer)
	if fieldUintAuth(info, "RCNT") != 2 || fieldUintAuth(info, "RATE") != 1 || fieldUintAuth(info, "UCNT") != 2 || fieldUintAuth(info, "URAT") != 1 {
		t.Fatalf("viewer info %v", info)
	}
	third := &connState{Persona: database.Persona{ID: 3, BlazeID: 1000003, Name: "Player3"}}
	if info := list(third); fieldUintAuth(info, "URAT") != 0 || fieldUintAuth(info, "RCNT") != 2 {
		t.Fatalf("third info %v", info)
	}
	// unknown content: still an empty reply
	var w blaze.Writer
	w.TagVar("CID", 99)
	w.TagVar("RATE", 1)
	if r := s.handleLocker(&blaze.Packet{Component: ComponentLocker, Command: LockerUpdateRating}, decodeOK(t, w.Bytes()), viewer); r.Error != 0 {
		t.Fatalf("unknown cid %+v", r)
	}
}
